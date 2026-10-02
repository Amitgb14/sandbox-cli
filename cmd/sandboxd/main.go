// Command sandboxd serves Sandbox API v1 (docs/api/v1.md) for one machine.
//
// Locally it listens on a unix socket that only its owner can open; on a
// self-hosted server, on TCP with a bearer token. It refuses the combination
// that would let anyone on the network create sandboxes: a non-loopback address
// with no token.
//
// The only backend in M2 is the in-memory fake, which runs no host processes and
// isolates nothing — it exists so the API and its conformance suite can be
// exercised end to end. It must be asked for by name; the real backends arrive in
// M5 (Linux) and M6 (macOS).
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/server"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
	"github.com/Amitgb14/sandbox-cli/internal/version"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "sandboxd: "+err.Error())
		os.Exit(1)
	}
}

type listFlag []string

func (l *listFlag) String() string     { return strings.Join(*l, ",") }
func (l *listFlag) Set(v string) error { *l = append(*l, v); return nil }

func run(args []string) error {
	fl := flag.NewFlagSet("sandboxd", flag.ContinueOnError)
	listen := fl.String("listen", "", "unix:///path/to/socket, or host:port (default: a unix socket in the user's runtime directory)")
	tokenFile := fl.String("token-file", "", "file holding the bearer token clients must present")
	backendName := fl.String("backend", "", "which backend to serve: fake (the only one before M5)")
	var allowedHosts listFlag
	fl.Var(&allowedHosts, "allowed-host", "a Host name to answer besides loopback (repeatable)")
	showVersion := fl.Bool("version", false, "print the version and exit")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		fmt.Println("sandboxd " + version.Version)
		return nil
	}

	be, err := newBackend(*backendName)
	if err != nil {
		return err
	}
	token, err := readToken(*tokenFile)
	if err != nil {
		return err
	}
	pol := spec.DefaultPolicy()
	if err := pol.Validate(); err != nil {
		return err
	}

	ln, where, err := openListener(*listen, token != "")
	if err != nil {
		return err
	}
	srv := &http.Server{
		Handler: (&server.Server{Backend: be, Policy: pol, Token: token, AllowedHosts: allowedHosts}).Handler(),
		// Output streams are long-lived, so there is no WriteTimeout; a client that
		// stops reading is noticed through its context.
		ReadHeaderTimeout: 10 * time.Second,
	}

	fmt.Fprintf(os.Stderr, "sandboxd %s: serving API %s on %s, backend %s, token %s\n",
		version.Version, api.Version, where, be.Name(), map[bool]string{true: "required", false: "not required"}[token != ""])
	if be.Name() == "fake" {
		fmt.Fprintln(os.Stderr, "sandboxd: the fake backend runs no VMs and isolates nothing; it is for development and the conformance suite")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 1)
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return srv.Shutdown(shutdown)
}

func newBackend(name string) (backend.Backend, error) {
	switch name {
	case "fake":
		return fake.New(api.CapNetworkPolicyUpdate), nil
	case "":
		return nil, errors.New("--backend is required; the only one before M5 is: fake")
	}
	return nil, fmt.Errorf("unknown backend %q; the only one before M5 is: fake", name)
}

// readToken reads the token file, refusing one other users can read: a token
// that any account on the machine can see protects nothing from those accounts.
func readToken(path string) (string, error) {
	if path == "" {
		return "", nil
	}
	fi, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	if fi.Mode().Perm()&0o077 != 0 {
		return "", fmt.Errorf("%s is readable by others (mode %v); chmod 600 it", path, fi.Mode().Perm())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	tok := strings.TrimSpace(string(data))
	if len(tok) < 16 {
		return "", fmt.Errorf("the token in %s is shorter than 16 characters", path)
	}
	return tok, nil
}

// openListener opens the socket or port to serve on. A TCP address that is not
// loopback needs a token — otherwise anyone who can reach the port can create
// sandboxes and run commands in them.
func openListener(addr string, haveToken bool) (net.Listener, string, error) {
	if addr == "" {
		addr = "unix://" + defaultSocket()
	}
	if path, ok := strings.CutPrefix(addr, "unix://"); ok {
		return listenUnix(path)
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, "", fmt.Errorf("--listen %q: %w", addr, err)
	}
	ip := net.ParseIP(host)
	loopback := host == "localhost" || (ip != nil && ip.IsLoopback())
	if !loopback && !haveToken {
		return nil, "", fmt.Errorf("--listen %s is reachable from other machines; refusing to serve it without --token-file", addr)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, "", err
	}
	return ln, ln.Addr().String(), nil
}

func defaultSocket() string {
	if d := os.Getenv("XDG_RUNTIME_DIR"); d != "" {
		return filepath.Join(d, "sandboxd.sock")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "sandbox", "sandboxd.sock")
}

// listenUnix opens a socket only its owner can connect to. A stale socket from a
// previous run is removed — only if it is a socket, so a mistyped path cannot
// delete a file.
func listenUnix(path string) (net.Listener, string, error) {
	// The kernel's limit on a socket path (sun_path) is 104 bytes on macOS and 108
	// on Linux, and exceeding it fails as a bare "invalid argument" from bind. The
	// smaller limit is checked so the same path works on both.
	if len(path) >= 104 {
		return nil, "", fmt.Errorf("socket path is %d bytes; unix sockets allow at most 103 here — use a shorter --listen path", len(path))
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, "", err
	}
	if fi, err := os.Lstat(path); err == nil {
		if fi.Mode().Type() != fs.ModeSocket {
			return nil, "", fmt.Errorf("%s exists and is not a socket; not removing it", path)
		}
		if c, err := net.Dial("unix", path); err == nil {
			c.Close()
			return nil, "", fmt.Errorf("another sandboxd is already serving %s", path)
		}
		if err := os.Remove(path); err != nil {
			return nil, "", err
		}
	}
	// The umask is narrowed around the bind so the socket is never, even for an
	// instant, connectable by anyone but its owner.
	var ln net.Listener
	var err error
	withPrivateUmask(func() { ln, err = net.Listen("unix", path) })
	if err != nil {
		return nil, "", err
	}
	return ln, "unix://" + path, nil
}

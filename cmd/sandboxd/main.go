// Command sandboxd serves Sandbox API v1 (docs/api/v1.md) for one machine.
//
// Locally it listens on a unix socket that only its owner can open; on a
// self-hosted server, on TCP with a bearer token. It refuses the combinations
// that would let someone else create sandboxes: any TCP address with no token
// (a loopback port is open to every user on the machine), and a non-loopback
// one without TLS.
//
// Backends:
//
//	--backend firecracker   Linux: every sandbox a Firecracker microVM. As root
//	                        (the system service) it adds host-enforced egress and,
//	                        with --jailer, the jailer; unprivileged it serves
//	                        sandboxes with no network, and says so.
//	--backend macos         macOS 26+ on arm64: every sandbox a VM of the native
//	                        `container` runtime. Local, on a unix socket.
//	--backend fake          in memory, no VMs, isolates nothing: for development
//	                        and the conformance suite.
//
// TCP needs a token, and a network address TLS as well; only a unix socket
// may go without a token.
//
// As one node behind a gateway (docs/self-hosting.md): --node-id names the
// node in every sandbox id it makes, --node-label and --capacity-* describe it
// at GET /v1/node, and --client-ca lets only the gateway's certificate connect.
package main

import (
	"context"
	"crypto/tls"
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
	"github.com/Amitgb14/sandbox-cli/internal/audit"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/backend/macos"
	"github.com/Amitgb14/sandbox-cli/internal/metrics"
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
	tlsCert := fl.String("tls-cert", "", "TLS certificate (PEM); required for a non-loopback address")
	tlsKey := fl.String("tls-key", "", "TLS private key (PEM)")
	policyFile := fl.String("policy", "", "operator policy (YAML); default: the built-in policy")
	backendName := fl.String("backend", "", "firecracker, macos or fake")
	containerBin := fl.String("container", "container", "macos: the container CLI")
	stateDir := fl.String("state-dir", defaultStateDir(), "images and per-sandbox state")
	kernel := fl.String("kernel", "", "firecracker: guest kernel (vmlinux)")
	firecracker := fl.String("firecracker", "firecracker", "firecracker: the VMM binary")
	jailer := fl.String("jailer", "", "firecracker: the jailer binary; enables it (root only)")
	agent := fl.String("agent", "", "firecracker: sandbox-guestd for the guest (default: beside this binary)")
	network := fl.Bool("network", os.Geteuid() == 0, "firecracker: host-enforced egress (root only)")
	defaultImage := fl.String("default-image", "", "image for requests that name none (overrides the policy file)")
	var allowedHosts, insecureRegistries listFlag
	fl.Var(&allowedHosts, "allowed-host", "a Host name to answer besides loopback (repeatable)")
	fl.Var(&insecureRegistries, "insecure-registry", "a registry (host:port) to pull from over plain HTTP — a local one; repeatable")
	auditLog := fl.String("audit-log", "", `every sandbox's events, as JSONL (default: <state-dir>/audit/events.jsonl; "none" keeps no log)`)
	var node nodeOptions
	var nodeLabels listFlag
	fl.StringVar(&node.id, "node-id", "", "this sandboxd's name as one node behind a gateway; every sandbox id it makes names it")
	fl.Var(&nodeLabels, "node-label", "key=value describing this node to a gateway (repeatable)")
	fl.Float64Var(&node.cpus, "capacity-cpus", 0, "CPUs offered to sandboxes (default: every CPU)")
	fl.IntVar(&node.memoryMB, "capacity-memory-mb", 0, "memory offered to sandboxes, MiB (default: all of it, on Linux and macOS)")
	fl.IntVar(&node.diskMB, "capacity-disk-mb", 0, "disk offered to sandboxes, MiB (default: the size of the state directory's filesystem)")
	fl.StringVar(&node.clientCA, "client-ca", "", "CA (PEM) whose certificates alone may connect; needs --tls-cert (mutual TLS, for a node behind a gateway)")
	metricsListen := fl.String("metrics-listen", "", "loopback host:port to serve Prometheus metrics on, without a credential; off when empty")
	showVersion := fl.Bool("version", false, "print the version and exit")
	if err := fl.Parse(args); err != nil {
		return err
	}
	if *showVersion {
		fmt.Println("sandboxd " + version.Version)
		return nil
	}
	node.labels = nodeLabels
	labels, err := checkNodeFlags(node, *tlsCert != "")
	if err != nil {
		return err
	}
	if *metricsListen != "" {
		if err := metrics.CheckAddr(*metricsListen); err != nil {
			return err
		}
	}
	logf := func(format string, a ...any) { fmt.Fprintf(os.Stderr, "sandboxd: "+format+"\n", a...) }

	pol := spec.DefaultPolicy()
	if *policyFile != "" {
		p, err := spec.LoadPolicy(*policyFile)
		if err != nil {
			return err
		}
		pol = p
	}
	if *defaultImage != "" {
		pol.DefaultImage = *defaultImage
	}

	be, err := newBackend(*backendName, backendOptions{
		stateDir: *stateDir, kernel: *kernel, firecracker: *firecracker, jailer: *jailer,
		agent: *agent, network: *network, logf: logf, insecureRegistries: insecureRegistries,
		container: *containerBin,
	})
	if err != nil {
		return err
	}
	pol, notes := pol.FitTo(be.Capabilities())
	for _, n := range notes {
		logf("policy: %s", n)
	}
	if err := pol.Validate(); err != nil {
		return err
	}
	token, err := readToken(*tokenFile)
	if err != nil {
		return err
	}

	ln, where, err := openListener(*listen, token != "", *tlsCert != "")
	if err != nil {
		return err
	}
	if *tlsCert != "" {
		cfg, err := serverTLS(*tlsCert, *tlsKey, node.clientCA)
		if err != nil {
			ln.Close()
			return err
		}
		ln = tls.NewListener(ln, cfg)
		where = "https://" + strings.TrimPrefix(where, "tcp://")
	}
	// On by default: a run log is the record of what an agent did after its
	// sandbox is gone, and opting in to one after the fact is not possible.
	logPath := *auditLog
	switch logPath {
	case "":
		logPath = filepath.Join(*stateDir, "audit", "events.jsonl")
	case "none":
		logPath = ""
	}
	nodeCap := capacity(node, *stateDir)
	apiSrv := &server.Server{Backend: be, Policy: pol, Token: token, AllowedHosts: allowedHosts,
		Audit: audit.NewLog(logPath), Logf: logf,
		NodeID: node.id, NodeLabels: labels, Capacity: nodeCap}
	srv := &http.Server{
		Handler: apiSrv.Handler(),
		// Output streams are long-lived, so there is no WriteTimeout; a client that
		// stops reading is noticed through its context.
		ReadHeaderTimeout: 10 * time.Second,
	}

	for _, pl := range pol.Pools {
		img := pl.Image
		if img == "" {
			img = pol.DefaultImage
		}
		logf("pool: keeping %d sandboxes of %s booted ahead of requests", pl.Size, img)
	}
	auditNote := logPath
	if auditNote == "" {
		auditNote = "off"
	}
	logf("%s serving API %s on %s; backend %s; token %s; network ceiling %s; default image %s; audit log %s",
		version.Version, api.Version, where, be.Name(),
		map[bool]string{true: "required", false: "not required"}[token != ""], pol.Network.Ceiling, pol.DefaultImage, auditNote)
	if node.id != "" {
		logf("node %s: capacity %g CPUs, %d MiB memory, %d MiB disk; client certificates %s",
			node.id, nodeCap.CPUs, nodeCap.MemoryMB, nodeCap.DiskMB, map[bool]string{true: "required", false: "not required"}[node.clientCA != ""])
	}
	if be.Name() == "fake" {
		logf("the fake backend runs no VMs and isolates nothing; it is for development and the conformance suite")
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	errc := make(chan error, 2)
	if *metricsListen != "" {
		mln, err := metrics.Listen(*metricsListen)
		if err != nil {
			ln.Close()
			return err
		}
		msrv := &http.Server{Handler: apiSrv.MetricsHandler(), ReadHeaderTimeout: 10 * time.Second}
		defer msrv.Close()
		go func() { errc <- fmt.Errorf("metrics: %w", msrv.Serve(mln)) }()
		logf("serving metrics on http://%s/metrics", mln.Addr())
	}
	go func() { errc <- srv.Serve(ln) }()
	select {
	case err := <-errc:
		return err
	case <-ctx.Done():
	}
	shutdown, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err = srv.Shutdown(shutdown)
	if c, ok := be.(interface{ Close() }); ok {
		c.Close()
	}
	return err
}

type backendOptions struct {
	stateDir, kernel, firecracker, jailer, agent string
	network                                      bool
	logf                                         func(string, ...any)
	insecureRegistries                           []string
	container                                    string
}

func defaultStateDir() string {
	if os.Geteuid() == 0 {
		return "/var/lib/sandboxd"
	}
	if d := os.Getenv("XDG_DATA_HOME"); d != "" {
		return filepath.Join(d, "sandboxd")
	}
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "sandboxd")
}

func newBackend(name string, o backendOptions) (backend.Backend, error) {
	switch name {
	case "fake":
		return fake.New(api.CapNetworkPolicyUpdate, api.CapEgressAllowlist, api.CapVolumes), nil
	case "firecracker":
		return newFirecracker(o)
	case "macos":
		agent := o.agent
		if agent == "" {
			self, _ := os.Executable()
			agent = filepath.Join(filepath.Dir(self), "sandbox-guestd")
		}
		owner, err := macos.Owner(o.stateDir)
		if err != nil {
			return nil, fmt.Errorf("macos backend: --state-dir: %w", err)
		}
		// A snapshot's archive is the size of the sandbox's files: made beside
		// the rest of this sandboxd's state, not wherever $TMPDIR points.
		scratch := filepath.Join(o.stateDir, "snapshot-tmp")
		return macos.New(macos.Config{Container: o.container, Agent: agent, Logf: o.logf, Owner: owner, ScratchDir: scratch})
	case "":
		return nil, errors.New("--backend is required: firecracker, macos or fake")
	}
	return nil, fmt.Errorf("unknown backend %q: want firecracker, macos or fake", name)
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

// openListener opens the socket or port to serve on. Any TCP address needs a
// token — otherwise anyone who can reach the port can create sandboxes, run
// commands in them and type into their terminals. Loopback is no exception: a
// loopback port is reachable by every user on the machine, which is why Studio
// carries a token of its own. Only a unix socket, which listenUnix makes
// owner-only, may go without one. One that is not loopback needs TLS too.
func openListener(addr string, haveToken, haveTLS bool) (net.Listener, string, error) {
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
	if !haveToken {
		if loopback {
			return nil, "", fmt.Errorf("--listen %s is reachable by every user on this machine; refusing to serve it without --token-file (or listen on a unix:// socket, which only you can open)", addr)
		}
		return nil, "", fmt.Errorf("--listen %s is reachable from other machines; refusing to serve it without --token-file", addr)
	}
	// A bearer token over plain HTTP on a network is a token anyone on the path
	// can read and reuse.
	if !loopback && !haveTLS {
		return nil, "", fmt.Errorf("--listen %s is reachable from other machines; refusing to serve it without --tls-cert and --tls-key", addr)
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return nil, "", err
	}
	return ln, "tcp://" + ln.Addr().String(), nil
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

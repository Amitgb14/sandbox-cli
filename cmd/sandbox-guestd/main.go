// Command sandbox-guestd is the guest agent: the only thing inside a sandbox VM
// that the host talks to (internal/guestproto).
//
//	sandbox-guestd init                      PID 1 of a Firecracker guest
//	sandbox-guestd serve --vsock 5000        answer the host over vsock
//	sandbox-guestd serve --stdio             answer one request on stdin/stdout
//	                                         (macOS: one `container exec -i` per request)
//	sandbox-guestd idle                      stay alive as a sandbox's main process (macOS)
//	sandbox-guestd version
//
// It does what the host asks and nothing on its own initiative: it never dials
// out, and it has no request that reads a path back to the host unasked.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/guestproto"
	"github.com/Amitgb14/sandbox-cli/internal/version"
	"github.com/Amitgb14/sandbox-cli/internal/vsock"
)

func main() {
	if os.Getpid() == 1 && (len(os.Args) < 2 || os.Args[1] == "init") {
		runInit() // never returns
	}
	if len(os.Args) < 2 {
		usage()
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = serve(os.Args[2:])
	case "version":
		fmt.Printf("sandbox-guestd %s (protocol %s)\n", version.Version, guestproto.Version)
	case "idle":
		// The main process of a sandbox whose runtime starts its own init (the
		// macOS backend): it only has to stay alive. Each request is a separate
		// `serve --stdio` exec.
		for {
			time.Sleep(time.Hour)
		}
	case "init":
		err = fmt.Errorf("init runs only as PID 1")
	default:
		usage()
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "sandbox-guestd: "+err.Error())
		os.Exit(1)
	}
}

func usage() {
	fmt.Fprintln(os.Stderr, "usage: sandbox-guestd init | serve (--vsock PORT | --stdio | --unix PATH) | idle | version")
	os.Exit(2)
}

// The sandbox user, as the base image creates it. Processes run as this user
// when the agent is root, which it is inside a VM.
const (
	defaultUID  = 1001
	defaultGID  = 1001
	defaultHome = "/sandbox/home"
)

func baseEnv(home string) map[string]string {
	return map[string]string{
		"PATH": "/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin",
		"HOME": home,
		"USER": "sandbox",
		"LANG": "C.UTF-8",
	}
}

func serve(args []string) error {
	fl := flag.NewFlagSet("serve", flag.ContinueOnError)
	port := fl.Uint("vsock", 0, "vsock port to listen on")
	stdio := fl.Bool("stdio", false, "answer one request on stdin/stdout")
	unixPath := fl.String("unix", "", "unix socket to listen on (tests)")
	root := fl.String("root", "/", "directory every path is relative to")
	uid := fl.Int("uid", defaultUID, "uid processes run as (when root); -1 to keep")
	gid := fl.Int("gid", defaultGID, "gid processes run as (when root); -1 to keep")
	home := fl.String("home", defaultHome, "HOME for processes")
	if err := fl.Parse(args); err != nil {
		return err
	}
	env := baseEnv(*home)
	if *root == "/" {
		imageEnv(env)
	}
	s := &guestproto.Server{Root: *root, UID: *uid, GID: *gid, BaseEnv: env}
	switch {
	case *stdio:
		s.ServeConn(stdioConn{})
		return nil
	case *port != 0:
		l, err := vsock.Listen(uint32(*port))
		if err != nil {
			return err
		}
		return s.Serve(l)
	case *unixPath != "":
		l, err := net.Listen("unix", *unixPath)
		if err != nil {
			return err
		}
		return s.Serve(l)
	}
	return fmt.Errorf("serve needs --vsock, --stdio or --unix")
}

// imageEnv merges the image's own environment (recorded at build time, see
// image.ImageConfigPath) over the defaults — an image that puts its tools on a
// PATH of its own must keep it. HOME and USER stay the sandbox user's: they
// describe who the process runs as, which the image does not decide.
func imageEnv(env map[string]string) {
	b, err := os.ReadFile("/etc/sandbox/image.json")
	if err != nil {
		return
	}
	var cfg struct {
		Env []string `json:"Env"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return
	}
	for _, kv := range cfg.Env {
		k, v, ok := strings.Cut(kv, "=")
		if !ok || k == "HOME" || k == "USER" {
			continue
		}
		env[k] = v
	}
}

// stdioConn is stdin and stdout as one connection.
type stdioConn struct{}

func (stdioConn) Read(p []byte) (int, error)  { return os.Stdin.Read(p) }
func (stdioConn) Write(p []byte) (int, error) { return os.Stdout.Write(p) }
func (stdioConn) Close() error                { return nil }

var _ io.ReadWriteCloser = stdioConn{}

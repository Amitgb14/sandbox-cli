package runtime

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// This file is the one place sandbox-cli talks to the engine's API socket
// instead of running its binary, and it is worth saying why rather than
// treating it as a shortcut.
//
// Everything else here shells out to `docker`, which is the right default: it
// is the interface with a stability promise, and it keeps the podman dialect to
// three differences instead of a second client. But `docker attach` sets the
// *client's* terminal to raw mode and refuses outright when stdin is not a tty
// ("the input device is not a TTY"). A web server has no terminal and never
// will, so the CLI cannot express "write these bytes to that container's
// stdin" at all — the one thing an in-browser console is made of.
//
// The API socket can: /containers/{id}/attach upgrades to a raw duplex stream
// that is plain socket I/O afterwards, which the standard library does with no
// dependency. Measured before it was written, against a real container: the
// handshake answers `101 UPGRADED` and bytes written land on stdin with no pty
// anywhere in the path.
//
// The rule this file keeps: it may *read* container output and *write*
// container stdin, and nothing else. It is not a general engine client, and a
// second question that the CLI can already answer does not belong here.

// consoleDialTimeout bounds the connection to the engine, not the stream: an
// attach is expected to stay open for as long as the agent runs.
const consoleDialTimeout = 10 * time.Second

// socketPath returns the engine's API socket.
//
// A fourth entry in the podman dialect (engine.go), and the same shape as the
// others: the engines answer the same question in different places. DOCKER_HOST
// wins when it names a unix socket, because a user who set it means it — but
// only unix, since a tcp daemon is a different security story and this is not
// the code to open one quietly.
func (d *DockerCLI) socketPath() (string, error) {
	if h := os.Getenv("DOCKER_HOST"); h != "" && !d.IsPodman() {
		u, err := url.Parse(h)
		if err != nil {
			return "", fmt.Errorf("DOCKER_HOST %q: %w", h, err)
		}
		if u.Scheme != "unix" {
			return "", fmt.Errorf("a console needs a unix socket, and DOCKER_HOST is %s://%s", u.Scheme, u.Host)
		}
		return u.Path, nil
	}

	var candidates []string
	if d.IsPodman() {
		if run := os.Getenv("XDG_RUNTIME_DIR"); run != "" {
			candidates = append(candidates, filepath.Join(run, "podman", "podman.sock"))
		}
		candidates = append(candidates, "/run/podman/podman.sock")
	} else {
		if home, err := os.UserHomeDir(); err == nil {
			// Docker Desktop's per-user socket. /var/run/docker.sock is usually a
			// symlink to it, but only when the "allow the default socket" setting
			// is on, so the real path is tried too rather than assumed.
			candidates = append(candidates, filepath.Join(home, ".docker", "run", "docker.sock"))
		}
		candidates = append(candidates, "/var/run/docker.sock")
	}
	for _, p := range candidates {
		if fi, err := os.Stat(p); err == nil && fi.Mode()&os.ModeSocket != 0 {
			return p, nil
		}
	}
	return "", fmt.Errorf("no engine API socket found (looked in %s)", strings.Join(candidates, ", "))
}

// attach opens a hijacked attach stream to a container.
//
// The caller says which halves it wants. Asking for stdin alone is a
// write-and-close, which is how a keystroke is delivered; asking for output
// alone is a long-lived read, which is how a console is watched. Keeping them
// separate means no session state on the server: there is nothing to reap when
// a browser tab closes, and two watchers do not have to agree about anything.
func (d *DockerCLI) attach(ctx context.Context, id string, stdin, output bool) (net.Conn, error) {
	sock, err := d.socketPath()
	if err != nil {
		return nil, err
	}
	dialer := net.Dialer{Timeout: consoleDialTimeout}
	conn, err := dialer.DialContext(ctx, "unix", sock)
	if err != nil {
		return nil, fmt.Errorf("connecting to %s: %w", sock, err)
	}

	q := url.Values{"stream": {"1"}}
	if stdin {
		q.Set("stdin", "1")
	}
	if output {
		q.Set("stdout", "1")
		q.Set("stderr", "1")
	}
	// No API version in the path: the daemon serves its newest when none is
	// given, and pinning one here would be a second thing to keep current for no
	// gain — attach has had this shape since v1.12.
	req := fmt.Sprintf("POST /containers/%s/attach?%s HTTP/1.1\r\n"+
		"Host: localhost\r\nConnection: Upgrade\r\nUpgrade: tcp\r\nContent-Length: 0\r\n\r\n",
		url.PathEscape(id), q.Encode())
	if _, err := conn.Write([]byte(req)); err != nil {
		conn.Close()
		return nil, fmt.Errorf("attach handshake: %w", err)
	}

	// Read the response head off the wire so the caller gets the raw stream.
	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("attach handshake: %w", err)
	}
	if !strings.Contains(status, "101") && !strings.Contains(status, "200") {
		conn.Close()
		return nil, fmt.Errorf("attach refused: %s", strings.TrimSpace(status))
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			conn.Close()
			return nil, fmt.Errorf("attach handshake: %w", err)
		}
		if strings.TrimSpace(line) == "" {
			break
		}
	}
	// Anything the reader buffered past the head belongs to the stream.
	if n := br.Buffered(); n > 0 {
		peeked, _ := br.Peek(n)
		return &bufferedConn{Conn: conn, pre: append([]byte(nil), peeked...)}, nil
	}
	return conn, nil
}

// bufferedConn replays bytes that the handshake reader pulled in early. Without
// it the first characters an agent printed can be lost, which looks exactly like
// a console that started mid-sentence.
type bufferedConn struct {
	net.Conn
	pre []byte
}

func (c *bufferedConn) Read(p []byte) (int, error) {
	if len(c.pre) > 0 {
		n := copy(p, c.pre)
		c.pre = c.pre[n:]
		return n, nil
	}
	return c.Conn.Read(p)
}

// ConsoleWrite delivers bytes to a container's stdin.
//
// One connection per write, closed immediately. An attach that stays open holds
// the container's stdin, and a browser that goes away without saying so would
// leave it held — so the short-lived shape is what makes this safe to call from
// an HTTP handler.
func (d *DockerCLI) ConsoleWrite(ctx context.Context, id string, data []byte) error {
	conn, err := d.attach(ctx, id, true, false)
	if err != nil {
		return err
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		_ = conn.SetWriteDeadline(dl)
	}
	if _, err := conn.Write(data); err != nil {
		return fmt.Errorf("writing to stdin: %w", err)
	}
	return nil
}

// ConsoleStream copies a container's output to w until the context is done or
// the container exits.
func (d *DockerCLI) ConsoleStream(ctx context.Context, id string, w io.Writer) error {
	conn, err := d.attach(ctx, id, false, true)
	if err != nil {
		return err
	}
	defer conn.Close()
	go func() {
		<-ctx.Done()
		conn.Close() // unblocks the Read below; there is no other way out of it
	}()
	_, err = io.Copy(w, conn)
	if ctx.Err() != nil {
		return nil
	}
	return err
}

// Console is the capability this file adds to the Runtime family: read a
// running container's output, and write to its stdin. Separated as its own
// interface for the same reason Inspector and Controller are — a caller that
// only lists containers should not have to fake a console to be tested.
type Console interface {
	ConsoleWrite(ctx context.Context, id string, data []byte) error
	ConsoleStream(ctx context.Context, id string, w io.Writer) error
}

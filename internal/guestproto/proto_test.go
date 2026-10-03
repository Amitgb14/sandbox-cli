//go:build unix

package guestproto

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

// serve runs a guest agent rooted at a temporary directory and returns a client
// dialling it over a unix socket — the protocol end to end, with no VM.
func serve(t *testing.T) (*Client, string) {
	t.Helper()
	root := t.TempDir()
	sockDir, err := os.MkdirTemp("", "gp") // short: socket paths are limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sockDir) })
	sock := filepath.Join(sockDir, "g.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	s := &Server{Root: root, UID: -1, GID: -1, BaseEnv: map[string]string{"PATH": os.Getenv("PATH"), "BASE": "1"}}
	go s.Serve(l)
	return &Client{Dial: func(ctx context.Context) (io.ReadWriteCloser, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}}, root
}

// The host runs commands by name; PATH lookup must not escape Root. Tests run on
// the host, so the commands come from the host's PATH joined under Root — which
// is empty. Mirror the host's binaries in by symlink for the ones used.
func withCommands(t *testing.T, root string, names ...string) {
	t.Helper()
	bin := filepath.Join(root, "bin")
	if err := os.MkdirAll(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, n := range names {
		p, err := lookPath(n, os.Getenv("PATH"), "/")
		if err != nil {
			t.Skipf("%s not available on this host", n)
		}
		if err := os.Symlink(p, filepath.Join(bin, n)); err != nil {
			t.Fatal(err)
		}
	}
}

func ctx(t *testing.T) context.Context {
	c, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return c
}

func run(t *testing.T, c *Client, argv []string, env map[string]string, stdin string) (string, string, int) {
	t.Helper()
	var out, errb bytes.Buffer
	p, err := c.Exec(ctx(t), argv, env, "", &out, &errb)
	if err != nil {
		t.Fatalf("exec %v: %v", argv, err)
	}
	if stdin != "" {
		_, _ = p.Stdin().Write([]byte(stdin))
	}
	_ = p.Stdin().Close()
	code := p.Wait()
	return out.String(), errb.String(), code
}

func TestPing(t *testing.T) {
	c, _ := serve(t)
	v, err := c.Ping(ctx(t))
	if err != nil || v != Version {
		t.Fatalf("ping = %q, %v", v, err)
	}
}

func TestExecStreamsAndExits(t *testing.T) {
	c, root := serve(t)
	withCommands(t, root, "sh", "cat")
	env := map[string]string{"PATH": "/bin"}
	out, errOut, code := run(t, c, []string{"sh", "-c", "echo out; echo err >&2; exit 3"}, env, "")
	if out != "out\n" || errOut != "err\n" || code != 3 {
		t.Fatalf("out=%q err=%q code=%d", out, errOut, code)
	}
	out, _, code = run(t, c, []string{"cat"}, env, "through stdin\n")
	if out != "through stdin\n" || code != 0 {
		t.Fatalf("cat: %q %d", out, code)
	}
	// The request's env is merged over the base.
	out, _, _ = run(t, c, []string{"sh", "-c", `echo "$BASE-$MINE"`}, map[string]string{"PATH": "/bin", "MINE": "2"}, "")
	if out != "1-2\n" {
		t.Fatalf("env merge: %q", out)
	}
}

func TestExecUnknownCommand(t *testing.T) {
	c, _ := serve(t)
	_, err := c.Exec(ctx(t), []string{"no-such-command-x9"}, map[string]string{"PATH": "/bin"}, "", io.Discard, io.Discard)
	if !IsCode(err, CodeNoSuchCommand) {
		t.Fatalf("err = %v; want no_such_command", err)
	}
}

// A missing working directory is named as such. Before, the process failed to
// start with ENOENT and that was reported as the command not existing.
func TestExecMissingCwd(t *testing.T) {
	c, root := serve(t)
	withCommands(t, root, "true")
	_, err := c.Exec(ctx(t), []string{"true"}, map[string]string{"PATH": "/bin"}, "/no-such-dir", io.Discard, io.Discard)
	if !IsCode(err, CodeNoSuchCwd) {
		t.Fatalf("err = %v; want no_such_cwd", err)
	}
}

func TestSignalReachesTheProcess(t *testing.T) {
	c, root := serve(t)
	withCommands(t, root, "sleep")
	p, err := c.Exec(ctx(t), []string{"sleep", "30"}, map[string]string{"PATH": "/bin"}, "", io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	if err := p.Signal("TERM"); err != nil {
		t.Fatal(err)
	}
	if code := p.Wait(); code != 128+15 {
		t.Fatalf("exit = %d, want 143", code)
	}
}

// A host that goes away must not leave a process running in its sandbox.
func TestDroppedConnectionKillsTheProcess(t *testing.T) {
	c, root := serve(t)
	withCommands(t, root, "sh", "sleep")
	pc, cancel := context.WithCancel(context.Background())
	pr, pw := io.Pipe()
	p, err := c.Exec(pc, []string{"sh", "-c", "echo $$; exec sleep 30"}, map[string]string{"PATH": "/bin"}, "", pw, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	line := make([]byte, 32)
	n, _ := pr.Read(line)
	pid, err := strconv.Atoi(strings.TrimSpace(string(line[:n])))
	if err != nil {
		t.Fatalf("pid line %q", line[:n])
	}
	go io.Copy(io.Discard, pr)
	cancel()
	p.Wait()
	deadline := time.Now().Add(5 * time.Second)
	for syscall.Kill(pid, 0) == nil {
		if time.Now().After(deadline) {
			t.Fatalf("process %d still running after the host disconnected", pid)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestFiles(t *testing.T) {
	c, root := serve(t)
	cx := ctx(t)
	data := []byte("bytes\x00\xff")
	if err := c.WriteFile(cx, "/workspace/a/b.txt", data); err != nil {
		t.Fatal(err)
	}
	if got, err := os.ReadFile(filepath.Join(root, "workspace/a/b.txt")); err != nil || !bytes.Equal(got, data) {
		t.Fatalf("on disk: %q %v", got, err)
	}
	got, err := c.ReadFile(cx, "/workspace/a/b.txt")
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("read back %q %v", got, err)
	}
	entries, err := c.List(cx, "/workspace/a")
	if err != nil || len(entries) != 1 || entries[0] != (DirEntry{Name: "b.txt", Type: "file", Size: int64(len(data))}) {
		t.Fatalf("list: %+v %v", entries, err)
	}
	for name, check := range map[string]func() error{
		"read a dir":          func() error { _, e := c.ReadFile(cx, "/workspace/a"); return e },
		"remove a non-empty":  func() error { return c.Remove(cx, "/workspace/a") },
		"read a missing file": func() error { _, e := c.ReadFile(cx, "/nope"); return e },
		"traversal":           func() error { _, e := c.ReadFile(cx, "/workspace/../../etc/passwd"); return e },
		"relative":            func() error { return c.WriteFile(cx, "rel", nil) },
		"write through a file": func() error {
			return c.WriteFile(cx, "/workspace/a/b.txt/c", []byte("x"))
		},
	} {
		want := map[string]string{
			"read a dir": CodeIsDir, "remove a non-empty": CodeNotEmpty, "read a missing file": CodeNotFound,
			"traversal": CodeBadRequest, "relative": CodeBadRequest, "write through a file": CodeNotDir,
		}[name]
		if err := check(); !IsCode(err, want) {
			t.Errorf("%s: err = %v; want %s", name, err, want)
		}
	}
	if err := c.Remove(cx, "/workspace/a/b.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReadFile(cx, "/workspace/a/b.txt"); !IsCode(err, CodeNotFound) {
		t.Fatalf("after remove: %v", err)
	}
}

// fakeGuest answers every connection with whatever bytes the test supplies — a
// guest agent that has been replaced by something hostile.
func fakeGuest(t *testing.T, answer func(net.Conn)) *Client {
	t.Helper()
	a, b := net.Pipe()
	go func() {
		br := make([]byte, 4096)
		_, _ = b.Read(br) // the request line
		answer(b)
		b.Close()
	}()
	used := false
	return &Client{Dial: func(context.Context) (io.ReadWriteCloser, error) {
		if used {
			t.Fatal("dialled twice")
		}
		used = true
		return a, nil
	}}
}

// The host refuses what a hostile guest announces before allocating for it.
func TestClientBoundsWhatTheGuestSends(t *testing.T) {
	huge := fakeGuest(t, func(c net.Conn) {
		b, _ := json.Marshal(Response{OK: true, Size: MaxFileBytes + 1})
		c.Write(append(b, '\n'))
	})
	if _, err := huge.ReadFile(ctx(t), "/x"); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Errorf("an announced size over the limit was accepted: %v", err)
	}

	longLine := fakeGuest(t, func(c net.Conn) {
		c.Write(bytes.Repeat([]byte("a"), MaxLine+10))
	})
	if _, err := longLine.Ping(ctx(t)); err == nil {
		t.Error("an answer line over MaxLine was accepted")
	}

	bigFrame := fakeGuest(t, func(c net.Conn) {
		b, _ := json.Marshal(Response{OK: true})
		c.Write(append(b, '\n'))
		c.Write([]byte{FrameStdout, 0xff, 0xff, 0xff, 0xff})
	})
	p, err := bigFrame.Exec(ctx(t), []string{"x"}, nil, "", io.Discard, io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	if code := p.Wait(); code != -1 {
		t.Errorf("an oversized frame did not end the process view: exit %d", code)
	}
}

// A tty exec gets a controlling terminal of the asked size, and a resize frame
// changes it while the process runs.
func TestTTYAndResize(t *testing.T) {
	c, root := serve(t)
	withCommands(t, root, "sh", "stty", "tty")
	var out syncBuffer
	p, err := c.ExecRequest(ctx(t), Request{Argv: []string{"sh"}, Env: map[string]string{"PATH": "/bin"}, Tty: true, Rows: 30, Cols: 100}, &out, &out)
	if err != nil {
		t.Fatal(err)
	}
	in := p.Stdin()
	waitFor := func(want string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for !strings.Contains(out.String(), want) {
			if time.Now().After(deadline) {
				t.Fatalf("never saw %q in:\n%s", want, out.String())
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	in.Write([]byte("stty size; tty\n"))
	waitFor("30 100")
	waitFor("/dev/pts/")
	if err := p.Resize(40, 120); err != nil {
		t.Fatal(err)
	}
	in.Write([]byte("stty size\n"))
	waitFor("40 120")
	in.Write([]byte("exit 7\n"))
	if code := p.Wait(); code != 7 {
		t.Fatalf("exit %d, want 7", code)
	}
}

type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}
func (s *syncBuffer) String() string { s.mu.Lock(); defer s.mu.Unlock(); return s.b.String() }

// A dial reaches a listener on the guest's loopback and carries bytes both
// ways, including the reply after the client's half-close.
func TestDialCarriesBothDirections(t *testing.T) {
	c, _ := serve(t)
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	go func() {
		conn, err := l.Accept()
		if err != nil {
			return
		}
		buf := make([]byte, 64)
		n, _ := conn.Read(buf)
		conn.Write(append([]byte("hi:"), buf[:n]...))
		conn.Close()
	}()
	port := l.Addr().(*net.TCPAddr).Port
	d, err := c.DialPort(ctx(t), port)
	if err != nil {
		t.Fatal(err)
	}
	d.Write([]byte("ping"))
	got, _ := io.ReadAll(d)
	d.Close()
	if string(got) != "hi:ping" {
		t.Fatalf("got %q", got)
	}
}

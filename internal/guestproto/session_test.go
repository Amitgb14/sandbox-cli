//go:build unix

package guestproto

import (
	"bytes"
	"context"
	"io"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// startKept starts argv as a kept process under id, reads its stdout until
// want appears, then drops the connection, as a sandboxd that exits does.
func startKept(t *testing.T, c *Client, id string, argv []string, want string) {
	t.Helper()
	pc, cancel := context.WithCancel(context.Background())
	out := &syncBuffer{}
	p, err := c.ExecRequest(pc, Request{Argv: argv, Env: map[string]string{"PATH": "/bin"}, Keep: true, Session: id}, out, io.Discard)
	if err != nil {
		t.Fatalf("exec %v: %v", argv, err)
	}
	deadline := time.Now().Add(10 * time.Second)
	for !strings.Contains(out.String(), want) {
		if time.Now().After(deadline) {
			t.Fatalf("never saw %q; got %q", want, out.String())
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	p.Wait()
}

func attachAll(t *testing.T, c *Client, id string, offset int64) (string, int, int64) {
	t.Helper()
	var out bytes.Buffer
	var dropped atomic.Int64
	p, err := c.Attach(ctx(t), id, offset, &out, io.Discard, func(n int64) { dropped.Store(n) })
	if err != nil {
		t.Fatalf("attach %s: %v", id, err)
	}
	code := p.Wait()
	return out.String(), code, dropped.Load()
}

// The point of keeping: the process runs on after its connection goes, and a
// later connection gets everything it wrote and how it ended.
func TestKeptProcessOutlivesItsConnection(t *testing.T) {
	c, root := serve(t)
	withCommands(t, root, "sh")
	startKept(t, c, "s1", []string{"sh", "-c", "echo before; sleep 0.3; echo after; exit 7"}, "before")

	ss, err := c.Sessions(ctx(t))
	if err != nil || len(ss) != 1 || ss[0].ID != "s1" {
		t.Fatalf("sessions after the host left: %+v, %v", ss, err)
	}
	out, code, dropped := attachAll(t, c, "s1", 0)
	if out != "before\nafter\n" || code != 7 || dropped != 0 {
		t.Fatalf("attach: out %q code %d dropped %d", out, code, dropped)
	}
	// Delivered, so forgotten.
	if ss, _ := c.Sessions(ctx(t)); len(ss) != 0 {
		t.Fatalf("a delivered session was kept: %+v", ss)
	}
	if _, err := c.Attach(ctx(t), "s1", 0, io.Discard, io.Discard, nil); !IsCode(err, CodeNotFound) {
		t.Fatalf("attach to a forgotten session: %v", err)
	}
}

// The host says how much it has; the guest sends the rest, nothing twice.
func TestAttachFromAnOffset(t *testing.T) {
	c, root := serve(t)
	withCommands(t, root, "sh")
	startKept(t, c, "s2", []string{"sh", "-c", "echo before; sleep 0.2; echo after"}, "before")
	out, code, _ := attachAll(t, c, "s2", int64(len("before\n")))
	if out != "after\n" || code != 0 {
		t.Fatalf("from offset: %q %d", out, code)
	}
}

// Output past what the guest keeps is dropped, oldest first, and the attach
// says how much: a cut that is not reported reads as everything.
func TestKeptOutputIsBoundedAndTheCutReported(t *testing.T) {
	c, root := serve(t)
	withCommands(t, root, "sh", "head")
	const total = 3 * keptBytes
	startKept(t, c, "big", []string{"sh", "-c", "echo start; sleep 0.2; head -c 3145728 /dev/zero"}, "start")
	waitFinished(t, c, "big") // so what follows is the guest's buffer, not live output
	out, code, dropped := attachAll(t, c, "big", 0)
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	if len(out) > keptBytes+32<<10 {
		t.Fatalf("replayed %d bytes, more than the guest keeps", len(out))
	}
	if dropped <= 0 || dropped+int64(len(out)) != total+int64(len("start\n")) {
		t.Fatalf("dropped %d + replayed %d != written %d", dropped, len(out), total+len("start\n"))
	}
}

func TestKeptSessionIDs(t *testing.T) {
	c, root := serve(t)
	withCommands(t, root, "sh", "sleep")
	for _, bad := range []string{"", "../x", "a b", strings.Repeat("a", 65)} {
		_, err := c.ExecRequest(ctx(t), Request{Argv: []string{"sleep", "1"}, Env: map[string]string{"PATH": "/bin"}, Keep: true, Session: bad}, io.Discard, io.Discard)
		if !IsCode(err, CodeBadRequest) {
			t.Errorf("session id %q: %v", bad, err)
		}
	}
	startKept(t, c, "dup", []string{"sh", "-c", "echo up; sleep 30"}, "up")
	_, err := c.ExecRequest(ctx(t), Request{Argv: []string{"sleep", "1"}, Env: map[string]string{"PATH": "/bin"}, Keep: true, Session: "dup"}, io.Discard, io.Discard)
	if !IsCode(err, CodeBadRequest) {
		t.Fatalf("a second process under a taken id: %v", err)
	}
	p, err := c.Attach(ctx(t), "dup", 0, io.Discard, io.Discard, nil)
	if err != nil {
		t.Fatal(err)
	}
	_ = p.Signal("KILL")
	p.Wait()
}

// A process rejoined is as controllable as one never left: a signal reaches it.
func TestSignalAfterAttach(t *testing.T) {
	c, root := serve(t)
	withCommands(t, root, "sh", "sleep")
	startKept(t, c, "sig", []string{"sh", "-c", "echo up; exec sleep 30"}, "up")
	p, err := c.Attach(ctx(t), "sig", 0, io.Discard, io.Discard, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Signal("TERM"); err != nil {
		t.Fatal(err)
	}
	if code := p.Wait(); code != 128+15 {
		t.Fatalf("exit after TERM: %d", code)
	}
}

// One host at a time: a new attach takes the session over, and the old one
// is let go rather than both being fed.
func TestANewAttachTakesOver(t *testing.T) {
	c, root := serve(t)
	withCommands(t, root, "sh", "sleep")
	startKept(t, c, "two", []string{"sh", "-c", "echo up; sleep 0.5; echo done"}, "up")
	first, err := c.Attach(ctx(t), "two", 0, io.Discard, io.Discard, nil)
	if err != nil {
		t.Fatal(err)
	}
	time.Sleep(50 * time.Millisecond)
	out, code, _ := attachAll(t, c, "two", 0)
	if out != "up\ndone\n" || code != 0 {
		t.Fatalf("second attach: %q %d", out, code)
	}
	if got := first.Wait(); got != -1 {
		t.Fatalf("the replaced attach got exit %d, want the connection let go (-1)", got)
	}
}

// Finished sessions a host never comes back for are not held forever.
func TestFinishedSessionsArePruned(t *testing.T) {
	c, root := serve(t)
	withCommands(t, root, "sh")
	for i := 0; i < keptExited+3; i++ {
		startKept(t, c, "p"+strconv.Itoa(i), []string{"sh", "-c", "echo x"}, "x")
	}
	deadline := time.Now().Add(10 * time.Second)
	for {
		ss, err := c.Sessions(ctx(t))
		if err != nil {
			t.Fatal(err)
		}
		running := 0
		for _, s := range ss {
			if s.Running {
				running++
			}
		}
		if running == 0 {
			if len(ss) > keptExited {
				t.Fatalf("%d finished sessions held, more than %d", len(ss), keptExited)
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("sessions still running: %+v", ss)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func waitFinished(t *testing.T, c *Client, id string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for {
		ss, err := c.Sessions(ctx(t))
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range ss {
			if s.ID == id && !s.Running {
				return
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("session %s did not finish: %+v", id, ss)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

package session

import (
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/protocol"
)

// shortTmp points the config root somewhere short.
//
// Necessary rather than tidy: a unix socket address holds about 100 bytes, and
// Go's own t.TempDir() produces paths near that on its own — the session path
// under one is over the limit before the repository name is added. Production
// hits the same wall from a different direction, which is what CheckSockPath is
// for; the tests need a path that fits so they exercise the server rather than
// the refusal.
func shortTmp(t *testing.T) string {
	t.Helper()
	base := filepath.Join(tmpRoot(), "sbx-t")
	if err := os.MkdirAll(base, 0o700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(base, "")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		return resolved
	}
	return dir
}

// waitForSocket blocks until Serve has bound, which it does on its own goroutine.
// Polling rather than a fixed sleep: a sleep that is long enough on this machine
// is the classic flake on a loaded CI runner.
func waitForSocket(t *testing.T, path string, serveErr <-chan error) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		// Serve's own error first. Without this a refusal it reported immediately —
		// a path too long to bind, say — showed up here as a five-second timeout
		// and "the server never bound", which is the symptom and not the reason.
		select {
		case err := <-serveErr:
			t.Fatalf("Serve returned before binding: %v", err)
		default:
		}
		if _, err := os.Stat(path); err == nil {
			if c, err := net.Dial("unix", path); err == nil {
				c.Close()
				return
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("the server never bound %s", path)
}

// tmpRoot prefers /tmp over os.TempDir().
//
// On macOS os.TempDir() is the per-user /var/folders/<...> path, which is 56
// bytes before anything is added to it — so a session socket under it is over the
// 100-byte limit on its own, and the socket tests would test CheckSockPath's
// refusal rather than the server. /tmp is the shortest directory that exists on
// every platform these tests run on.
func tmpRoot() string {
	if st, err := os.Stat("/tmp"); err == nil && st.IsDir() {
		return "/tmp"
	}
	return os.TempDir()
}

func dial(path string) (net.Conn, error) {
	return net.DialTimeout("unix", path, 2*time.Second)
}

func asProtocolError(err error, into **protocol.Error) bool {
	return errors.As(err, into)
}

func itoa(n int) string { return strconv.Itoa(n) }

var errNoFile = os.ErrNotExist

func osReadFile(path string) ([]byte, error) { return os.ReadFile(path) }

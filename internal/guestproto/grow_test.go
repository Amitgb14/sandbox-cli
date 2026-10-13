//go:build unix

package guestproto

import (
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
)

// grow_scratch reaches the guest's grower with the host's size, says when
// the guest has nothing to grow, and refuses a request without a size.
func TestGrowScratch(t *testing.T) {
	var asked []int64
	var fails bool
	c := serveWith(t, &Server{Root: t.TempDir(), UID: -1, GID: -1, GrowScratch: func(min int64) error {
		asked = append(asked, min)
		if fails {
			return errors.New("resize to 1 blocks: no space")
		}
		return nil
	}})
	ctx := context.Background()
	if err := c.GrowScratch(ctx, 4<<30); err != nil || len(asked) != 1 || asked[0] != 4<<30 {
		t.Fatalf("grow: %v, asked %v", err, asked)
	}
	fails = true
	if err := c.GrowScratch(ctx, 8<<30); err == nil {
		t.Fatal("a failing grow answered ok")
	}
	if err := c.GrowScratch(ctx, 0); err == nil || len(asked) != 2 {
		t.Fatalf("no size: %v, asked %v", err, asked)
	}
	if v, err := c.Ping(ctx); err != nil || v != Version {
		t.Fatalf("ping: %q, %v", v, err)
	}

	none := serveWith(t, &Server{Root: t.TempDir(), UID: -1, GID: -1})
	if err := none.GrowScratch(ctx, 4<<30); err == nil {
		t.Fatal("a guest without a scratch filesystem answered ok")
	}
}

func serveWith(t *testing.T, s *Server) *Client {
	t.Helper()
	dir, err := os.MkdirTemp("", "gp")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	sock := filepath.Join(dir, "g.sock")
	l, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go s.Serve(l)
	return &Client{Dial: func(ctx context.Context) (io.ReadWriteCloser, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}}
}

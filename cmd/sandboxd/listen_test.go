package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// Who can reach the API decides whether it needs a token: a unix socket is its
// owner's alone; a TCP port, loopback included, is every local user's, so it
// needs a token; and one other machines can reach needs TLS as well.
func TestOpenListenerRefusesAnUnguardedPort(t *testing.T) {
	for _, tc := range []struct {
		addr          string
		token, tls    bool
		refused, says string
	}{
		{addr: "127.0.0.1:0", refused: "yes", says: "every user on this machine"},
		{addr: "localhost:0", refused: "yes", says: "every user on this machine"},
		{addr: "[::1]:0", refused: "yes", says: "every user on this machine"},
		{addr: "127.0.0.1:0", token: true},
		{addr: "0.0.0.0:0", token: true, refused: "yes", says: "--tls-cert"},
		{addr: "0.0.0.0:0", tls: true, refused: "yes", says: "--token-file"},
	} {
		ln, _, err := openListener(tc.addr, tc.token, tc.tls)
		if ln != nil {
			ln.Close()
		}
		switch {
		case tc.refused == "" && err != nil:
			// [::1] may be missing on a host without IPv6; nothing else should fail.
			t.Errorf("%s token=%v tls=%v: %v", tc.addr, tc.token, tc.tls, err)
		case tc.refused != "" && (err == nil || !strings.Contains(err.Error(), tc.says)):
			t.Errorf("%s token=%v tls=%v: got %v, want a refusal naming %q", tc.addr, tc.token, tc.tls, err, tc.says)
		}
	}
	ln, _, err := openListener("unix://"+filepath.Join(t.TempDir(), "s.sock"), false, false)
	if err != nil {
		t.Fatalf("a unix socket without a token: %v", err)
	}
	ln.Close()
}

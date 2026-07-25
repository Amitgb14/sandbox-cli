package sandbox

import (
	"strings"
	"testing"
)

// TestNormalizePublish_Forms pins the accepted syntax and, more importantly, the
// address every form ends up bound to. The default is the whole point of this
// function: a spec that names no address must never reach docker as one docker
// would expand to 0.0.0.0.
func TestNormalizePublish_Forms(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		// no address given -> localhost, never all interfaces
		{"3000", "127.0.0.1:3000:3000"},
		{"8080:3000", "127.0.0.1:8080:3000"},
		{"  8080:3000  ", "127.0.0.1:8080:3000"},
		{"3000/udp", "127.0.0.1:3000:3000/udp"},
		{"8080:3000/tcp", "127.0.0.1:8080:3000/tcp"},
		{"8000-8010:8000-8010", "127.0.0.1:8000-8010:8000-8010"},

		// an explicit address is honoured exactly, including the wide one
		{"127.0.0.1:8080:3000", "127.0.0.1:8080:3000"},
		{"0.0.0.0:3000:3000", "0.0.0.0:3000:3000"},
		{"192.168.1.5:8080:3000", "192.168.1.5:8080:3000"},
		{"0.0.0.0:8000-8010:8000-8010/udp", "0.0.0.0:8000-8010:8000-8010/udp"},

		// IPv6 is bracketed; its colons are not separators
		{"[::1]:8080:3000", "[::1]:8080:3000"},
		{"[::]:3000:3000", "[::]:3000:3000"},
		{"[::1]:3000", "[::1]:3000:3000"},
	}
	for _, c := range cases {
		got, err := NormalizePublish([]string{c.in})
		if err != nil {
			t.Errorf("NormalizePublish(%q) unexpected error: %v", c.in, err)
			continue
		}
		if len(got) != 1 || got[0] != c.want {
			t.Errorf("NormalizePublish(%q) = %v, want [%q]", c.in, got, c.want)
		}
	}
}

// TestNormalizePublish_DefaultsToLoopback is the security-relevant assertion,
// stated on its own so it cannot be lost in a table edit: no spec that omits an
// address may come out bound to every interface.
func TestNormalizePublish_DefaultsToLoopback(t *testing.T) {
	got, err := NormalizePublish([]string{"3000", "8080:80", "5432:5432/tcp", "9000-9002:9000-9002"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	for _, p := range got {
		if !strings.HasPrefix(p, "127.0.0.1:") {
			t.Errorf("port %q is not bound to loopback; a bare spec must never reach the network", p)
		}
	}
}

func TestNormalizePublish_Rejects(t *testing.T) {
	bad := []string{
		"",                      // empty
		"   ",                   // whitespace only
		"http",                  // not a number
		"0",                     // below range
		"65536",                 // above range
		"-1",                    // missing port number
		"3000:",                 // missing container port
		":3000",                 // missing host port
		"1.2.3.4.5:8080:3000",   // not an IP
		"a:b:c:d",               // too many parts
		"3000/http",             // unknown protocol
		"8000-8010:9000",        // range sizes differ
		"8010-8000:8010-8000",   // backwards range
		"[::1:8080:3000",        // unterminated IPv6
		"[not-an-ip]:8080:3000", // bad IPv6
	}
	for _, in := range bad {
		if got, err := NormalizePublish([]string{in}); err == nil {
			t.Errorf("NormalizePublish(%q) = %v, want an error", in, got)
		}
	}
}

func TestNormalizePublish_EmptyAndDuplicates(t *testing.T) {
	if got, err := NormalizePublish(nil); err != nil || got != nil {
		t.Errorf("NormalizePublish(nil) = %v, %v; want nil, nil", got, err)
	}
	// The same port reached through two spellings collapses; docker would refuse
	// the duplicate bind, and a project config plus a --publish repeating it is an
	// ordinary thing to do.
	got, err := NormalizePublish([]string{"3000", "3000:3000", "127.0.0.1:3000:3000"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0] != "127.0.0.1:3000:3000" {
		t.Errorf("duplicates not collapsed: %v", got)
	}
}

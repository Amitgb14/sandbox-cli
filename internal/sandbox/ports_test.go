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

// TestIngressPorts covers the one deliberate hole in the default-deny INPUT
// chain. Getting this wrong in either direction is bad: too narrow and a
// published dev server stops answering the moment someone adds --allow, too wide
// and the ingress guard means nothing.
func TestIngressPorts(t *testing.T) {
	published, err := NormalizePublish([]string{
		"3000",             // bare -> 127.0.0.1:3000:3000
		"8080:80",          // host:container, container port is what the firewall sees
		"0.0.0.0:443:8443", // explicit address
		"9000:9000/udp",    // protocol carried through
		"[::1]:7000:7000",  // bracketed IPv6 host: its colons precede the port
		"8000-8010",        // a range
		"3000",             // duplicate of the first
	})
	if err != nil {
		t.Fatal(err)
	}
	got := IngressPorts(published)
	want := []string{"tcp:3000", "tcp:80", "tcp:8443", "udp:9000", "tcp:7000", "tcp:8000-8010"}
	if len(got) != len(want) {
		t.Fatalf("IngressPorts = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("IngressPorts[%d] = %q, want %q (full: %v)", i, got[i], want[i], got)
		}
	}
	if len(IngressPorts(nil)) != 0 {
		t.Error("no published ports must yield no carve-out")
	}
}

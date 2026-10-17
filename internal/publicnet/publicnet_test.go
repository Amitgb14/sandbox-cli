package publicnet

import (
	"net"
	"net/netip"
	"strings"
	"testing"
)

func TestAddr(t *testing.T) {
	for s, want := range map[string]bool{
		"8.8.8.8":         true,
		"2606:4700::1111": true,
		"127.0.0.1":       false,
		"::1":             false,
		"10.1.2.3":        false,
		"172.16.0.1":      false,
		"192.168.1.1":     false,
		"169.254.169.254": false, // a cloud's metadata address
		"fe80::1":         false,
		"fd00::1":         false,
		"100.64.0.1":      false,
		"0.0.0.0":         false,
		"224.0.0.1":       false,
		"::ffff:10.0.0.1": false, // a private address written as IPv6
	} {
		if got := Addr(netip.MustParseAddr(s)); got != want {
			t.Errorf("Addr(%s) = %v", s, got)
		}
	}
}

// The check is at the dial: a listener on loopback cannot be reached.
func TestControlRefusesLoopbackAtDial(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	d := &net.Dialer{Control: Control("metrics export", "--flag")}
	_, err = d.Dial("tcp", ln.Addr().String())
	if err == nil || !strings.Contains(err.Error(), "not a public address") || !strings.Contains(err.Error(), "--flag") {
		t.Fatalf("dial loopback: %v", err)
	}
}

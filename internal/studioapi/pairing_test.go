package studioapi

import (
	"errors"
	"net"
	"strings"
	"testing"
)

// The canonical fixture. The iOS app pins this exact string too, so a change
// here that "only" reorders fields or swaps "+" for "%20" is a change to a
// format another repository parses — make it on both sides or not at all.
const canonicalPairingLink = "sandboxstudio://pair#v=1&url=http%3A%2F%2Fmac.tailnet.ts.net%3A8787&token=abc123&name=this+mac"

func TestPairingLink(t *testing.T) {
	tests := []struct {
		name, url, token, label string
		want                    string
	}{
		{"canonical fixture", "http://mac.tailnet.ts.net:8787", "abc123", "this mac", canonicalPairingLink},
		{"no token is omitted, not empty", "http://192.168.1.20:8787", "", "studio",
			"sandboxstudio://pair#v=1&url=http%3A%2F%2F192.168.1.20%3A8787&name=studio"},
		{"no name is omitted", "https://mac.tailnet.ts.net", "abc123", "",
			"sandboxstudio://pair#v=1&url=https%3A%2F%2Fmac.tailnet.ts.net&token=abc123"},
		{"spaces and unicode are escaped", "http://mac.local:8787", "a+b/c=d&e", "Amit’s Mac café",
			"sandboxstudio://pair#v=1&url=http%3A%2F%2Fmac.local%3A8787&token=a%2Bb%2Fc%3Dd%26e&name=Amit%E2%80%99s+Mac+caf%C3%A9"},
		{"ipv6 literal", "http://[fd7a:115c:a1e0::1]:8787", "t", "",
			"sandboxstudio://pair#v=1&url=http%3A%2F%2F%5Bfd7a%3A115c%3Aa1e0%3A%3A1%5D%3A8787&token=t"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := PairingLink(tt.url, tt.token, tt.label)
			if err != nil {
				t.Fatalf("PairingLink: %v", err)
			}
			if got != tt.want {
				t.Errorf("PairingLink(%q, %q, %q)\n got %s\nwant %s", tt.url, tt.token, tt.label, got, tt.want)
			}
		})
	}
}

func TestPairingLinkRefusesURLsThePhoneCannotDial(t *testing.T) {
	for _, raw := range []string{
		"",
		"mac.local:8787",       // no scheme: parses as scheme "mac.local"
		"192.168.1.20:8787",    // no scheme: not a URL at all
		"//mac.local:8787",     // scheme-relative
		"http://",              // no host
		"http:///v1/health",    // no host, with a path
		"ftp://mac.local:8787", // not http
		"sandboxstudio://pair", // a pairing link is not a daemon
	} {
		if link, err := PairingLink(raw, "abc123", "x"); err == nil {
			t.Errorf("PairingLink(%q) = %q, want a refusal", raw, link)
		}
	}
}

func TestIsLoopbackHost(t *testing.T) {
	for host, want := range map[string]bool{
		"127.0.0.1":      true,
		"127.0.0.1:8787": true,
		"localhost":      true,
		"LocalHost:8787": true,
		"::1":            true,
		"[::1]":          true,
		"[::1]:8787":     true,
		// The wildcards listen everywhere; they are not loopback, and they are
		// not dialable either — isWildcardHost is the other half.
		"0.0.0.0":        false,
		"0.0.0.0:8787":   false,
		"[::]:8787":      false,
		"":               false,
		"192.168.1.20":   false,
		"mac.local:8787": false,
		// A name that merely resolves to loopback is the DNS-rebinding shape.
		"localhost.attacker.example": false,
	} {
		if got := IsLoopbackHost(host); got != want {
			t.Errorf("IsLoopbackHost(%q) = %v, want %v", host, got, want)
		}
	}
	for host, want := range map[string]bool{
		"0.0.0.0": true, ":8787": true, "[::]:8787": true, "::": true,
		"127.0.0.1": false, "192.168.1.20:8787": false,
	} {
		if got := isWildcardHost(host); got != want {
			t.Errorf("isWildcardHost(%q) = %v, want %v", host, got, want)
		}
	}
}

// The guard and the pairing check must be one rule: a pairing link is only
// worth printing if hostAllowed would answer the request it produces.
func TestAnswersToHostIsTheGuardsRule(t *testing.T) {
	s := &Server{AllowedHosts: []string{"mac.tailnet.ts.net", "192.168.1.20"}}
	for _, host := range []string{"127.0.0.1:8787", "[::1]:8787", "MAC.tailnet.ts.net:8787", "192.168.1.20:8787", "192.168.1.21:8787", "example.com"} {
		r := newTestRequest(t, "GET", "/v1/health", nil)
		r.Host = host
		if got, want := s.hostAllowed(r), AnswersToHost(host, s.AllowedHosts); got != want {
			t.Errorf("host %q: hostAllowed = %v, AnswersToHost = %v", host, got, want)
		}
	}
}

func fixedIP(ip string) func() (net.IP, error) {
	return func() (net.IP, error) { return net.ParseIP(ip), nil }
}

func TestResolvePairing(t *testing.T) {
	tests := []struct {
		name       string
		opts       PairingOptions
		wantLink   string
		wantNotes  []string // substrings, one per expected note, in order
		wantRefuse string   // substring of the refusal
	}{
		{
			name: "explicit url, allowed host, token",
			opts: PairingOptions{URL: "http://mac.tailnet.ts.net:8787", Addr: "127.0.0.1:8787", Token: "abc123",
				Name: "this mac", AllowedHosts: []string{"mac.tailnet.ts.net"}, Serving: true},
			wantLink: canonicalPairingLink,
			// A qualified name over plain http is what the phone's ATS refuses.
			wantNotes: []string{"will refuse plain http to mac.tailnet.ts.net"},
		},
		{
			name: "https to a qualified name needs no ATS note",
			opts: PairingOptions{URL: "https://mac.tailnet.ts.net", Addr: "127.0.0.1:8787", Token: "abc123",
				AllowedHosts: []string{"mac.tailnet.ts.net"}, Serving: true},
			wantLink: "sandboxstudio://pair#v=1&url=https%3A%2F%2Fmac.tailnet.ts.net&token=abc123",
		},
		{
			name:       "derived from a loopback -addr is refused",
			opts:       PairingOptions{Addr: "127.0.0.1:8787", Token: "abc123", Serving: true},
			wantRefuse: "a phone cannot reach this machine's loopback",
		},
		{
			name:       "derived from localhost is refused",
			opts:       PairingOptions{Addr: "localhost:8787", Token: "abc123"},
			wantRefuse: "loopback only",
		},
		{
			name:       "derived from [::1] is refused",
			opts:       PairingOptions{Addr: "[::1]:8787", Token: "abc123"},
			wantRefuse: "loopback only",
		},
		{
			name: "all interfaces picks the outbound IPv4 and says so",
			opts: PairingOptions{Addr: "0.0.0.0:8787", Token: "abc123", AllowedHosts: []string{"192.168.1.20"},
				Serving: true, OutboundIP: fixedIP("192.168.1.20")},
			wantLink:  "sandboxstudio://pair#v=1&url=http%3A%2F%2F192.168.1.20%3A8787&token=abc123",
			wantNotes: []string{"the link uses 192.168.1.20"},
		},
		{
			name: "all interfaces, but the chosen address is not allowed, while serving",
			opts: PairingOptions{Addr: ":8787", Token: "abc123", Serving: true, OutboundIP: fixedIP("192.168.1.20")},
			// The refusal must name the address it chose, or it asks the reader
			// to allow something they never typed.
			wantRefuse: "the link uses 192.168.1.20, the address this machine routes outbound traffic from. Pass -pair-url to choose another. But this server would refuse the phone's requests for \"192.168.1.20\"",
		},
		{
			name:      "not serving: the Host check is someone else's, so remind rather than refuse",
			opts:      PairingOptions{URL: "http://mac.local:8787", Addr: "127.0.0.1:8787", Token: "abc123"},
			wantLink:  "sandboxstudio://pair#v=1&url=http%3A%2F%2Fmac.local%3A8787&token=abc123",
			wantNotes: []string{"-allow-host mac.local"},
		},
		{
			name:       "all interfaces with no address found",
			opts:       PairingOptions{Addr: "0.0.0.0:8787", Token: "abc123", OutboundIP: func() (net.IP, error) { return nil, errors.New("network is unreachable") }},
			wantRefuse: "network is unreachable",
		},
		{
			name:       "an outbound address that is loopback is not an answer",
			opts:       PairingOptions{Addr: "0.0.0.0:8787", Token: "abc123", OutboundIP: fixedIP("127.0.0.1")},
			wantRefuse: "no address other than loopback",
		},
		{
			name:     "a concrete non-loopback -addr is used as it is",
			opts:     PairingOptions{Addr: "10.0.0.5:4319", Token: "abc123", AllowedHosts: []string{"10.0.0.5"}, Serving: true},
			wantLink: "sandboxstudio://pair#v=1&url=http%3A%2F%2F10.0.0.5%3A4319&token=abc123",
		},
		{
			name:       "a wildcard in -pair-url can never be dialled",
			opts:       PairingOptions{URL: "http://0.0.0.0:8787", Addr: "0.0.0.0:8787", Token: "abc123"},
			wantRefuse: "not an address anything can dial",
		},
		{
			name:       "an ephemeral port has nothing fixed to dial",
			opts:       PairingOptions{Addr: "0.0.0.0:0", Token: "abc123", OutboundIP: fixedIP("192.168.1.20")},
			wantRefuse: "fixed port",
		},
		{
			name:       "invalid -pair-url",
			opts:       PairingOptions{URL: "mac.local:8787", Addr: "0.0.0.0:8787", Token: "abc123"},
			wantRefuse: "absolute http:// or https:// URL",
		},
		{
			name:      "explicit loopback is for the simulator, and says so",
			opts:      PairingOptions{URL: "http://127.0.0.1:8787", Addr: "127.0.0.1:8787", Token: "abc123", Serving: true},
			wantLink:  "sandboxstudio://pair#v=1&url=http%3A%2F%2F127.0.0.1%3A8787&token=abc123",
			wantNotes: []string{"a phone cannot"},
		},
		{
			name: "no token: the link carries none, and the console refusal is quoted",
			opts: PairingOptions{URL: "http://192.168.1.20:8787", Addr: "0.0.0.0:8787", Name: "studio",
				AllowedHosts: []string{"192.168.1.20"}, Serving: true},
			wantLink:  "sandboxstudio://pair#v=1&url=http%3A%2F%2F192.168.1.20%3A8787&name=studio",
			wantNotes: []string{ConsoleNeedsToken},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := ResolvePairing(tt.opts)
			if tt.wantRefuse != "" {
				if err == nil {
					t.Fatalf("got link %q, want a refusal containing %q", p.Link, tt.wantRefuse)
				}
				if !strings.Contains(err.Error(), tt.wantRefuse) {
					t.Fatalf("refusal %q does not contain %q", err, tt.wantRefuse)
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolvePairing: %v", err)
			}
			if p.Link != tt.wantLink {
				t.Errorf("link\n got %s\nwant %s", p.Link, tt.wantLink)
			}
			if len(p.Notes) != len(tt.wantNotes) {
				t.Fatalf("notes = %q, want %d matching %q", p.Notes, len(tt.wantNotes), tt.wantNotes)
			}
			for i, want := range tt.wantNotes {
				if !strings.Contains(p.Notes[i], want) {
					t.Errorf("note %d = %q, want it to contain %q", i, p.Notes[i], want)
				}
			}
		})
	}
}

func TestIOSAllowsCleartext(t *testing.T) {
	for host, want := range map[string]bool{
		"192.168.1.20":       true,
		"fd7a:115c:a1e0::1":  true,
		"mac":                true, // unqualified
		"mac.local":          true,
		"Mac.LOCAL.":         true,
		"localhost":          true,
		"mac.tailnet.ts.net": false,
		"studio.example.com": false,
		"mac.local.example":  false,
	} {
		if got := iosAllowsCleartext(host); got != want {
			t.Errorf("iosAllowsCleartext(%q) = %v, want %v", host, got, want)
		}
	}
}

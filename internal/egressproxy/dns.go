package egressproxy

import (
	"encoding/binary"
	"errors"
	"net"
	"strings"
)

// DNS is the resolver a sandbox's guest is given. It never forwards a query —
// nothing leaves the host on a guest's behalf over DNS — so DNS is not an
// exfiltration channel, however a name is spelled.
//
// An allowlisted name is answered with Sinkhole(remote): an address on the
// host, which is enough, because the guest's tcp/80 and tcp/443 are redirected
// to the proxy whatever their destination, and the proxy resolves the real
// address itself, freshly, by name. Any other name is NXDOMAIN. AAAA and every
// other type get an empty answer, so clients fall back to the A record.
type DNS struct {
	// MatchFor picks the allowlist by the querying address; nil means no answer
	// for anything.
	MatchFor func(remote net.Addr) *Matcher
	// Sinkhole is the address to answer with for a given querier — the host's
	// side of that guest's link.
	Sinkhole func(remote net.Addr) net.IP
	// Log receives one line per refused name.
	Log func(Decision)
}

// ServeUDP answers queries on pc until it is closed.
func (d *DNS) ServeUDP(pc net.PacketConn) error {
	buf := make([]byte, 512)
	for {
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			return err
		}
		if resp := d.answer(buf[:n], addr); resp != nil {
			_, _ = pc.WriteTo(resp, addr)
		}
	}
}

const (
	typeA     = 1
	classIN   = 1
	rcodeOK   = 0
	rcodeFail = 2 // SERVFAIL
	rcodeNX   = 3
	rcodeImpl = 4 // NOTIMP
)

// answer builds the reply to one query, or nil to stay silent (a malformed
// packet gets nothing).
func (d *DNS) answer(q []byte, remote net.Addr) []byte {
	if len(q) < 12 {
		return nil
	}
	id := q[0:2]
	flags := binary.BigEndian.Uint16(q[2:4])
	if flags&0x8000 != 0 { // a response, not a query
		return nil
	}
	opcode := (flags >> 11) & 0xF
	qd := binary.BigEndian.Uint16(q[4:6])
	if qd != 1 {
		return reply(id, flags, rcodeFail, nil, nil, 0, nil)
	}
	name, qtype, qclass, end, err := parseQuestion(q, 12)
	if err != nil {
		return nil
	}
	question := q[12:end]
	if opcode != 0 || qclass != classIN {
		return reply(id, flags, rcodeImpl, question, nil, 0, nil)
	}
	var m *Matcher
	if d.MatchFor != nil {
		m = d.MatchFor(remote)
	}
	if m == nil || !m.Allows(name) {
		if d.Log != nil {
			d.Log(Decision{Host: name, Reason: "DNS: not on the egress allowlist"})
		}
		return reply(id, flags, rcodeNX, question, nil, 0, nil)
	}
	if qtype != typeA {
		return reply(id, flags, rcodeOK, question, nil, 0, nil) // exists, no record of this type
	}
	ip := d.Sinkhole(remote).To4()
	if ip == nil {
		return reply(id, flags, rcodeFail, question, nil, 0, nil)
	}
	return reply(id, flags, rcodeOK, question, ip, typeA, nil)
}

// parseQuestion reads one question: an uncompressed name, type and class.
func parseQuestion(b []byte, off int) (name string, qtype, qclass uint16, end int, err error) {
	var labels []string
	total := 0
	for {
		if off >= len(b) {
			return "", 0, 0, 0, errors.New("short")
		}
		l := int(b[off])
		off++
		if l == 0 {
			break
		}
		if l&0xC0 != 0 || l > 63 { // compression has no place in a question
			return "", 0, 0, 0, errors.New("bad label")
		}
		if off+l > len(b) {
			return "", 0, 0, 0, errors.New("short")
		}
		total += l + 1
		if total > 255 {
			return "", 0, 0, 0, errors.New("name too long")
		}
		labels = append(labels, string(b[off:off+l]))
		off += l
	}
	if off+4 > len(b) {
		return "", 0, 0, 0, errors.New("short")
	}
	qtype = binary.BigEndian.Uint16(b[off : off+2])
	qclass = binary.BigEndian.Uint16(b[off+2 : off+4])
	return strings.ToLower(strings.Join(labels, ".")), qtype, qclass, off + 4, nil
}

// reply assembles a response: the question echoed, and at most one A record
// pointing back at it.
func reply(id []byte, qflags uint16, rcode int, question []byte, ip net.IP, typ uint16, _ []byte) []byte {
	out := make([]byte, 12, 64)
	copy(out[0:2], id)
	flags := uint16(0x8000)  // QR
	flags |= qflags & 0x0100 // RD as asked
	flags |= 0x0080          // RA
	flags |= uint16(rcode)
	binary.BigEndian.PutUint16(out[2:4], flags)
	if question != nil {
		binary.BigEndian.PutUint16(out[4:6], 1)
		out = append(out, question...)
	}
	if ip != nil {
		binary.BigEndian.PutUint16(out[6:8], 1)
		out = append(out, 0xC0, 0x0C) // the question's name
		out = binary.BigEndian.AppendUint16(out, typ)
		out = binary.BigEndian.AppendUint16(out, classIN)
		out = binary.BigEndian.AppendUint32(out, 30) // short TTL: a policy may change
		out = binary.BigEndian.AppendUint16(out, 4)
		out = append(out, ip...)
	}
	return out
}

package egressproxy

import (
	"errors"
	"strings"
)

// Parsing a TLS ClientHello.
//
// Every byte here is chosen by whoever opened the connection — the agent — so
// this is a parser over hostile input. It reads only what it needs (the
// server_name extension), copies nothing large, and treats any inconsistency as
// "no hostname" rather than guessing. There is no allocation proportional to a
// length field the input controls.
//
// The alternative to parsing was to require an explicit HTTP CONNECT, which is
// far simpler — but an agent can decline to use a proxy it was merely *told*
// about, and the whole point of the iptables redirect is that declining is not an
// option. A redirected connection arrives as a raw handshake, so the handshake is
// what has to be read.

// maxClientHello bounds how much is read while looking for SNI. A real
// ClientHello is a few hundred bytes; the record layer permits 16 KiB. Anything
// beyond that is not a handshake we are going to understand, and reading further
// would let a peer decide how much memory this spends.
const maxClientHello = 16 * 1024

var errNotClientHello = errors.New("not a TLS ClientHello")

// SNIFromClientHello extracts the server_name from a TLS ClientHello.
//
// It returns ErrNoHostname when the handshake is well-formed but carries no SNI,
// which is a real case (a client connecting to a bare address) and is refused
// rather than resolved another way — see ErrNoHostname.
func SNIFromClientHello(b []byte) (string, error) {
	// TLS record: type(1) version(2) length(2), then the handshake.
	if len(b) < 5 || b[0] != 0x16 {
		return "", errNotClientHello // 0x16 = handshake
	}
	recLen := int(b[3])<<8 | int(b[4])
	if recLen <= 0 || recLen > maxClientHello {
		return "", errNotClientHello
	}
	r := reader{b: b[5:]}
	if recLen < len(r.b) {
		r.b = r.b[:recLen]
	}

	// Handshake: type(1) length(3) version(2) random(32)
	if t, ok := r.u8(); !ok || t != 0x01 { // 0x01 = client_hello
		return "", errNotClientHello
	}
	if _, ok := r.skip(3 + 2 + 32); !ok {
		return "", errNotClientHello
	}
	// session_id, cipher_suites, compression_methods — each length-prefixed and
	// each skipped wholesale.
	if _, ok := r.vector(1); !ok {
		return "", errNotClientHello
	}
	if _, ok := r.vector(2); !ok {
		return "", errNotClientHello
	}
	if _, ok := r.vector(1); !ok {
		return "", errNotClientHello
	}

	ext, ok := r.vector(2)
	if !ok {
		// No extension block at all: a valid (ancient) hello with no SNI.
		return "", ErrNoHostname
	}
	e := reader{b: ext}
	for {
		typ, ok := e.u16()
		if !ok {
			return "", ErrNoHostname
		}
		body, ok := e.vector(2)
		if !ok {
			return "", ErrNoHostname
		}
		if typ != 0x0000 { // server_name
			continue
		}
		return parseServerNameList(body)
	}
}

// parseServerNameList reads the ServerNameList, returning the first host_name
// entry. The list is a vector of (type, name) pairs; type 0 is host_name and is
// the only one ever used in practice.
func parseServerNameList(b []byte) (string, error) {
	list, ok := (&reader{b: b}).vector(2)
	if !ok {
		return "", ErrNoHostname
	}
	l := reader{b: list}
	for {
		typ, ok := l.u8()
		if !ok {
			return "", ErrNoHostname
		}
		name, ok := l.vector(2)
		if !ok {
			return "", ErrNoHostname
		}
		if typ != 0 {
			continue
		}
		h := normalizeHost(string(name))
		if h == "" || !plausibleHostname(h) {
			return "", ErrNoHostname
		}
		return h, nil
	}
}

// plausibleHostname rejects names that cannot be a DNS host.
//
// It exists to keep junk out of log lines and out of the resolver, not as a
// security boundary — the allowlist is the boundary, and anything that fails to
// match is refused whether it is plausible or not.
func plausibleHostname(h string) bool {
	if len(h) > 253 || strings.ContainsAny(h, " \t\r\n/\\@") {
		return false
	}
	for _, label := range strings.Split(h, ".") {
		if label == "" || len(label) > 63 {
			return false
		}
	}
	return true
}

// reader is a bounds-checked cursor. Every read returns ok=false rather than
// panicking or returning a short value, so a truncated or lying length field
// ends the parse instead of being acted on.
type reader struct{ b []byte }

func (r *reader) u8() (int, bool) {
	if len(r.b) < 1 {
		return 0, false
	}
	v := int(r.b[0])
	r.b = r.b[1:]
	return v, true
}

func (r *reader) u16() (int, bool) {
	if len(r.b) < 2 {
		return 0, false
	}
	v := int(r.b[0])<<8 | int(r.b[1])
	r.b = r.b[2:]
	return v, true
}

func (r *reader) skip(n int) ([]byte, bool) {
	if n < 0 || len(r.b) < n {
		return nil, false
	}
	v := r.b[:n]
	r.b = r.b[n:]
	return v, true
}

// vector reads a length-prefixed block, where lenBytes is the width of the
// prefix. The length is checked against what is actually left, so a field
// claiming more than the buffer holds fails here rather than slicing past it.
func (r *reader) vector(lenBytes int) ([]byte, bool) {
	var n int
	switch lenBytes {
	case 1:
		v, ok := r.u8()
		if !ok {
			return nil, false
		}
		n = v
	case 2:
		v, ok := r.u16()
		if !ok {
			return nil, false
		}
		n = v
	case 3:
		hi, ok := r.u8()
		if !ok {
			return nil, false
		}
		lo, ok := r.u16()
		if !ok {
			return nil, false
		}
		n = hi<<16 | lo
	default:
		return nil, false
	}
	return r.skip(n)
}

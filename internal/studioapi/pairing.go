package studioapi

import (
	"errors"
	"fmt"
	"net"
	"net/url"
	"strings"
)

// Pairing: the two facts a client of this daemon already needs — the address to
// dial and the bearer token — packaged as a link a phone can scan (Sandbox
// Studio for iOS).
//
// It is deliberately not a pairing protocol. The daemon mints no per-device
// credential and keeps no list of paired phones; there is one token, and the
// link carries it. That is also why the link is a secret, and why the command
// prints it only when asked (see -pair in cmd/sandbox-studio-api).
//
// The format is shared with the iOS app, which pins the same fixture string in
// its own tests; change one side and the other stops scanning:
//
//	sandboxstudio://pair#v=1&url=<daemon URL>&token=<token>&name=<label>
//
// The values live in the fragment rather than the query on purpose. A fragment
// is never sent in an HTTP request line, so if this ever becomes an https
// universal link the token cannot land in a web server's access log.

// pairingPrefix is the link up to its first field. The version is part of it
// because a reader refuses a version it does not know rather than guessing at
// fields whose meaning may have changed.
const pairingPrefix = "sandboxstudio://pair#v=1"

// PairingLink builds the link for a daemon at baseURL. token and name are
// omitted when empty — an absent token is a daemon without one, not an empty
// secret. Fields are always in the order v, url, token, name, so the same inputs
// yield the same string on both sides of the format.
//
// Values are escaped with url.QueryEscape (a space becomes "+"), which is the
// escaping the iOS app reproduces byte for byte.
func PairingLink(baseURL, token, name string) (string, error) {
	if _, err := parsePairingURL(baseURL); err != nil {
		return "", err
	}
	var b strings.Builder
	b.WriteString(pairingPrefix)
	b.WriteString("&url=")
	b.WriteString(url.QueryEscape(baseURL))
	if token != "" {
		b.WriteString("&token=")
		b.WriteString(url.QueryEscape(token))
	}
	if name != "" {
		b.WriteString("&name=")
		b.WriteString(url.QueryEscape(name))
	}
	return b.String(), nil
}

// parsePairingURL accepts what the app will dial: an absolute http or https URL
// with a host. Refused here rather than left to the phone, whose only possible
// answer is "this link is broken" after it has already been scanned.
func parsePairingURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("pairing URL %q is not a URL: %w", raw, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return nil, fmt.Errorf("pairing URL %q must be an absolute http:// or https:// URL", raw)
	}
	if u.Hostname() == "" {
		return nil, fmt.Errorf("pairing URL %q has no host for the phone to dial", raw)
	}
	return u, nil
}

// PairingOptions is what the daemon's flags say about the link to print.
type PairingOptions struct {
	URL          string   // -pair-url; empty means derive one from Addr
	Addr         string   // -addr
	Token        string   // -token
	Name         string   // -pair-name, already defaulted by the caller
	AllowedHosts []string // -allow-host

	// Serving is true when this process is the daemon the link points at, so
	// its Host check is a fact rather than a guess about another process.
	Serving bool

	// OutboundIP finds the address this machine routes from, for an
	// all-interfaces -addr. A field so tests do not depend on the network the
	// machine running them happens to be on.
	OutboundIP func() (net.IP, error)
}

// Pairing is a link ready to print, and the things worth saying before it.
type Pairing struct {
	Link string
	// Notes are true statements about this link a reader should see before
	// scanning: which address was chosen for them, and what will not work.
	Notes []string
}

// ResolvePairing decides which address the phone should dial and builds the
// link, refusing where the link could not work.
//
// The refusals are the substance. A phone cannot reach 127.0.0.1 — on a phone,
// that is the phone — and a link naming a host the Host check refuses fails on
// its first request, so printing either would hand somebody a secret that
// scans cleanly and then does nothing, with nothing on screen pointing at why.
func ResolvePairing(o PairingOptions) (Pairing, error) {
	var notes []string
	dial := o.URL
	if dial == "" {
		derived, note, err := pairingURLFromAddr(o.Addr, o.OutboundIP)
		if err != nil {
			return Pairing{}, err
		}
		dial = derived
		if note != "" {
			notes = append(notes, note)
		}
	}

	u, err := parsePairingURL(dial)
	if err != nil {
		return Pairing{}, err
	}
	host := u.Hostname()

	switch {
	case isWildcardHost(host):
		return Pairing{}, fmt.Errorf("%s is not an address anything can dial: %s means every interface "+
			"when listening and nothing when connecting. Pass -pair-url with this machine's LAN or tailnet address", dial, host)
	case IsLoopbackHost(host):
		// Only reachable by naming it in -pair-url, since a derived loopback
		// address is refused above. Allowed because the iOS Simulator shares
		// this machine's network and is the one client that can use it.
		notes = append(notes, fmt.Sprintf("%s is loopback: the iOS Simulator on this machine can dial it, a phone cannot", host))
	case !AnswersToHost(host, o.AllowedHosts):
		if o.Serving {
			// The derived-address note is carried into the refusal: without it
			// the reader is told to allow an address they never typed.
			return Pairing{}, fmt.Errorf("%sthis server would refuse the phone's requests for %q: it answers to loopback "+
				"names and -allow-host only, which is what stops DNS rebinding. Start it with -allow-host %s",
				notesPrefix(notes), host, host)
		}
		notes = append(notes, fmt.Sprintf("the daemon must answer to %q: start it with -allow-host %s "+
			"(this command cannot see a running daemon's flags, so it has not checked)", host, host))
	}

	if u.Scheme == "http" && !iosAllowsCleartext(host) {
		notes = append(notes, fmt.Sprintf("Sandbox Studio for iOS will refuse plain http to %s: it allows cleartext only "+
			"to IP addresses, .local names and unqualified names. Put https in front of it (tailscale serve, a reverse "+
			"proxy), or pass -pair-url with an IP or .local address", host))
	}
	if o.Token == "" {
		notes = append(notes, "no -token is set, so the link carries none and a paired phone can watch but not type — "+
			ConsoleNeedsToken)
	}

	link, err := PairingLink(dial, o.Token, o.Name)
	if err != nil {
		return Pairing{}, err
	}
	return Pairing{Link: link, Notes: notes}, nil
}

// pairingURLFromAddr turns a listen address into one a phone can dial, or says
// why it cannot. The note, when there is one, names an address chosen on the
// reader's behalf and how to choose another: a guess that is not announced is
// one nobody knows to correct.
func pairingURLFromAddr(addr string, outbound func() (net.IP, error)) (string, string, error) {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return "", "", fmt.Errorf("-addr %q is not host:port, so there is no address to pair with; pass -pair-url", addr)
	}
	if port == "" || port == "0" {
		return "", "", fmt.Errorf("-addr %q does not name a fixed port for the phone to dial; pass -pair-url", addr)
	}
	var note string
	switch {
	case IsLoopbackHost(host):
		return "", "", fmt.Errorf("-addr %s listens on loopback only, and a phone cannot reach this machine's loopback. "+
			"Bind an address the phone can reach (with -token) and pass -pair-url with the address it should dial, "+
			"e.g. -addr 0.0.0.0:%s -pair-url http://192.168.1.20:%s -allow-host 192.168.1.20 — or, if a proxy such as "+
			"tailscale serve fronts this port, pass -pair-url with the proxy's URL", addr, port, port)
	case isWildcardHost(host):
		if outbound == nil {
			return "", "", errors.New("no way to find this machine's address; pass -pair-url")
		}
		ip, err := outbound()
		if err != nil {
			return "", "", fmt.Errorf("-addr %s listens on every interface, but this machine's address could not be "+
				"found (%v); pass -pair-url", addr, err)
		}
		if ip == nil || ip.IsLoopback() || ip.IsUnspecified() {
			return "", "", fmt.Errorf("-addr %s listens on every interface, but no address other than loopback was "+
				"found; pass -pair-url", addr)
		}
		// An IP literal rather than the hostname: the iOS app allows cleartext
		// to IP literals and .local names only, and a hostname is whatever the
		// machine was called, qualified or not.
		host = ip.String()
		note = fmt.Sprintf("-addr %s listens on every interface; the link uses %s, the address this machine routes "+
			"outbound traffic from. Pass -pair-url to choose another", addr, host)
	}
	return "http://" + net.JoinHostPort(host, port), note, nil
}

// OutboundIP is the IPv4 address of the interface this machine would route
// outbound traffic from — the LAN address, on an ordinary network.
//
// No packet is sent: connecting a UDP socket only asks the routing table which
// interface would carry it. 192.0.2.1 is TEST-NET-1, reserved for documentation,
// so the question cannot be mistaken for a request to anyone.
func OutboundIP() (net.IP, error) {
	c, err := net.Dial("udp4", "192.0.2.1:9")
	if err != nil {
		return nil, err
	}
	defer c.Close()
	addr, ok := c.LocalAddr().(*net.UDPAddr)
	if !ok {
		return nil, fmt.Errorf("unexpected local address %v", c.LocalAddr())
	}
	return addr.IP, nil
}

func notesPrefix(notes []string) string {
	if len(notes) == 0 {
		return ""
	}
	return strings.Join(notes, ". ") + ". But "
}

// isWildcardHost reports the addresses that mean "every interface" to a
// listener. Bound, they are reachable; dialled, they reach nothing.
func isWildcardHost(host string) bool {
	host = bareHost(host)
	if host == "" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsUnspecified()
}

// iosAllowsCleartext mirrors the App Transport Security exception the iOS app
// carries, NSAllowsLocalNetworking: plain http to IP literals, unqualified names
// and .local names, and nothing else. Said before scanning because the phone's
// refusal is a generic "cannot connect" that does not name the scheme.
func iosAllowsCleartext(host string) bool {
	host = strings.TrimSuffix(bareHost(host), ".")
	if net.ParseIP(host) != nil || !strings.Contains(host, ".") {
		return true
	}
	return strings.HasSuffix(strings.ToLower(host), ".local")
}

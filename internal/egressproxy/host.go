// Package egressproxy enforces the egress allowlist by **name** rather than by
// address.
//
// The firewall it replaces resolves each allowlisted domain once at container
// start and permits those addresses. Two things follow, both confirmed against a
// running sandbox:
//
//   - A host sharing an allowlisted address is reachable. `gist.github.com` — a
//     write endpoint, and so an exfiltration channel — answers under the baseline
//     because it shares `github.com`'s address. Nothing inspects SNI, and which
//     domains share a CDN address is something an attacker can shop for.
//   - Names resolve once, so a rotating record breaks a domain the user *did*
//     allowlist, mid-session, with an error that looks like a network outage.
//
// Both are the same bug: an address is not a name. This package reads the name
// the client actually asked for and decides on that.
//
// # Where it runs
//
// Inside the sandbox container, as its own unprivileged uid. The firewall
// entrypoint — already running as root before it drops privileges — allows egress
// only from the proxy's uid and REDIRECTs everything else to it. The agent cannot
// go around it, because "around it" would mean being a different uid.
//
// That placement is deliberate. A host-side proxy dies with the CLI, which breaks
// `--detach` (the case that is unattended, so the case that most needs it); a
// sidecar container needs a per-run docker network, which reintroduces the
// orphaned-network cleanup this tool is currently free of. In-container costs
// neither.
//
// # What it does NOT do
//
// It does not terminate TLS. It reads the hostname from the handshake and then
// forwards bytes it cannot decrypt. That is enough to enforce an allowlist by
// name and to re-check every hop of a redirect chain — the client opens a *new*
// connection for a redirected host, so the new name is checked like any other.
//
// Injecting a credential would require terminating TLS with a private CA in the
// container's trust store, which is a much larger decision: it puts every prompt,
// every response, the real credentials and a CA private key in one process. That
// is tracked separately and deliberately not started here.
package egressproxy

import (
	"errors"
	"fmt"
	"strings"
)

// ErrNoHostname is returned when a connection carries no name to decide on — a
// TLS handshake with no SNI, or a plain HTTP request with no Host header.
//
// It is an error rather than a fallback to the destination address. Falling back
// would recreate exactly the hole this package exists to close: an unnamed
// connection to an allowlisted address would be permitted, and "connect by IP" is
// the most obvious way to try to evade a name-based allowlist.
var ErrNoHostname = errors.New("connection carries no hostname to check")

// Matcher decides whether a hostname is permitted.
//
// Matching is exact and case-insensitive, with one explicit form of breadth: a
// pattern written `*.example.com` matches any single-or-multi label subdomain but
// **not** `example.com` itself. Everything else is literal.
//
// Subdomains are deliberately not implied. Allowing `github.com` must not also
// allow `gist.github.com`, because that is the precise mistake the
// address-matching firewall made by accident — and `gist.github.com` is a write
// endpoint. Breadth has to be asked for.
type Matcher struct {
	exact    map[string]bool
	suffixes []string // ".example.com" for a "*.example.com" pattern
}

// NewMatcher builds a Matcher from allowlist patterns. Empty and blank patterns
// are ignored rather than rejected: they come from merged config layers where a
// stray empty entry is a formatting accident, not a request.
func NewMatcher(patterns []string) *Matcher {
	m := &Matcher{exact: map[string]bool{}}
	for _, p := range patterns {
		p = normalizeHost(p)
		if p == "" {
			continue
		}
		if rest, ok := strings.CutPrefix(p, "*."); ok && rest != "" {
			m.suffixes = append(m.suffixes, "."+rest)
			continue
		}
		m.exact[p] = true
	}
	return m
}

// Allows reports whether host is permitted.
func (m *Matcher) Allows(host string) bool {
	h := normalizeHost(host)
	if h == "" {
		return false
	}
	if m.exact[h] {
		return true
	}
	for _, suf := range m.suffixes {
		// strings.HasSuffix alone would let "evil-example.com" match ".example.com";
		// the leading dot in the stored suffix is what forces a label boundary.
		if strings.HasSuffix(h, suf) && len(h) > len(suf) {
			return true
		}
	}
	return false
}

// Len reports how many patterns the matcher holds, for the startup log.
func (m *Matcher) Len() int { return len(m.exact) + len(m.suffixes) }

// normalizeHost puts a hostname in the one form comparisons happen in.
//
// DNS is case-insensitive, and a trailing dot is the same name written
// absolutely — `GitHub.com.` and `github.com` are one host, and an allowlist that
// disagreed would be trivially evaded by changing the spelling. A port, if the
// caller left one on, is not part of the name.
func normalizeHost(h string) string {
	h = strings.TrimSpace(h)
	// Strip a :port, but leave IPv6 literals ("[::1]") alone — their colons are
	// part of the address, and the closing bracket tells them apart.
	if i := strings.LastIndexByte(h, ':'); i >= 0 && !strings.Contains(h[i:], "]") {
		if !strings.Contains(h[:i], ":") || strings.HasSuffix(h[:i], "]") {
			h = h[:i]
		}
	}
	h = strings.TrimSuffix(h, ".")
	return strings.ToLower(h)
}

// Decision is the outcome for one connection, for logging and for the caller.
type Decision struct {
	Host    string
	Port    int
	Allowed bool
	Reason  string
}

func (d Decision) String() string {
	verb := "DENY"
	if d.Allowed {
		verb = "allow"
	}
	return fmt.Sprintf("%s %s:%d%s", verb, d.Host, d.Port, reasonSuffix(d.Reason))
}

func reasonSuffix(r string) string {
	if r == "" {
		return ""
	}
	return " (" + r + ")"
}

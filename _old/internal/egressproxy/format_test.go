package egressproxy

import (
	"strings"
	"testing"
)

// TestDenyLinePrefixIsWhatTheProxyPrints checks that the constants and the line
// the proxy actually emits still describe the same thing. They share a definition
// now, so this cannot drift the way three literals could — what it still catches
// is someone reintroducing a literal, which is how the drift started.
func TestDenyLinePrefixIsWhatTheProxyPrints(t *testing.T) {
	// The shipped `main` must build its line from the constant, not from text.
	if !strings.Contains(mainSource, "egressproxy.LogLinePrefix") {
		t.Error("the proxy's main no longer logs via egressproxy.LogLinePrefix.\n" +
			"internal/runtime counts refusals by matching DenyLinePrefix; a literal here\n" +
			"is how that counter silently starts reporting zero.")
	}
	if strings.Contains(mainSource, `"`+LogLinePrefix) {
		t.Error("the proxy's main has a literal copy of LogLinePrefix again")
	}

	// The half that lives in Decision.String: the verb.
	line := Decision{Host: "gist.github.com", Port: 443, Reason: "not on the egress allowlist"}.String()
	if !strings.HasPrefix(line, denyVerb+" ") {
		t.Errorf("Decision.String no longer starts a refusal with %q: %q", denyVerb, line)
	}

	// And the composition, which is what the counter actually sees.
	full := LogLinePrefix + line
	if !strings.HasPrefix(full, DenyLinePrefix) {
		t.Errorf("a real denial line %q does not start with DenyLinePrefix %q", full, DenyLinePrefix)
	}

	// An allowed decision must not look like a denial, or every permitted
	// connection would be counted as a refusal.
	allowed := LogLinePrefix + Decision{Host: "github.com", Port: 443, Allowed: true}.String()
	if strings.HasPrefix(allowed, DenyLinePrefix) {
		t.Errorf("an allowed decision matches DenyLinePrefix: %q", allowed)
	}
}

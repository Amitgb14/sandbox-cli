package egressproxy

import (
	"strings"
	"testing"
)

// TestDenyLinePrefixIsWhatTheProxyPrints checks that the constants and the line
// a decision renders as still describe the same thing.
func TestDenyLinePrefixIsWhatTheProxyPrints(t *testing.T) {
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

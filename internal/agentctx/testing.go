package agentctx

import "testing"

// PinLookups replaces the two per-machine lookups ConversationFor makes, so a
// caller in another package can test the correlation without a real transcript
// store. Returns the restore func.
//
// Exported for internal/studioapi, which cannot reach the unexported vars and
// whose own tests are the ones that need a *positive* case — the shape a suite
// of refusals cannot cover, because a correlation that never matches produces
// the same silence as one that correctly declines.
func PinLookups(t *testing.T, f Finding, sessions []Session) func() {
	t.Helper()
	origResolve, origList := resolveStore, listSessions
	resolveStore = func(string) (Finding, bool) { return f, true }
	listSessions = func(Finding, ListOpts) ([]Session, error) { return sessions, nil }
	return func() { resolveStore, listSessions = origResolve, origList }
}

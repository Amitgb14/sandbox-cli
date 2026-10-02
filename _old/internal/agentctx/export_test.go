package agentctx

import "testing"

// PinLookups replaces the two per-machine lookups ConversationFor makes.
//
// In `export_test.go`, not a production file: a non-test file importing
// `testing` links it — and its `flag`, `regexp` and `runtime/pprof` dependencies
// — into `cmd/sandbox-cli`, which ships. It is also a helper that mutates
// package-level vars, so it must not be reachable from a running daemon at all.
//
// The cost is that only this package's own tests can use it, and
// internal/studioapi's positive case therefore builds a real store on disk
// instead. That is the better test anyway: stubbing these two is what made the
// first version of that test pass with the bug still in it.
func PinLookups(t *testing.T, f Finding, sessions []Session) func() {
	t.Helper()
	origResolve, origList := resolveStore, listSessions
	resolveStore = func(string) (Finding, bool) { return f, true }
	listSessions = func(Finding, ListOpts) ([]Session, error) { return sessions, nil }
	return func() { resolveStore, listSessions = origResolve, origList }
}

package fleet

import "github.com/Amitgb14/sandbox-cli/internal/sandbox"

// The verify wrapper moved to `internal/sandbox`, and these are the names this
// package and its callers already use.
//
// It moved because `internal/session` became its third caller and `session` is
// imported by this package, so reaching back for it would be an import cycle. The
// reasoning — why the verify runs in the container, in the *same* container, in a
// subshell, with the agent argv passed through `"$@"` — is all there, with the
// failure each rule was written for.

// VerifyFailedExit is the container exit code meaning "the agent finished, and the
// task's own verify command said the work is not done". A contract: `fleet status`
// and `fleet land` read it back off containers that exited days ago.
const VerifyFailedExit = sandbox.VerifyFailedExit

// WithVerify returns the container argv for argv followed by verify.
func WithVerify(argv []string, verify string) []string { return sandbox.WithVerify(argv, verify) }

// withVerify is the unexported name this package's own call sites use.
func withVerify(argv []string, verify string) []string { return sandbox.WithVerify(argv, verify) }

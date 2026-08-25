package cli

import "github.com/Amitgb14/sandbox-cli/internal/agents"

// The descriptors the interactive wrappers start their agents from — the same
// table the fleet runner reads.
//
// This is what makes internal/agents' anti-drift claim true rather than
// aspirational. The table was introduced alongside a second copy of Claude's
// bootstrap script, and the two had already diverged before either shipped: one
// prepended the persisted HOME to PATH where the other appended, which is the
// difference between a planted $HOME/.local/bin/git shadowing the image's binary
// and not. Two copies of a security-relevant script drift silently; one copy
// cannot.
//
// Deliberately *not* moved into the table: anything producing host paths — the
// status line settings mount, the project-history mount, the persisted HOME
// itself. A Descriptor says what runs inside the container and which host
// variable names may cross the boundary; where a wrapper reaches on the host is
// the wrapper's business, and is gated by config the table does not see.
//
// A missing descriptor is a programming error, not a runtime condition: these
// names are compiled in.
var (
	claudeAgent   = mustAgent("claude")
	codexAgent    = mustAgent("codex")
	geminiAgent   = mustAgent("gemini")
	opencodeAgent = mustAgent("opencode")
	droidAgent    = mustAgent("droid")
	clineAgent    = mustAgent("cline")
)

func mustAgent(name string) agents.Descriptor {
	d, ok := agents.Lookup(name)
	if !ok {
		panic("cli: no agent descriptor for " + name)
	}
	return d
}

package cli

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"
)

// newContextCmd is the entry point for conversation context: the conversations
// agents leave behind, and the ids you resume them by.
//
// There is deliberately one command here. Finding out *where* an agent keeps its
// transcripts is a real question, but it is one people ask when a listing comes
// up empty — not something to learn a second command for. So `list` answers it
// where it comes up: inline when there is nothing to show, and behind --verbose
// when there is (docs/proposals/shared-context.md).
func newContextCmd() *cobra.Command { return contextCmd(contextScope{}) }

// contextScope is what an agent wrapper has already answered by the time it hands
// over: which agent, and the --project it was given. Carrying the project through
// matters — `sandbox-cli claude --project ~/app context list` must list ~/app's
// sessions, not the ones for whatever directory the shell happens to be in.
type contextScope struct {
	agent   string
	project string
}

// contextCmd builds the context command tree, optionally scoped to one agent.
// The scoped form is what `sandbox-cli claude context …` runs: the same command,
// with the agent already chosen, so the agent-scoped spelling cannot drift from
// the canonical one.
func contextCmd(sc contextScope) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "context",
		Short: "List the conversations agents have had here",
		Long: "Agents keep their conversation transcripts in their own private layout under\n" +
			"HOME, and sandbox-cli persists that HOME so the sessions survive the container.\n" +
			"This is how you see them, and get the id to resume one by.\n\n" +
			"`sandbox-cli context` and `sandbox-cli context list` are the same command.",
		Example: "  sandbox-cli context\n" +
			"  sandbox-cli claude context list\n" +
			"  sandbox-cli claude context list --all",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runContextList(contextListOpts{scope: sc})
		},
	}
	cmd.AddCommand(newContextListCmd(sc))
	return cmd
}

// wrapperSubcommand answers the sandbox-cli subcommands that an agent wrapper
// also accepts under its own name — today just `context`, so that
// `sandbox-cli claude context list` reads the way people expect it to.
//
// This is a deliberate exception to the wrapper rule that everything after the
// sandbox flags is forwarded verbatim, and it is kept narrow on purpose: it fires
// only on a leading `context` token, only when the user did not type an explicit
// `--`, and only for real adapters. `sandbox-cli claude -- context …` still goes
// to the agent, which is the escape hatch for the day an agent grows a `context`
// subcommand of its own.
func wrapperSubcommand(cmd *cobra.Command, rf *runFlags, guest []string, explicit bool) (bool, error) {
	agent := cmd.Annotations[agentAnnotation]
	if !wrapperHandles(agent, guest, explicit) {
		return false, nil
	}
	// Run the scoped command under a stand-in for the real command path. Cobra
	// builds every usage and error line from a command's ancestry, and this tree
	// has none — without the stand-in, `sandbox-cli claude context --help` would
	// tell the user about a command called `context` that they cannot type.
	root := &cobra.Command{Use: "sandbox-cli", SilenceUsage: true, SilenceErrors: true}
	wrapper := &cobra.Command{Use: agent}
	wrapper.AddCommand(contextCmd(contextScope{agent: agent, project: rf.project}))
	root.AddCommand(wrapper)
	root.SetArgs(append([]string{agent, "context"}, guest[1:]...))
	return true, root.Execute()
}

// wrapperSubcommands are the sandbox-cli subcommands an agent wrapper answers in
// its own name. Keeping it a list of one, in one place, is the point: every entry
// here is a word that can never again be passed straight through to an agent
// without a `--`.
var wrapperSubcommands = []string{"context"}

// wrapperHandles is the rule, alone and testable: a real adapter, a leading token
// that names one of sandbox-cli's own subcommands, and no `--` typed by the user.
func wrapperHandles(agent string, guest []string, explicit bool) bool {
	if agent == "" || explicit || len(guest) == 0 {
		return false
	}
	for _, s := range wrapperSubcommands {
		if guest[0] == s {
			return true
		}
	}
	return false
}

// shortenHome renders a path under the user's home as ~/…, which keeps output
// readable without hiding which home a store is actually in.
func shortenHome(p string) string {
	if p == "" {
		return "-"
	}
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if rel, err := filepath.Rel(home, p); err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return filepath.Join("~", rel)
	}
	return p
}

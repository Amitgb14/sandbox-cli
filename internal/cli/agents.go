package cli

import (
	"os"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/config"
)

// Shared plumbing for the agent wrappers (claude/codex/gemini/opencode).

// agentCmds builds every agent adapter command, newest-supported last. This is
// the single list: NewRootCmd registers what it returns, and the contract test
// checks what it returns, so an adapter cannot be wired into the command tree
// while being quietly left out of the test that holds adapters to their shared
// shape (or the reverse).
func agentCmds() []*cobra.Command {
	return []*cobra.Command{
		newClaudeCmd(),
		newCodexCmd(),
		newGeminiCmd(),
		newOpencodeCmd(),
		newClineCmd(),
		newGooseCmd(),
		newCrushCmd(),
		newAiderCmd(),
		newCopilotCmd(),
		newCursorCmd(),
		newQwenCmd(),
		newAmpCmd(),
		newContinueCmd(),
		newOpenhandsCmd(),
		newDroidCmd(),
	}
}

// agentAnnotation keys the cobra annotation carrying an adapter's agent name —
// the same string that becomes its persisted-HOME directory. It exists so the
// wiring can be inspected (by tests, and by anything that wants to enumerate the
// adapters) without every wrapper having to expose its runFlags.
const agentAnnotation = "sandbox-cli/agent"

// finishAgentCmd applies the wiring every agent adapter shares: the common
// sandbox flag set, a sandbox-owned host dir named after the agent mounted as
// its whole HOME so the login survives the throwaway container, and the opt-out
// for it. Wrappers with extra behaviour (claude) add their own flags on top.
func finishAgentCmd(cmd *cobra.Command, rf *runFlags, agent string) *cobra.Command {
	addRunFlags(cmd, rf)
	rf.persistName = agent
	cmd.Flags().BoolVar(&rf.noPersistAuth, "no-persist-auth", false, "do not persist the agent login across runs")
	if cmd.Annotations == nil {
		cmd.Annotations = map[string]string{}
	}
	cmd.Annotations[agentAnnotation] = agent
	return cmd
}

// agentBootstrap and npmAgentBootstrap are the wrappers' spelling of the install
// scripts, which live in internal/agents so a fleet task naming an agent gets
// byte-for-byte the same container argv this wrapper would produce. See
// internal/agents/bootstrap.go for what the script does and why it appends to
// PATH rather than prepending.
func agentBootstrap(bin, install string) []string { return agents.Bootstrap(bin, install) }

func npmAgentBootstrap(bin, pkg string) []string { return agents.NpmBootstrap(bin, pkg) }

// syncEnabled reports whether the resolved configuration permits mounting the
// host's agent history.
//
// The wrapper decides its mounts before newSession loads the configuration, so
// this reads the same layers again rather than threading the config through. A
// second read is a few file opens and cannot disagree with the first, since both
// see the same files; an error here defers to the flag alone, because
// newSession will report the same failure properly a moment later.
//
// It exists because `sync` became a config key when profiles arrived: prod turns
// it off, and a setting the profile promises has to hold whether the user passed
// --no-sync or not.
// Fails closed. This decides whether to mount the one default that reaches a
// host path outside the workspace, so "I could not determine the policy" must
// not resolve to "mount it". Benign today — newSession makes the identical call
// moments later and aborts the run — but a fail-open default one refactor away
// from mattering is not worth keeping.
func syncEnabled(rf *runFlags) bool {
	wd, err := os.Getwd()
	if err != nil {
		return false
	}
	cfg, err := config.LoadProfile(wd, rf.config, rf.profile)
	if err != nil {
		return false
	}
	return cfg.SyncEnabled()
}

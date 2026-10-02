package cli

import "github.com/spf13/cobra"

// The container argv and the forwarded variable names come from the shared
// descriptor (internal/agents), because a fleet can run `agent: opencode`.
// OpenCode is provider-agnostic, so that list spans the providers it can drive
// rather than naming a single vendor; each is forwarded only if you have it set.

func newOpencodeCmd() *cobra.Command {
	rf := &runFlags{}
	cmd := &cobra.Command{
		Use:   "opencode [sandbox-flags --] [opencode-args...]",
		Short: "Run OpenCode inside the sandbox",
		Long: "Runs `opencode` inside the sandbox. Everything you pass is forwarded to\n" +
			"opencode. Sandbox options (leading --flags below, or before a `--` separator)\n" +
			"are consumed first.\n\n" +
			"Your OpenCode login is persisted by default in a sandbox-owned directory\n" +
			"(~/.config/sandbox/agents/opencode, separate from your host OpenCode config),\n" +
			"so `opencode auth login` survives the throwaway container. Use\n" +
			"--no-persist-auth for a session that keeps nothing.\n\n" +
			"OpenCode drives several providers, so the API keys of each are forwarded from\n" +
			"your host environment only if they are set. No other host files are mounted\n" +
			"unless you pass --mount.",
		Example: "  sandbox-cli opencode\n" +
			"  sandbox-cli opencode run 'run the tests'\n" +
			"  sandbox-cli opencode --project ~/app -- run 'fix the failing test'",
		// Forward unknown agent flags instead of rejecting them; sandbox flags are
		// parsed manually from the pre-`--` portion in runWrapper.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWrapper(cmd, rf, args, opencodeAgent.Command, opencodeAgent.EnvAllow, nil)
		},
	}
	// Persists OpenCode's login in a sandbox-owned host dir (~/.config/sandbox/
	// agents/opencode) mounted as the container HOME. Opt out with
	// --no-persist-auth.
	return finishAgentCmd(cmd, rf, "opencode")
}

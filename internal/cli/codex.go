package cli

import "github.com/spf13/cobra"

func newCodexCmd() *cobra.Command {
	rf := &runFlags{}
	cmd := &cobra.Command{
		Use:   "codex [sandbox-flags --] [codex-args...]",
		Short: "Run Codex CLI inside the sandbox",
		Long: "Runs `codex` inside the sandbox. Everything you pass is forwarded to codex.\n" +
			"To set sandbox options, put them before a `--` separator, e.g.\n" +
			"`sandbox-cli codex --project ~/app -- exec 'run the tests'`.\n\n" +
			"Forwards OPENAI_API_KEY and related variables from your host environment\n" +
			"only if they are set. No host files or credentials are mounted unless you\n" +
			"pass --mount explicitly.",
		Example: "  sandbox-cli codex\n" +
			"  sandbox-cli codex exec 'run the tests'\n" +
			"  sandbox-cli codex --project ~/app -- exec 'run the tests'",
		// Forward unknown agent flags instead of rejecting them; sandbox flags are
		// parsed manually from the pre-`--` portion in runWrapper.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWrapper(cmd, rf, args, codexAgent.Command, codexAgent.EnvAllow, nil)
		},
	}
	// Persists Codex's login in a sandbox-owned host dir (~/.config/sandbox/
	// agents/codex) mounted as the container HOME. Opt out with --no-persist-auth.
	return finishAgentCmd(cmd, rf, "codex")
}

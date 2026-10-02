package cli

import "github.com/spf13/cobra"

// clineEnvAllow is the suggested (opt-in) set of host env vars forwarded to a
// Cline session, applied only if present in the host environment. Cline drives
// several providers, so the list spans them rather than naming one vendor.
//
// Cline's several path-valued variables are deliberately absent — CLINE_DATA_DIR,
// CLINE_SANDBOX_DATA_DIR, CLINE_TEAM_DATA_DIR, CLINE_TOOL_APPROVAL_DIR,
// CLINE_LOG_PATH, NODE_EXTRA_CA_CERTS. Each names a host directory that is not
// mounted; forwarding one would point Cline's state at a path that does not
// exist in the container, and CLINE_DATA_DIR in particular would move the login
// out of the persisted HOME, quietly costing you the session on every run.
var clineEnvAllow = []string{
	"ANTHROPIC_API_KEY",
	"CLINE_API_KEY",
	"OPENAI_API_KEY",
	"OPENROUTER_API_KEY",
	"AI_GATEWAY_API_KEY",
	"V0_API_KEY",
}

func newClineCmd() *cobra.Command {
	rf := &runFlags{}
	cmd := &cobra.Command{
		Use:   "cline [sandbox-flags --] [cline-args...]",
		Short: "Run Cline inside the sandbox",
		Long: "Runs `cline` inside the sandbox. Everything you pass is forwarded to cline.\n" +
			"Sandbox options (leading --flags below, or before a `--` separator) are\n" +
			"consumed first.\n\n" +
			"Cline is installed into the sandbox agent home the first time you run it,\n" +
			"which takes a while: the platform binary is around 130MB. It is not baked\n" +
			"into the base image, so you only pay that if you use it.\n\n" +
			"Your Cline login is persisted by default in a sandbox-owned directory\n" +
			"(~/.config/sandbox/agents/cline, separate from your host ~/.cline), so you\n" +
			"log in once. Use --no-persist-auth for a throwaway session.\n\n" +
			"Cline will not open a browser it cannot show you: with an OAuth provider and\n" +
			"no stored credentials it fails with an auth message rather than hanging. Log\n" +
			"in non-interactively with `cline auth --provider anthropic --apikey ...`, or\n" +
			"forward a key from your host environment.\n\n" +
			"Forwards ANTHROPIC_API_KEY and the other provider keys from your host\n" +
			"environment only if they are set. No other host files are mounted unless you\n" +
			"pass --mount.",
		Example: "  sandbox-cli cline\n" +
			"  sandbox-cli cline task 'run the tests'\n" +
			"  sandbox-cli cline --project ~/app -- task 'fix the failing test'",
		// Forward unknown agent flags instead of rejecting them; sandbox flags are
		// parsed manually from the pre-`--` portion in runWrapper.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			agentCmd := npmAgentBootstrap("cline", "cline")
			return runWrapper(cmd, rf, args, agentCmd, clineEnvAllow, nil)
		},
	}
	// Persists Cline's login in a sandbox-owned host dir (~/.config/sandbox/
	// agents/cline) mounted as the container HOME. Opt out with --no-persist-auth.
	return finishAgentCmd(cmd, rf, "cline")
}

package cli

import "github.com/spf13/cobra"

// continueEnvAllow is the suggested (opt-in) set of host env vars forwarded to a
// Continue CLI session, applied only if present in the host environment.
//
// CONTINUE_API_KEY is deliberately absent despite being the obvious candidate
// and still documented upstream: it appears nowhere in the shipped CLI, so
// forwarding it would do nothing while implying it did something. ANTHROPIC_API_KEY
// is what the CLI actually reads.
//
// The path-valued CONTINUE_GLOBAL_DIR, GOOGLE_APPLICATION_CREDENTIALS and
// AWS_LOGIN_CACHE_DIRECTORY are excluded for the usual reason.
var continueEnvAllow = []string{
	"ANTHROPIC_API_KEY",
	"CONTINUE_API_BASE",
	"AWS_ACCESS_KEY_ID",
	"AWS_SECRET_ACCESS_KEY",
	"AWS_SESSION_TOKEN",
	"AWS_REGION",
	"GOOGLE_CLOUD_PROJECT",
}

func newContinueCmd() *cobra.Command {
	rf := &runFlags{}
	cmd := &cobra.Command{
		Use:   "continue [sandbox-flags --] [cn-args...]",
		Short: "Run Continue CLI inside the sandbox",
		Long: "Runs Continue's `cn` inside the sandbox. Everything you pass is forwarded to\n" +
			"it. Sandbox options (leading --flags below, or before a `--` separator) are\n" +
			"consumed first.\n\n" +
			"Continue CLI is installed into the sandbox agent home the first time you run\n" +
			"it. It is not baked into the base image, so you only pay the download if you\n" +
			"use this agent.\n\n" +
			"There is no login to do: hub authentication was removed upstream, and the\n" +
			"`cn login` command in the published docs no longer exists. Authentication is\n" +
			"ANTHROPIC_API_KEY, forwarded from your host environment only if it is set and\n" +
			"written into the config in the persisted agent home on first use.\n\n" +
			"With no config yet, Continue fetches a default one from api.continue.dev. If\n" +
			"you run with --allow, that host needs to be on the allowlist or the agent has\n" +
			"nothing to configure itself from.\n\n" +
			"Your config is persisted by default in a sandbox-owned directory\n" +
			"(~/.config/sandbox/agents/continue, separate from your host ~/.continue). Use\n" +
			"--no-persist-auth for a throwaway session.",
		Example: "  sandbox-cli continue\n" +
			"  sandbox-cli continue -p 'run the tests'\n" +
			"  sandbox-cli continue --allow api.continue.dev",
		// Forward unknown agent flags instead of rejecting them; sandbox flags are
		// parsed manually from the pre-`--` portion in runWrapper.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			agentCmd := npmAgentBootstrap("cn", "@continuedev/cli")
			return runWrapper(cmd, rf, args, agentCmd, continueEnvAllow, nil)
		},
	}
	// Persists Continue's config in a sandbox-owned host dir (~/.config/sandbox/
	// agents/continue) mounted as the container HOME. Opt out with
	// --no-persist-auth.
	return finishAgentCmd(cmd, rf, "continue")
}

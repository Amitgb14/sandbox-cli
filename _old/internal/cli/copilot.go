package cli

import "github.com/spf13/cobra"

// copilotEnvAllow is the suggested (opt-in) set of host env vars forwarded to a
// GitHub Copilot CLI session, applied only if present in the host environment.
//
// A forwarded GitHub token deserves more thought than the other agents' keys: a
// provider API key buys the container inference, while a GitHub PAT can reach
// every repository you can. It is forwarded only when set, like everything else
// here, but the command's help says plainly what that hands over.
//
// COPILOT_HOME and the other path-valued variables are deliberately absent —
// COPILOT_HOME relocates the whole config directory, so a forwarded host value
// would point Copilot's auth at a path the container cannot see.
var copilotEnvAllow = []string{
	"COPILOT_GITHUB_TOKEN",
	"GH_TOKEN",
	"GITHUB_TOKEN",
	"GH_HOST",
	"COPILOT_MODEL",
	"COPILOT_API_URL",
}

func newCopilotCmd() *cobra.Command {
	rf := &runFlags{}
	cmd := &cobra.Command{
		Use:   "copilot [sandbox-flags --] [copilot-args...]",
		Short: "Run GitHub Copilot CLI inside the sandbox",
		Long: "Runs `copilot` inside the sandbox. Everything you pass is forwarded to it.\n" +
			"Sandbox options (leading --flags below, or before a `--` separator) are\n" +
			"consumed first.\n\n" +
			"Copilot CLI is installed into the sandbox agent home the first time you run\n" +
			"it. It is a large download (around 350MB) and is not baked into the base\n" +
			"image, so you only pay for it if you use this agent.\n\n" +
			"Your Copilot login is persisted by default in a sandbox-owned directory\n" +
			"(~/.config/sandbox/agents/copilot, separate from your host ~/.copilot), so\n" +
			"you log in once. Use --no-persist-auth for a throwaway session.\n\n" +
			"`copilot login` uses GitHub's device flow — it prints a code you enter at\n" +
			"github.com/login/device on your host — so no browser is needed in here.\n" +
			"Copilot normally keeps the token in the OS keychain, which a container does\n" +
			"not have, so it will ask once whether to store the token in its config file\n" +
			"instead. Answering yes is what makes the login persist; the file lives in the\n" +
			"sandbox-owned agent home, not in your host ~/.copilot.\n\n" +
			"Forwarding a GitHub token from your host skips the login entirely. Be aware\n" +
			"that it grants the container whatever that token can reach, which is usually\n" +
			"far more than the workspace — it is forwarded only if you have it set.",
		Example: "  sandbox-cli copilot\n" +
			"  sandbox-cli copilot -p 'run the tests'\n" +
			"  sandbox-cli copilot --project ~/app -- -p 'fix the failing test'",
		// Forward unknown agent flags instead of rejecting them; sandbox flags are
		// parsed manually from the pre-`--` portion in runWrapper.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			agentCmd := npmAgentBootstrap("copilot", "@github/copilot")
			return runWrapper(cmd, rf, args, agentCmd, copilotEnvAllow, nil)
		},
	}
	// Persists Copilot's login in a sandbox-owned host dir (~/.config/sandbox/
	// agents/copilot) mounted as the container HOME. Opt out with --no-persist-auth.
	return finishAgentCmd(cmd, rf, "copilot")
}

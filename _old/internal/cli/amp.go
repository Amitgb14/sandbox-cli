package cli

import "github.com/spf13/cobra"

// ampEnvAllow is the suggested (opt-in) set of host env vars forwarded to an Amp
// session, applied only if present in the host environment.
//
// Amp's path-valued variables are deliberately absent — AMP_SETTINGS_FILE,
// AMP_HOME, AMP_RIPGREP_PATH, AMP_LOG_FILE, AMP_PLUGIN_RUNTIME_LOG_FILE. AMP_HOME
// relocates everything Amp stores, the access token included.
var ampEnvAllow = []string{
	"AMP_API_KEY",
	"AMP_URL",
	"AMP_LOG_LEVEL",
	"AMP_SKIP_UPDATE_CHECK",
}

func newAmpCmd() *cobra.Command {
	rf := &runFlags{}
	cmd := &cobra.Command{
		Use:   "amp [sandbox-flags --] [amp-args...]",
		Short: "Run Amp inside the sandbox",
		Long: "Runs `amp` inside the sandbox. Everything you pass is forwarded to amp.\n" +
			"Sandbox options (leading --flags below, or before a `--` separator) are\n" +
			"consumed first.\n\n" +
			"Amp is installed into the sandbox agent home the first time you run it, from\n" +
			"npm rather than the vendor's install script — the script writes into HOME at\n" +
			"a point where that would not survive. It is not baked into the base image, so\n" +
			"you only pay the download if you use this agent.\n\n" +
			"Your Amp login is persisted by default in a sandbox-owned directory\n" +
			"(~/.config/sandbox/agents/amp, separate from your host Amp config), so you log\n" +
			"in once. Use --no-persist-auth for a throwaway session.\n\n" +
			"`amp login` prints a URL to open on your host and accepts the code back in the\n" +
			"terminal, so it completes from in here. Setting AMP_API_KEY in your host\n" +
			"environment skips login altogether.\n\n" +
			"One setting to leave alone: Amp can be told to keep its token in a native\n" +
			"keyring, which migrates the token file into a keyring and deletes it. A\n" +
			"container has no keyring daemon, so turning that on would cost you the login\n" +
			"every run. The default is the file store, which persists correctly here.",
		Example: "  sandbox-cli amp\n" +
			"  sandbox-cli amp -x 'run the tests'\n" +
			"  sandbox-cli amp --project ~/app -- -x 'fix the failing test'",
		// Forward unknown agent flags instead of rejecting them; sandbox flags are
		// parsed manually from the pre-`--` portion in runWrapper.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// @ampcode/cli, not @sourcegraph/amp: the latter is deprecated upstream
			// and its own registry description says it was renamed to this one.
			agentCmd := npmAgentBootstrap("amp", "@ampcode/cli")
			return runWrapper(cmd, rf, args, agentCmd, ampEnvAllow, nil)
		},
	}
	// Persists Amp's login in a sandbox-owned host dir (~/.config/sandbox/
	// agents/amp) mounted as the container HOME. Opt out with --no-persist-auth.
	return finishAgentCmd(cmd, rf, "amp")
}

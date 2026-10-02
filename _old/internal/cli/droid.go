package cli

import "github.com/spf13/cobra"

// The container argv, the forwarded variable names and FACTORY_DISABLE_KEYRING
// all come from the shared descriptor (internal/agents), because a fleet can run
// `agent: droid` — and the keyring setting is exactly the kind of thing that,
// left in the wrapper, would be missing from the unattended path where a login
// prompt cannot be answered.
//
// Droid has an unusually large collection of path-valued variables and every one
// of them is excluded from EnvAllow — FACTORY_HOME_OVERRIDE, FACTORY_EXTRA_CA_CERTS,
// FACTORY_KEYTAR_PATH, FACTORY_RIPGREP_PATH, FACTORY_AGENT_BROWSER_PATH,
// FACTORY_LOG_FILE, FACTORY_RUNTIME_SETTINGS_PATH,
// FACTORY_ORG_MANAGED_SETTINGS_LOCAL_PATH, FACTORY_NPM_MODULES_DIR,
// FACTORY_DROID_BINARY, FACTORY_PROJECT_DIR. FACTORY_HOME_OVERRIDE is the one
// that would cost you the login, since ~/.factory holds auth.json.

func newDroidCmd() *cobra.Command {
	rf := &runFlags{}
	cmd := &cobra.Command{
		Use:   "droid [sandbox-flags --] [droid-args...]",
		Short: "Run Droid inside the sandbox",
		Long: "Runs `droid` inside the sandbox. Everything you pass is forwarded to droid.\n" +
			"Sandbox options (leading --flags below, or before a `--` separator) are\n" +
			"consumed first.\n\n" +
			"Droid is installed into the sandbox agent home the first time you run it\n" +
			"(around 148MB). It is not baked into the base image, so you only pay the\n" +
			"download if you use this agent.\n\n" +
			"Your Droid login is persisted by default in a sandbox-owned directory\n" +
			"(~/.config/sandbox/agents/droid, separate from your host ~/.factory), so you\n" +
			"log in once. Use --no-persist-auth for a throwaway session.\n\n" +
			"Login is a device-code flow — it prints a code and a URL you open on your\n" +
			"host — so no browser is needed in here. FACTORY_API_KEY from your host\n" +
			"environment skips it entirely, which is the usual choice for `droid exec`.\n\n" +
			"The sandbox sets FACTORY_DISABLE_KEYRING. Droid stores credentials in a file\n" +
			"by default today, which persists correctly, and this keeps that true if the\n" +
			"upstream default ever changes — a container has no keyring daemon to fall\n" +
			"back on.",
		Example: "  sandbox-cli droid\n" +
			"  sandbox-cli droid exec 'run the tests'\n" +
			"  sandbox-cli droid --project ~/app -- exec 'fix the failing test'",
		// Forward unknown agent flags instead of rejecting them; sandbox flags are
		// parsed manually from the pre-`--` portion in runWrapper.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			afterParse := func() error {
				// After parsing: pflag's string array replaces its initial contents on
				// the first --env, which would drop this for anyone passing one.
				rf.env = append(rf.env, droidAgent.Env...)
				return nil
			}
			return runWrapper(cmd, rf, args, droidAgent.Command, droidAgent.EnvAllow, afterParse)
		},
	}
	// Persists Droid's login in a sandbox-owned host dir (~/.config/sandbox/
	// agents/droid) mounted as the container HOME. Opt out with --no-persist-auth.
	return finishAgentCmd(cmd, rf, "droid")
}

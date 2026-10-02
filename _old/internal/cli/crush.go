package cli

import "github.com/spf13/cobra"

// crushEnvAllow is the suggested (opt-in) set of host env vars forwarded to a
// Crush session, applied only if present in the host environment. Crush speaks
// to a long list of providers; the common ones are here, and anything else it
// supports can be added per-run with --env-allow rather than carried by every
// user forever.
//
// Crush's path-valued variables are deliberately absent — CRUSH_GLOBAL_CONFIG,
// CRUSH_GLOBAL_DATA, CRUSH_CACHE_DIR, CRUSH_SKILLS_DIR, and the XDG_* trio that
// Crush also honours. CRUSH_GLOBAL_DATA is the one to watch: credentials live
// under it, so a forwarded host path would move the login somewhere the
// container cannot see and lose it every run.
var crushEnvAllow = []string{
	"ANTHROPIC_API_KEY",
	"OPENAI_API_KEY",
	"GEMINI_API_KEY",
	"OPENROUTER_API_KEY",
	"GROQ_API_KEY",
	"HYPER_API_KEY",
	"AWS_ACCESS_KEY_ID",
	"AWS_SECRET_ACCESS_KEY",
	"AWS_REGION",
	"AZURE_OPENAI_API_KEY",
	"AZURE_OPENAI_API_ENDPOINT",
}

func newCrushCmd() *cobra.Command {
	rf := &runFlags{}
	cmd := &cobra.Command{
		Use:   "crush [sandbox-flags --] [crush-args...]",
		Short: "Run Crush inside the sandbox",
		Long: "Runs `crush` inside the sandbox. Everything you pass is forwarded to crush.\n" +
			"Sandbox options (leading --flags below, or before a `--` separator) are\n" +
			"consumed first.\n\n" +
			"Crush is installed into the sandbox agent home the first time you run it. It\n" +
			"is not baked into the base image, so you only pay the download if you use it.\n\n" +
			"Your Crush login is persisted by default in a sandbox-owned directory\n" +
			"(~/.config/sandbox/agents/crush, separate from your host Crush config), so you\n" +
			"log in once. Use --no-persist-auth for a throwaway session.\n\n" +
			"`crush login` is a device-code flow: it shows a short code, and you open the\n" +
			"page on your host and paste it. Nothing needs a browser inside the container\n" +
			"and nothing listens on localhost, so it works here as-is.\n\n" +
			"Forwards the common provider keys from your host environment only if they are\n" +
			"set; Crush supports many more, which you can add with --env-allow. No host\n" +
			"files are mounted unless you pass --mount.",
		Example: "  sandbox-cli crush\n" +
			"  sandbox-cli crush run 'run the tests'\n" +
			"  sandbox-cli crush --project ~/app -- run 'fix the failing test'",
		// Forward unknown agent flags instead of rejecting them; sandbox flags are
		// parsed manually from the pre-`--` portion in runWrapper.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			agentCmd := npmAgentBootstrap("crush", "@charmland/crush")
			return runWrapper(cmd, rf, args, agentCmd, crushEnvAllow, nil)
		},
	}
	// Persists Crush's login in a sandbox-owned host dir (~/.config/sandbox/
	// agents/crush) mounted as the container HOME. Opt out with --no-persist-auth.
	return finishAgentCmd(cmd, rf, "crush")
}

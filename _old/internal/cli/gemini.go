package cli

import "github.com/spf13/cobra"

// The container argv and the forwarded variable names come from the shared
// descriptor (internal/agents), because a fleet can run `agent: gemini` and the
// two paths must start the same agent the same way.
//
// GOOGLE_APPLICATION_CREDENTIALS is deliberately absent from that list: it names
// a host file path that is not mounted, so forwarding it would only produce a
// confusing "credentials file not found" instead of a clean auth prompt. Mount
// it explicitly (`--mount ~/adc.json:/sandbox/home/adc.json:ro --env
// GOOGLE_APPLICATION_CREDENTIALS=/sandbox/home/adc.json`) if you want it.

func newGeminiCmd() *cobra.Command {
	rf := &runFlags{}
	cmd := &cobra.Command{
		Use:   "gemini [sandbox-flags --] [gemini-args...]",
		Short: "Run Gemini CLI inside the sandbox",
		Long: "Runs `gemini` inside the sandbox. Everything you pass is forwarded to gemini,\n" +
			"so `sandbox-cli gemini --yolo` just works. Sandbox options (leading --flags\n" +
			"below, or before a `--` separator) are consumed first.\n\n" +
			"Your Gemini login is persisted by default in a sandbox-owned directory\n" +
			"(~/.config/sandbox/agents/gemini, separate from your host ~/.gemini), so you\n" +
			"log in once. Use --no-persist-auth for a throwaway session.\n\n" +
			"Browser-based Google sign-in has no browser inside the container: gemini\n" +
			"prints a URL you open on the host, and the resulting credentials land in the\n" +
			"persisted agent home. Forwarding GEMINI_API_KEY from your host environment\n" +
			"skips that step entirely.\n\n" +
			"Forwards GEMINI_API_KEY and related variables from your host environment only\n" +
			"if they are set. No other host files are mounted unless you pass --mount.",
		Example: "  sandbox-cli gemini\n" +
			"  sandbox-cli gemini --yolo\n" +
			"  sandbox-cli gemini --project ~/app -- -p 'run the tests'",
		// Forward unknown agent flags instead of rejecting them; sandbox flags are
		// parsed manually from the pre-`--` portion in runWrapper.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			return runWrapper(cmd, rf, args, geminiAgent.Command, geminiAgent.EnvAllow, nil)
		},
	}
	// Persists Gemini's login in a sandbox-owned host dir (~/.config/sandbox/
	// agents/gemini) mounted as the container HOME. Opt out with --no-persist-auth.
	return finishAgentCmd(cmd, rf, "gemini")
}

package cli

import "github.com/spf13/cobra"

// codebuffEnvAllow is empty, and that is a finding rather than an omission.
//
// Codebuff's CLI documents no environment variable for credentials — not in
// `--help`, not in its npm README (checked 2026-08-25) — and its `login`
// subcommand is the route it does document. An adapter that forwarded a guessed
// name would imply a setup route that may not exist, which is worse than a short
// list: the login persists in the sandbox-owned HOME either way, which is the
// promise that matters here.
var codebuffEnvAllow []string

func newCodebuffCmd() *cobra.Command {
	rf := &runFlags{}
	cmd := &cobra.Command{
		Use:   "codebuff [sandbox-flags --] [codebuff-args...]",
		Short: "Run Codebuff inside the sandbox",
		Long: "Runs `codebuff` inside the sandbox. Everything you pass is forwarded to it.\n" +
			"Sandbox options (leading --flags below, or before a `--` separator) are\n" +
			"consumed first.\n\n" +
			"Codebuff installs in two stages, and both land in the persisted agent home:\n" +
			"the npm package is a launcher, and on its first start it downloads a ~46MB\n" +
			"binary. Neither is baked into the base image.\n\n" +
			"Your login is persisted by default in a sandbox-owned directory\n" +
			"(~/.config/sandbox/agents/codebuff), so you log in once with `codebuff login`.\n" +
			"Use --no-persist-auth for a throwaway session.\n\n" +
			"A prompt is a bare positional — `codebuff 'fix the failing test'` — and there\n" +
			"is no documented flag for a non-interactive run, which is why Studio, a fleet\n" +
			"and the SDKs do not offer Codebuff. See internal/agents for what a descriptor\n" +
			"requires.",
		Example: "  sandbox-cli codebuff\n" +
			"  sandbox-cli codebuff 'explain this repository'\n" +
			"  sandbox-cli codebuff --project ~/app -- --plan 'add a health endpoint'",
		// Forward unknown agent flags instead of rejecting them; sandbox flags are
		// parsed manually from the pre-`--` portion in runWrapper.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			agentCmd := npmAgentBootstrap("codebuff", "codebuff")
			return runWrapper(cmd, rf, args, agentCmd, codebuffEnvAllow, nil)
		},
	}
	// Persists Codebuff's login in a sandbox-owned host dir (~/.config/sandbox/
	// agents/codebuff) mounted as the container HOME. Opt out with --no-persist-auth.
	return finishAgentCmd(cmd, rf, "codebuff")
}

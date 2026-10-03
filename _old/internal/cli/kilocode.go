package cli

import "github.com/spf13/cobra"

// kilocodeEnvAllow is the suggested (opt-in) set of host env vars forwarded to a
// Kilo Code session.
//
// Kilo Code's CLI is an opencode fork — its own log lines say so, and its command
// surface (`run`, `auth`, `serve`, `acp`, `mcp`, `models`) is opencode's — so it
// reads the same provider keys, and this list is opencode's for that reason
// rather than by assumption. `KILOCODE_API_KEY` is their own gateway's name and
// is the one entry here that is unverified: if the CLI ignores it, the cost is a
// variable that crosses and is not read, which is the harmless direction.
//
// No path-valued variables, for the reason every adapter here excludes them: each
// names a host directory that is not mounted, and one pointing at the credential
// store would move the login out of the persisted HOME and discard it each run.
var kilocodeEnvAllow = []string{
	"KILOCODE_API_KEY",
	"ANTHROPIC_API_KEY",
	"OPENAI_API_KEY",
	"GEMINI_API_KEY",
	"GROQ_API_KEY",
	"OPENROUTER_API_KEY",
}

func newKilocodeCmd() *cobra.Command {
	rf := &runFlags{}
	cmd := &cobra.Command{
		Use:   "kilocode [sandbox-flags --] [kilocode-args...]",
		Short: "Run Kilo Code CLI inside the sandbox",
		Long: "Runs `kilocode` inside the sandbox. Everything you pass is forwarded to it.\n" +
			"Sandbox options (leading --flags below, or before a `--` separator) are\n" +
			"consumed first.\n\n" +
			"Kilo Code is installed into the sandbox agent home the first time you run it.\n" +
			"It is not baked into the base image, so you only pay for it if you use it.\n\n" +
			"Your login is persisted by default in a sandbox-owned directory\n" +
			"(~/.config/sandbox/agents/kilocode), so `kilocode auth` is a one-off. Use\n" +
			"--no-persist-auth for a throwaway session.\n\n" +
			"`kilocode run <message>` is its non-interactive mode, the same shape as\n" +
			"opencode's, which it is a fork of. That has not been verified end to end\n" +
			"here — nobody has an account to run one with — so Studio, a fleet and the\n" +
			"SDKs do not offer Kilo Code until somebody does.",
		Example: "  sandbox-cli kilocode\n" +
			"  sandbox-cli kilocode run 'explain this repository'\n" +
			"  sandbox-cli kilocode --project ~/app -- run 'fix the failing test'",
		// Forward unknown agent flags instead of rejecting them; sandbox flags are
		// parsed manually from the pre-`--` portion in runWrapper.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// The binary is `kilocode`; its own help calls the command `kilo`, which
			// is the name inside the tool rather than on disk.
			agentCmd := npmAgentBootstrap("kilocode", "@kilocode/cli")
			return runWrapper(cmd, rf, args, agentCmd, kilocodeEnvAllow, nil)
		},
	}
	// Persists Kilo Code's login in a sandbox-owned host dir (~/.config/sandbox/
	// agents/kilocode) mounted as the container HOME. Opt out with --no-persist-auth.
	return finishAgentCmd(cmd, rf, "kilocode")
}

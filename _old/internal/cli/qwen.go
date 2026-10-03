package cli

import "github.com/spf13/cobra"

// qwenEnvAllow is the suggested (opt-in) set of host env vars forwarded to a
// Qwen Code session, applied only if present in the host environment.
//
// Qwen's path-valued variables are deliberately absent — QWEN_HOME,
// QWEN_CODE_SYSTEM_SETTINGS_PATH, QWEN_CODE_SYSTEM_DEFAULTS_PATH,
// QWEN_CODE_TRUSTED_FOLDERS_PATH, QWEN_CODE_MCP_APPROVALS_PATH,
// GOOGLE_APPLICATION_CREDENTIALS, NODE_EXTRA_CA_CERTS. QWEN_HOME is the one that
// would cost you the login: it moves ~/.qwen, credentials included.
var qwenEnvAllow = []string{
	"OPENAI_API_KEY",
	"ANTHROPIC_API_KEY",
	"GEMINI_API_KEY",
	"GOOGLE_API_KEY",
	"DASHSCOPE_API_KEY",
	"OPENROUTER_API_KEY",
	"BAILIAN_CODING_PLAN_API_KEY",
	"OPENAI_BASE_URL",
	"ANTHROPIC_BASE_URL",
	"OPENAI_MODEL",
}

// qwenForcedEnv is set for every Qwen run.
//
// SANDBOX is the one that matters. Qwen inherits Gemini CLI's self-sandboxing,
// which re-execs the agent inside a container of its own via docker or podman.
// There is no docker socket in here and there will not be one, so that path can
// only fail — and it fails after startup, which is a confusing place to discover
// it. Qwen treats a set SANDBOX as "you are already sandboxed" and skips the
// whole mechanism, which is exactly true of this container.
//
// NO_BROWSER stops it trying to open a browser that cannot exist here.
var qwenForcedEnv = []string{"SANDBOX=1", "NO_BROWSER=1"}

func newQwenCmd() *cobra.Command {
	rf := &runFlags{}
	cmd := &cobra.Command{
		Use:   "qwen [sandbox-flags --] [qwen-args...]",
		Short: "Run Qwen Code inside the sandbox",
		Long: "Runs `qwen` inside the sandbox. Everything you pass is forwarded to qwen.\n" +
			"Sandbox options (leading --flags below, or before a `--` separator) are\n" +
			"consumed first.\n\n" +
			"Qwen Code is installed into the sandbox agent home the first time you run it.\n" +
			"It is not baked into the base image, so you only pay the download if you use\n" +
			"this agent.\n\n" +
			"Your Qwen settings are persisted by default in a sandbox-owned directory\n" +
			"(~/.config/sandbox/agents/qwen, separate from your host ~/.qwen). Use\n" +
			"--no-persist-auth for a throwaway session.\n\n" +
			"Qwen Code is a Gemini CLI fork and inherits its habit of re-running itself\n" +
			"inside a container it starts through docker. There is no docker socket in\n" +
			"here, so the sandbox tells it that it is already sandboxed and it skips that\n" +
			"entirely — which is simply the truth.\n\n" +
			"Note that Qwen's own OAuth free tier was discontinued, so authentication in\n" +
			"practice means an API key: forward one from your host environment (only if\n" +
			"set) or enter it with /auth. No host files are mounted unless you --mount.",
		Example: "  sandbox-cli qwen\n" +
			"  sandbox-cli qwen -p 'run the tests'\n" +
			"  sandbox-cli qwen --project ~/app -- -p 'fix the failing test'",
		// Forward unknown agent flags instead of rejecting them; sandbox flags are
		// parsed manually from the pre-`--` portion in runWrapper.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			agentCmd := npmAgentBootstrap("qwen", "@qwen-code/qwen-code")
			afterParse := func() error {
				// After parsing: pflag's string array replaces its initial contents on
				// the first --env, which would drop these for anyone passing one.
				rf.env = append(rf.env, qwenForcedEnv...)
				return nil
			}
			return runWrapper(cmd, rf, args, agentCmd, qwenEnvAllow, afterParse)
		},
	}
	// Persists Qwen's state in a sandbox-owned host dir (~/.config/sandbox/
	// agents/qwen) mounted as the container HOME. Opt out with --no-persist-auth.
	return finishAgentCmd(cmd, rf, "qwen")
}

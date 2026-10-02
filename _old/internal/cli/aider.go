package cli

import "github.com/spf13/cobra"

// aiderEnvAllow is the suggested (opt-in) set of host env vars forwarded to an
// Aider session, applied only if present in the host environment. Aider talks to
// providers through litellm, so the list is provider keys and their base URLs.
//
// Aider derives an AIDER_* variable from every one of its flags, and the ones
// naming files are deliberately absent — AIDER_CONFIG, AIDER_ENV_FILE,
// AIDER_MODEL_SETTINGS_FILE, AIDER_MODEL_METADATA_FILE, AIDER_AIDERIGNORE,
// AIDER_INPUT_HISTORY_FILE, AIDER_CHAT_HISTORY_FILE, AIDER_LLM_HISTORY_FILE,
// AIDER_ANALYTICS_LOG. Each points at a host path that is not mounted.
var aiderEnvAllow = []string{
	"OPENAI_API_KEY",
	"ANTHROPIC_API_KEY",
	"GEMINI_API_KEY",
	"DEEPSEEK_API_KEY",
	"OPENROUTER_API_KEY",
	"OPENAI_API_BASE",
	"ANTHROPIC_API_BASE",
}

// aiderInstall installs Aider with uv, which is a single static binary that puts
// both itself and the tools it installs under ~/.local — the persisted HOME —
// with no root and no system Python packages touched.
//
// --python pins the interpreter to the image's own python3. Without it uv is
// free to download a managed CPython, which is another ~87MB for an interpreter
// already sitting in the image; bookworm ships 3.11 and Aider wants >=3.10,<3.13.
//
// The `==` version comes from the pin table (internal/agents/pins.go). uv itself
// is still fetched unversioned from astral.sh: it is the installer, so pinning it
// would mean pinning the thing that decides what a pin means, and astral's script
// offers no version parameter to pass anyway. That half is recorded rather than
// solved.
var aiderInstall = `curl -LsSf https://astral.sh/uv/install.sh | sh && ` +
	`uv tool install --python "$(command -v python3)" aider-chat` + pinnedSpec("aider", "==")

func newAiderCmd() *cobra.Command {
	rf := &runFlags{}
	cmd := &cobra.Command{
		Use:   "aider [sandbox-flags --] [aider-args...]",
		Short: "Run Aider inside the sandbox",
		Long: "Runs `aider` inside the sandbox. Everything you pass is forwarded to aider.\n" +
			"Sandbox options (leading --flags below, or before a `--` separator) are\n" +
			"consumed first.\n\n" +
			"Aider is a Python tool, so the first run installs uv and then Aider itself\n" +
			"into the sandbox agent home. That is a large download and takes a while; it\n" +
			"happens once, and only if you use this agent.\n\n" +
			"Aider has no login at all — it authenticates with provider API keys, which\n" +
			"are forwarded from your host environment only if they are set. There is\n" +
			"nothing to persist beyond its own history, so --no-persist-auth costs you\n" +
			"little here.\n\n" +
			"Note that Aider writes into the project it is working on: it creates\n" +
			".aider.chat.history.md and a tags cache, and appends .aider* to the repo's\n" +
			".gitignore on first run. Pass --no-gitignore to aider if you would rather it\n" +
			"left your .gitignore alone. It also requires the workspace to be a git repo.",
		Example: "  sandbox-cli aider\n" +
			"  sandbox-cli aider --no-gitignore\n" +
			"  sandbox-cli aider --project ~/app -- --message 'run the tests'",
		// Forward unknown agent flags instead of rejecting them; sandbox flags are
		// parsed manually from the pre-`--` portion in runWrapper.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			agentCmd := agentBootstrap("aider", aiderInstall)
			return runWrapper(cmd, rf, args, agentCmd, aiderEnvAllow, nil)
		},
	}
	// Persists Aider's state in a sandbox-owned host dir (~/.config/sandbox/
	// agents/aider) mounted as the container HOME — mostly its installed copy and
	// chat history, since there is no login. Opt out with --no-persist-auth.
	return finishAgentCmd(cmd, rf, "aider")
}

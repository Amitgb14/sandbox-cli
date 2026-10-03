package cli

import "github.com/spf13/cobra"

// devinEnvAllow is the suggested (opt-in) set of host env vars forwarded to a
// Devin CLI session, applied only if present in the host environment.
//
// Deliberately short. Cognition documents `/login` inside the session as the
// auth route and names no environment variable for a key, so guessing one here
// would forward nothing and imply a route that may not exist. `DEVIN_API_KEY` is
// listed because Devin's HTTP API uses it and the CLI is the same product; if it
// turns out the CLI ignores it, the cost is a variable that crosses and is not
// read, which is the harmless direction.
//
// No path-valued variables, for the reason every other adapter here excludes
// them: each names a host directory that is not mounted, and one pointing at the
// credential store would move the login out of the persisted HOME and discard it
// on every run.
var devinEnvAllow = []string{
	"DEVIN_API_KEY",
	"DEVIN_API_BASE_URL",
}

// devinInstall runs the vendor installer, which is the only route Cognition
// documents for macOS and Linux outside its desktop bundle. It unpacks into the
// HOME the container gives it, which is the persisted one, so a first-run
// install survives the container.
//
// Unpinned, and recorded as such. The installer takes the latest promoted
// version and offers no switch; pinning would mean fetching a versioned
// `cli/<version>/setup.sh`, and no index of versions is published. That is a real
// difference from the npm adapters, and it is written down in
// internal/agents/pins.go as an `Unpinned` entry with the reason — *not* as an
// absent one, which fails TestEveryLazyInstalledAgentIsPinnedOrSaysWhy rather
// than meaning "the vendor decides".
const devinInstall = `curl -fsSL https://cli.devin.ai/install.sh | bash`

func newDevinCmd() *cobra.Command {
	rf := &runFlags{}
	cmd := &cobra.Command{
		Use:   "devin [sandbox-flags --] [devin-args...]",
		Short: "Run Devin CLI inside the sandbox",
		Long: "Runs Cognition's `devin` inside the sandbox. Everything you pass is\n" +
			"forwarded to it. Sandbox options (leading --flags below, or before a `--`\n" +
			"separator) are consumed first.\n\n" +
			"Devin CLI is installed into the sandbox agent home the first time you run\n" +
			"it, from Cognition's installer. It is not baked into the base image, so you\n" +
			"only pay for it if you use it.\n\n" +
			"Your Devin login is persisted by default in a sandbox-owned directory\n" +
			"(~/.config/sandbox/agents/devin, separate from any host install), so you log\n" +
			"in once. Use --no-persist-auth for a throwaway session.\n\n" +
			"Devin is a paid product: the CLI needs an account, and `/login` inside a\n" +
			"session is the documented route — which nobody has run here. If it completes\n" +
			"through a printed URL or a device code it works in the sandbox; if it needs a\n" +
			"loopback callback it does not, the same as the other browser-callback logins.\n\n" +
			"`devin -p PROMPT` is single-turn mode, and `--permission-mode bypass`\n" +
			"auto-approves tool calls. sandbox-cli does not add either for you: the\n" +
			"second is a decision about what an agent may do unattended, and typing it\n" +
			"is the point at which somebody makes it.",
		Example: "  sandbox-cli devin\n" +
			"  sandbox-cli devin -p 'explain this repository'\n" +
			"  sandbox-cli devin --project ~/app -- -p 'fix the failing test' --permission-mode bypass",
		// Forward unknown agent flags instead of rejecting them; sandbox flags are
		// parsed manually from the pre-`--` portion in runWrapper.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			agentCmd := agentBootstrap("devin", devinInstall)
			return runWrapper(cmd, rf, args, agentCmd, devinEnvAllow, nil)
		},
	}
	// Persists Devin's login in a sandbox-owned host dir (~/.config/sandbox/
	// agents/devin) mounted as the container HOME. Opt out with --no-persist-auth.
	return finishAgentCmd(cmd, rf, "devin")
}

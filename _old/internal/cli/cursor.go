package cli

import "github.com/spf13/cobra"

// cursorEnvAllow is the suggested (opt-in) set of host env vars forwarded to a
// Cursor CLI session, applied only if present in the host environment.
//
// Its path-valued variables are deliberately absent — CURSOR_DATA_DIR,
// CURSOR_AGENT_STORE, CURSOR_AGENT_STORE_FILES_DIR, CURSOR_STATE_OUTPUT_FILE,
// and the XDG_* pair Cursor also honours. CURSOR_DATA_DIR is the one that would
// bite: it moves the session and credential store somewhere the container has
// not got.
var cursorEnvAllow = []string{
	"CURSOR_API_KEY",
	"CURSOR_API_ENDPOINT",
}

// cursorNoBrowser stops the CLI trying to open a browser that cannot exist here.
// Without it the login still works — it prints the URL either way — but it first
// attempts a launch that can only fail, which reads like something went wrong.
const cursorNoBrowser = "NO_OPEN_BROWSER=1"

// cursorInstall runs the vendor installer, which unpacks into ~/.local/share and
// links ~/.local/bin — all inside the persisted HOME, so a first-run install
// survives the container without needing an install-prefix override (the
// installer has none to give).
const cursorInstall = `curl -fsS https://cursor.com/install | bash`

func newCursorCmd() *cobra.Command {
	rf := &runFlags{}
	cmd := &cobra.Command{
		Use:   "cursor [sandbox-flags --] [cursor-agent-args...]",
		Short: "Run Cursor CLI inside the sandbox",
		Long: "Runs Cursor's `cursor-agent` inside the sandbox. Everything you pass is\n" +
			"forwarded to it. Sandbox options (leading --flags below, or before a `--`\n" +
			"separator) are consumed first.\n\n" +
			"Cursor CLI is installed into the sandbox agent home the first time you run\n" +
			"it, from the official installer (around 219MB). It is not baked into the base\n" +
			"image, so you only pay for it if you use this agent, and it keeps itself up to\n" +
			"date in that persisted home afterwards.\n\n" +
			"Your Cursor login is persisted by default in a sandbox-owned directory\n" +
			"(~/.config/sandbox/agents/cursor, separate from your host ~/.cursor), so you\n" +
			"log in once. Use --no-persist-auth for a throwaway session.\n\n" +
			"`cursor-agent login` prints a URL you open on your host and then polls for the\n" +
			"result, with nothing listening on localhost, so it works from in here. The\n" +
			"sandbox also sets NO_OPEN_BROWSER so it does not attempt a launch that can\n" +
			"only fail. CURSOR_API_KEY from your host environment skips login entirely.\n\n" +
			"Cursor ships its own sandboxing and enables it by default. It should degrade\n" +
			"gracefully inside a container, but if it complains, `--sandbox disabled` hands\n" +
			"isolation back to this container, which is already providing it.",
		Example: "  sandbox-cli cursor\n" +
			"  sandbox-cli cursor -p 'run the tests'\n" +
			"  sandbox-cli cursor --project ~/app -- --sandbox disabled",
		// Forward unknown agent flags instead of rejecting them; sandbox flags are
		// parsed manually from the pre-`--` portion in runWrapper.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			// cursor-agent rather than the newer `agent` alias, which the installer
			// also links: both are the same executable, and `agent` is a name generic
			// enough to collide with something else on PATH.
			agentCmd := agentBootstrap("cursor-agent", cursorInstall)
			afterParse := func() error {
				// After parsing, not before: pflag's string array replaces its initial
				// contents on the first --env, which would drop this for anyone
				// passing an env var of their own.
				rf.env = append(rf.env, cursorNoBrowser)
				return nil
			}
			return runWrapper(cmd, rf, args, agentCmd, cursorEnvAllow, afterParse)
		},
	}
	// Persists Cursor's login in a sandbox-owned host dir (~/.config/sandbox/
	// agents/cursor) mounted as the container HOME. Opt out with --no-persist-auth.
	return finishAgentCmd(cmd, rf, "cursor")
}

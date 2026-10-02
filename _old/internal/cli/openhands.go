package cli

import "github.com/spf13/cobra"

// openhandsEnvAllow is the suggested (opt-in) set of host env vars forwarded to
// an OpenHands session, applied only if present in the host environment.
//
// LLM_API_KEY, LLM_MODEL and LLM_BASE_URL are forwarded but only take effect
// when you also pass --override-with-envs; that is OpenHands' own rule, not
// something the sandbox imposes, and the help says so rather than leaving you to
// wonder why an exported key appeared to be ignored.
//
// The path-valued variables are deliberately absent — OPENHANDS_PERSISTENCE_DIR,
// OPENHANDS_CONVERSATIONS_DIR, OPENHANDS_WORK_DIR, CACHE_DIR, FILE_STORE_PATH,
// SAVE_TRAJECTORY_PATH, GOOGLE_APPLICATION_CREDENTIALS — along with
// SANDBOX_VOLUMES, which only means anything to the docker runtime this adapter
// deliberately does not use.
var openhandsEnvAllow = []string{
	"LLM_API_KEY",
	"LLM_MODEL",
	"LLM_BASE_URL",
	"ANTHROPIC_API_KEY",
	"OPENAI_API_KEY",
	"OPENHANDS_CLOUD_URL",
}

// openhandsInstall fetches the standalone CLI binary, which needs no Python at
// all — the PyPI package pins 3.12 and the image ships 3.11, so the binary is
// the only route that works here without dragging an interpreter in.
//
// The vendor install script is not used: it pins a version well behind the
// current release. This fetches the release asset directly, at the version the
// pin table records (internal/agents/pins.go).
//
// It used to ask the releases API for the latest tag and fall back to a hardcoded
// one. That is now the pin: asking for "latest" meant the version installed was
// whichever one existed at the moment of the run, which is exactly what the pin
// table exists to stop. It also removes a second network dependency —
// api.github.com had to be reachable for the *usual* path to work, and under
// `--allow` it often is not, so the fallback was doing more of the work than it
// looked like.
//
// The failure mode changed shape and is worth knowing: there is no degradation
// path any more. If this release asset is ever yanked or 404s, the download fails
// and the run ends with the bootstrap's generic "installing it just now failed"
// and exit 127, where previously it would have resolved a different tag and
// carried on. That is the price of a pin — a deterministic install that can go
// wrong deterministically — and the fix is to bump the pin, not to restore the
// lookup.
var openhandsInstall = `mkdir -p "$HOME/.local/bin"
case "$(uname -m)" in
  x86_64) oh_arch=x86_64 ;;
  aarch64|arm64) oh_arch=arm64 ;;
  *) echo "sandbox-cli: unsupported architecture for openhands" >&2; exit 1 ;;
esac
oh_ver=` + pinnedSpec("openhands", "") + `
curl -fsSL "https://github.com/OpenHands/OpenHands-CLI/releases/download/$oh_ver/openhands-linux-$oh_arch" \
  -o "$HOME/.local/bin/openhands" && chmod +x "$HOME/.local/bin/openhands"`

func newOpenhandsCmd() *cobra.Command {
	rf := &runFlags{}
	cmd := &cobra.Command{
		Use:   "openhands [sandbox-flags --] [openhands-args...]",
		Short: "Run OpenHands CLI inside the sandbox",
		Long: "Runs `openhands` inside the sandbox. Everything you pass is forwarded to it.\n" +
			"Sandbox options (leading --flags below, or before a `--` separator) are\n" +
			"consumed first.\n\n" +
			"The standalone binary is installed into the sandbox agent home the first time\n" +
			"you run it. OpenHands' Python package requires 3.12 and the image ships 3.11,\n" +
			"so the binary is the route that works here without pulling in an interpreter.\n\n" +
			"OpenHands is best known for starting its own runtime container per session,\n" +
			"which cannot work here — there is no docker socket, and mounting one would\n" +
			"hand the container control of your host's daemon. The CLI does not need it:\n" +
			"its terminal interface runs the agent in the local workspace, which is what\n" +
			"this wrapper uses. The `openhands serve` web GUI is the part that wants\n" +
			"docker, and it is not what this command runs.\n\n" +
			"Your OpenHands login is persisted by default in a sandbox-owned directory\n" +
			"(~/.config/sandbox/agents/openhands, separate from your host ~/.openhands), so\n" +
			"you log in once. `openhands login` is a device-code flow, so no browser is\n" +
			"needed in here. Use --no-persist-auth for a throwaway session.\n\n" +
			"Two things are degraded in this environment and both are cosmetic rather than\n" +
			"fatal: its terminal tool prefers tmux and falls back to plain subprocesses\n" +
			"without it, and its browsing tool needs a browser the image does not carry.\n\n" +
			"LLM_API_KEY, LLM_MODEL and LLM_BASE_URL are forwarded if set, but OpenHands\n" +
			"only reads them when you also pass --override-with-envs.",
		Example: "  sandbox-cli openhands\n" +
			"  sandbox-cli openhands --override-with-envs\n" +
			"  sandbox-cli openhands --project ~/app",
		// Forward unknown agent flags instead of rejecting them; sandbox flags are
		// parsed manually from the pre-`--` portion in runWrapper.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			agentCmd := agentBootstrap("openhands", openhandsInstall)
			return runWrapper(cmd, rf, args, agentCmd, openhandsEnvAllow, nil)
		},
	}
	// Persists OpenHands' login in a sandbox-owned host dir (~/.config/sandbox/
	// agents/openhands) mounted as the container HOME. Opt out with
	// --no-persist-auth.
	return finishAgentCmd(cmd, rf, "openhands")
}

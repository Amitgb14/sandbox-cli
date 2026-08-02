package cli

import "github.com/spf13/cobra"

// gooseEnvAllow is the suggested (opt-in) set of host env vars forwarded to a
// Goose session, applied only if present in the host environment.
//
// Goose's path-valued variables are deliberately absent — GOOSE_PATH_ROOT,
// GOOSE_RECIPE_PATH, GOOSE_SEARCH_PATHS, GOOSE_TLS_CERT_PATH, GOOSE_TLS_KEY_PATH.
// GOOSE_PATH_ROOT is the one that would really hurt: it relocates every Goose
// data directory at once, so a forwarded host value would move config and
// secrets off the persisted HOME and silently discard the login each run.
var gooseEnvAllow = []string{
	"ANTHROPIC_API_KEY",
	"OPENAI_API_KEY",
	"GOOGLE_API_KEY",
	"GROQ_API_KEY",
	"OPENROUTER_API_KEY",
	"GOOSE_PROVIDER",
	"GOOSE_MODEL",
	"GOOSE_FAST_MODEL",
	"GOOSE_MODE",
}

// gooseDisableKeyring is set for every Goose run, and without it the wrapper's
// central promise — log in once — is simply false.
//
// Goose stores secrets in the OS keyring by default, reached over DBus. A
// container has no Secret Service, so the store fails with "org.freedesktop.
// secrets was not provided by any .service files". Goose does try to fall back
// to file storage when it detects a headless environment, but relying on that
// detection means the login silently depends on a heuristic; setting the
// documented switch makes it a property of the run. Secrets then live in
// ~/.config/goose/secrets.yaml, inside the persisted HOME, where they survive
// the container like every other agent's credentials.
const gooseDisableKeyring = "GOOSE_DISABLE_KEYRING=1"

// gooseInstall fetches the official installer and runs it against the persisted
// HOME. CONFIGURE=false is required, not cosmetic: the installer otherwise ends
// by running `goose configure` against /dev/tty, which in a first-run install
// would hang the sandbox on a prompt nobody asked for.
//
// The env assignments belong to bash rather than to curl — `VAR=x curl … | bash`
// would set them for the wrong process, and the installer would not see them.
//
// GOOSE_VERSION is the installer's own documented way to ask for a release rather
// than whatever `stable` points at today; the number comes from the pin table
// (internal/agents/pins.go). The script is still fetched from the `stable` tag,
// because that is the only URL the vendor publishes it at — so the pin governs
// which binary is installed, not which installer runs.
var gooseInstall = `curl -fsSL https://github.com/aaif-goose/goose/releases/download/stable/download_cli.sh` +
	` | CONFIGURE=false GOOSE_BIN_DIR="$HOME/.local/bin"` + pinnedSpec("goose", " GOOSE_VERSION=v") + ` bash`

func newGooseCmd() *cobra.Command {
	rf := &runFlags{}
	cmd := &cobra.Command{
		Use:   "goose [sandbox-flags --] [goose-args...]",
		Short: "Run Goose inside the sandbox",
		Long: "Runs `goose` inside the sandbox. Everything you pass is forwarded to goose.\n" +
			"Sandbox options (leading --flags below, or before a `--` separator) are\n" +
			"consumed first.\n\n" +
			"Goose is installed into the sandbox agent home the first time you run it,\n" +
			"from the official installer. It is not baked into the base image, so you only\n" +
			"pay the download if you use it.\n\n" +
			"Your Goose login is persisted by default in a sandbox-owned directory\n" +
			"(~/.config/sandbox/agents/goose, separate from your host ~/.config/goose), so\n" +
			"you log in once. Use --no-persist-auth for a throwaway session.\n\n" +
			"Goose keeps secrets in the OS keyring by default, and a container has no\n" +
			"keyring daemon, so the sandbox sets GOOSE_DISABLE_KEYRING for every run.\n" +
			"Secrets go to ~/.config/goose/secrets.yaml inside the persisted home instead,\n" +
			"which is what makes logging in once actually hold.\n\n" +
			"There is no browser flow to worry about: configure a provider with the\n" +
			"interactive `goose configure`, or forward a provider key from your host\n" +
			"environment. Keys are forwarded only if they are set. No other host files\n" +
			"are mounted unless you pass --mount.",
		Example: "  sandbox-cli goose\n" +
			"  sandbox-cli goose session\n" +
			"  sandbox-cli goose --project ~/app -- run -t 'run the tests'",
		// Forward unknown agent flags instead of rejecting them; sandbox flags are
		// parsed manually from the pre-`--` portion in runWrapper.
		DisableFlagParsing: true,
		RunE: func(cmd *cobra.Command, args []string) error {
			agentCmd := agentBootstrap("goose", gooseInstall)
			afterParse := func() error {
				// Appended after the flags are parsed, not before: pflag's string
				// array replaces its initial value on the first --env, so a value
				// set up front would vanish for anyone who passes one.
				rf.env = append(rf.env, gooseDisableKeyring)
				return nil
			}
			return runWrapper(cmd, rf, args, agentCmd, gooseEnvAllow, afterParse)
		},
	}
	// Persists Goose's login in a sandbox-owned host dir (~/.config/sandbox/
	// agents/goose) mounted as the container HOME. Opt out with --no-persist-auth.
	return finishAgentCmd(cmd, rf, "goose")
}

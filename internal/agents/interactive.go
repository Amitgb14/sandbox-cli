package agents

import "sort"

// The interactive-only agents: started in a terminal, with no verified headless
// mode, so they are wrappers (`sandbox-cli aider`) and never fleet or routing
// targets — an unattended agent that stops to ask does not fail, it hangs.
//
// Ported verbatim from beta.15's per-agent wrappers (_old/internal/cli/<agent>.go):
// each env allowlist and install line keeps the comment that says why it is the
// way it is. They install into the agent's HOME on first use, so the base image
// carries none of them.

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

// ampEnvAllow is the suggested (opt-in) set of host env vars forwarded to an Amp
// session, applied only if present in the host environment.
//
// Amp's path-valued variables are deliberately absent — AMP_SETTINGS_FILE,
// AMP_HOME, AMP_RIPGREP_PATH, AMP_LOG_FILE, AMP_PLUGIN_RUNTIME_LOG_FILE. AMP_HOME
// relocates everything Amp stores, the access token included.
var ampEnvAllow = []string{
	"AMP_API_KEY",
	"AMP_URL",
	"AMP_LOG_LEVEL",
	"AMP_SKIP_UPDATE_CHECK",
}

// clineEnvAllow is the suggested (opt-in) set of host env vars forwarded to a
// Cline session, applied only if present in the host environment. Cline drives
// several providers, so the list spans them rather than naming one vendor.
//
// Cline's several path-valued variables are deliberately absent — CLINE_DATA_DIR,
// CLINE_SANDBOX_DATA_DIR, CLINE_TEAM_DATA_DIR, CLINE_TOOL_APPROVAL_DIR,
// CLINE_LOG_PATH, NODE_EXTRA_CA_CERTS. Each names a host directory that is not
// mounted; forwarding one would point Cline's state at a path that does not
// exist in the container, and CLINE_DATA_DIR in particular would move the login
// out of the persisted HOME, quietly costing you the session on every run.
var clineEnvAllow = []string{
	"ANTHROPIC_API_KEY",
	"CLINE_API_KEY",
	"OPENAI_API_KEY",
	"OPENROUTER_API_KEY",
	"AI_GATEWAY_API_KEY",
	"V0_API_KEY",
}

// continueEnvAllow is the suggested (opt-in) set of host env vars forwarded to a
// Continue CLI session, applied only if present in the host environment.
//
// CONTINUE_API_KEY is deliberately absent despite being the obvious candidate
// and still documented upstream: it appears nowhere in the shipped CLI, so
// forwarding it would do nothing while implying it did something. ANTHROPIC_API_KEY
// is what the CLI actually reads.
//
// The path-valued CONTINUE_GLOBAL_DIR, GOOGLE_APPLICATION_CREDENTIALS and
// AWS_LOGIN_CACHE_DIRECTORY are excluded for the usual reason.
var continueEnvAllow = []string{
	"ANTHROPIC_API_KEY",
	"CONTINUE_API_BASE",
	"AWS_ACCESS_KEY_ID",
	"AWS_SECRET_ACCESS_KEY",
	"AWS_SESSION_TOKEN",
	"AWS_REGION",
	"GOOGLE_CLOUD_PROJECT",
}

// copilotEnvAllow is the suggested (opt-in) set of host env vars forwarded to a
// GitHub Copilot CLI session, applied only if present in the host environment.
//
// A forwarded GitHub token deserves more thought than the other agents' keys: a
// provider API key buys the container inference, while a GitHub PAT can reach
// every repository you can. It is forwarded only when set, like everything else
// here, but the command's help says plainly what that hands over.
//
// COPILOT_HOME and the other path-valued variables are deliberately absent —
// COPILOT_HOME relocates the whole config directory, so a forwarded host value
// would point Copilot's auth at a path the container cannot see.
var copilotEnvAllow = []string{
	"COPILOT_GITHUB_TOKEN",
	"GH_TOKEN",
	"GITHUB_TOKEN",
	"GH_HOST",
	"COPILOT_MODEL",
	"COPILOT_API_URL",
}

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

// pinnedSpec renders a pinned version into an install line, sep first, or
// nothing when the agent has no pin.
func pinnedSpec(bin, sep string) string {
	if p, ok := PinFor(bin); ok && p.Version != "" {
		return sep + p.Version
	}
	return ""
}

var interactive = map[string]Descriptor{
	"aider":     {Name: "aider", PersistDir: "aider", EnvAllow: aiderEnvAllow, Command: Bootstrap("aider", aiderInstall)},
	"amp":       {Name: "amp", PersistDir: "amp", EnvAllow: ampEnvAllow, Command: NpmBootstrap("amp", "@ampcode/cli")},
	"cline":     {Name: "cline", PersistDir: "cline", EnvAllow: clineEnvAllow, Command: NpmBootstrap("cline", "cline")},
	"continue":  {Name: "continue", PersistDir: "continue", EnvAllow: continueEnvAllow, Command: NpmBootstrap("cn", "@continuedev/cli")},
	"copilot":   {Name: "copilot", PersistDir: "copilot", EnvAllow: copilotEnvAllow, Command: NpmBootstrap("copilot", "@github/copilot")},
	"crush":     {Name: "crush", PersistDir: "crush", EnvAllow: crushEnvAllow, Command: NpmBootstrap("crush", "@charmland/crush")},
	"cursor":    {Name: "cursor", PersistDir: "cursor", EnvAllow: cursorEnvAllow, Command: Bootstrap("cursor-agent", cursorInstall), Env: []string{cursorNoBrowser}},
	"goose":     {Name: "goose", PersistDir: "goose", EnvAllow: gooseEnvAllow, Command: Bootstrap("goose", gooseInstall), Env: []string{gooseDisableKeyring}},
	"openhands": {Name: "openhands", PersistDir: "openhands", EnvAllow: openhandsEnvAllow, Command: Bootstrap("openhands", openhandsInstall)},
	"qwen":      {Name: "qwen", PersistDir: "qwen", EnvAllow: qwenEnvAllow, Command: NpmBootstrap("qwen", "@qwen-code/qwen-code"), Env: qwenForcedEnv},
}

// LookupInteractive returns an agent that can be started in a terminal: every
// headless-capable agent, and the interactive-only ones above.
func LookupInteractive(name string) (Descriptor, bool) {
	if d, ok := registry[name]; ok {
		return d, true
	}
	d, ok := interactive[name]
	return d, ok
}

// InteractiveNames lists every agent a wrapper exists for, sorted.
func InteractiveNames() []string {
	out := Names()
	for n := range interactive {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

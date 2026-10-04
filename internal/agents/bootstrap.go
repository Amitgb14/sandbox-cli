package agents

import (
	"crypto/sha256"
	"encoding/hex"
)

// The container-side install scripts, here rather than in internal/cli because
// a headless run (a routed fallback, a Studio launch) needs the same argv the
// interactive wrapper uses. They started in the wrappers, which was fine while
// only claude and codex could be launched headlessly; the moment another agent
// could be, a second copy of this script would have had to exist to build its
// Command — and the package doc above says what two copies of a
// security-relevant script do.
//
// internal/cli keeps one-line aliases (agentBootstrap / npmAgentBootstrap) so
// the twelve wrappers read as they did.

// Bootstrap builds the container argv for an agent that the base image may not
// carry. It prefers whatever is already on PATH — the baked copy, for the agents
// the image does carry — and otherwise runs install once, into the persisted
// HOME (~/.local, already ahead on PATH), where it stays for every later run of
// that agent.
//
// Installing lazily instead of baking is what keeps the base image from growing
// with each adapter added. Baking every agent would put hundreds of megabytes in
// front of every user on first build, most of it for agents they will never run;
// this way an unused adapter costs nothing but the few lines of Go, and the
// agents you do use are downloaded once into a directory that outlives the
// container.
//
// The two costs are real and are why the install is announced rather than
// silent: the first run of an agent waits for a download, and it needs network
// at that moment — under `--allow` the npm registry is in the baseline, but a
// vendor's own download host may not be.
//
// The announcement also names the version, when one is pinned (see pins.go).
// A pin's cost is staleness, and staleness that is only visible as "the agent
// behaves like an old one" is the kind that survives for months; printing the
// number turns it into something the user can read and report.
//
// The trailing bin is sh's argv[0] for the script, so "$@" is exactly the guest
// args appended by the caller.
func Bootstrap(bin, install string) []string {
	installs[bin] = Tools{Bin: bin, Install: install}
	// $HOME/.local/bin is APPENDED, not prepended, and the agent is exec'd from it
	// by absolute path.
	//
	// That directory is the persisted agent HOME: bind-mounted from the host, the
	// same one in every project, and writable by the agent. Prepending it put it
	// ahead of /usr/bin for every future session in every project — so an agent
	// compromised in one repository could drop a file named `git`, `node` or `sh`
	// there and shadow that command everywhere afterwards. Appending keeps the
	// directory usable while system binaries win, and the absolute exec below is
	// what still lets the agent self-update, which is the reason it lives there.
	//
	// A tools volume (see Tools) is the install a previous run's installer
	// sandbox made, read-only here. It is used only where the image does not
	// carry the agent — the image's copy is the one its builder chose — and
	// only once its ready marker is there, which is written last, so a volume
	// whose install was cut short is passed over.
	script := `export PATH="$PATH:$HOME/.local/bin"
if [ -x "$HOME/.local/bin/` + bin + `" ]; then
  exec "$HOME/.local/bin/` + bin + `" "$@"
fi
if ! command -v ` + bin + ` >/dev/null 2>&1; then
  if [ -f ` + ToolsDir + `/.sandbox-ready ] && [ -x ` + ToolsDir + `/.local/bin/` + bin + ` ]; then
    export PATH="$PATH:` + ToolsDir + `/.local/bin"
    exec ` + ToolsDir + `/.local/bin/` + bin + ` "$@"
  fi
  echo "sandbox-cli: installing ` + bin + installedVersionSuffix(bin) + ` into this sandbox (it is not in the image and not cached on this endpoint, so this run installs it)..." >&2
  ` + install + ` >/dev/null 2>&1 || true
fi
if ! command -v ` + bin + ` >/dev/null 2>&1; then
  echo "sandbox-cli: ` + bin + ` is not installed, and installing it just now failed." >&2
  echo "sandbox-cli: an agent that is not in the image needs network access to install." >&2
  echo "sandbox-cli: with --allow, the install host must be on the allowlist." >&2
  exit 127
fi
exec ` + bin + ` "$@"`
	return []string{"sh", "-c", script, bin}
}

// installedVersionSuffix renders " <version>" for the install announcement, or
// nothing for an agent the table deliberately leaves unpinned. It is a suffix
// rather than a separate echo so an unpinned agent's line reads exactly as it did
// before pins existed.
func installedVersionSuffix(bin string) string {
	if p, ok := installPins[bin]; ok && p.Version != "" {
		return " " + p.Version
	}
	return ""
}

// NpmBootstrap is Bootstrap for an agent distributed as an npm package, pinned to
// the version recorded in pins.go. --prefix keeps the install inside the
// persisted HOME, which is the only writable place that survives the container.
func NpmBootstrap(bin, pkg string) []string {
	return Bootstrap(bin, `npm install -g --prefix "$HOME/.local" `+npmSpec(bin, pkg))
}

// ToolsDir is where an agent's tools volume is mounted, read-only, in a run.
const ToolsDir = "/sandbox/tools"

// installs records each bootstrapped agent's install, by binary, as the
// descriptors are built.
var installs = map[string]Tools{}

// Tools is how an agent that is not in the image is installed once into a
// volume, so its runs stop installing it every time.
//
// The volume is executed by every later run of the agent, in every repository,
// so who writes it is the whole question. Not the agent: an agent compromised
// in one repository that could rewrite its own binary would carry itself into
// all the others. The volume is written only by an installer sandbox that runs
// this script and nothing else — no workspace, no secrets, no agent — and runs
// mount it read-only, which their drive enforces below the guest kernel. What
// it trusts is what installing on every run trusted: the pinned package and its
// registry.
type Tools struct {
	Bin     string
	Install string // as Bootstrap runs it, against $HOME
}

// Tools reports how d installs into a tools volume; false for an agent the
// image carries.
func (d Descriptor) Tools() (Tools, bool) {
	if len(d.Command) != 4 || d.Command[0] != "sh" || d.Command[1] != "-c" {
		return Tools{}, false
	}
	t, ok := installs[d.Command[3]]
	return t, ok
}

// Volume names the tools volume for this exact install. The name changes with
// the script, which carries the pinned version, so a new pin installs into a
// new volume instead of mixing with the old one.
func (t Tools) Volume(agent string) string {
	h := sha256.Sum256([]byte(t.Bin + "\x00" + t.Install))
	return "agent-" + agent + "-" + hex.EncodeToString(h[:4])
}

// InstallScript is what the installer sandbox runs, with the volume mounted
// writable at ToolsDir: the install with HOME pointed at the volume, so every
// path an installer writes is the path a run reads, then the check that the
// binary is there, then the ready marker, last.
//
// An agent the image already carries is not installed: the volume is marked
// ready and left empty, and runs keep using the image's copy (the bootstrap
// looks on PATH first). The image asked is the endpoint's default; a run on
// another image that lacks the agent finds the volume empty and installs it
// itself, as before.
func (t Tools) InstallScript() string {
	return `set -e
if command -v ` + t.Bin + ` >/dev/null 2>&1; then
  touch ` + ToolsDir + `/.sandbox-ready
  exit 0
fi
export HOME=` + ToolsDir + `
export npm_config_cache=/tmp/sandbox-npm-cache
cd "$HOME"
` + t.Install + `
test -x "$HOME/.local/bin/` + t.Bin + `"
touch "$HOME/.sandbox-ready"`
}

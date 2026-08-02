package agents

// Where the first-run version of every lazily-installed agent is decided, in one
// table.
//
// Eleven of the fifteen wrappers install their agent on first run, from a vendor
// host, into the persisted agent HOME. Until this table existed each of those
// installs resolved *whatever the vendor served that day*: `npm install -g <pkg>`
// with no version, a `curl | bash` whose script is regenerated per release, a
// GitHub API call for "latest". The bytes that ended up executing inside the
// sandbox were chosen at the moment of the run, and two users' first runs were
// not necessarily the same install.
//
// **What pinning buys.** A hijacked or typosquatted *publish* — the common real
// supply-chain attack — does not reach a sandbox until someone here bumps a line
// in this table. First install is the only one sandbox-cli controls, so it is the
// only place this can be said.
//
// **What it does not buy**, and must not be described as buying: anything against
// a compromised *registry*, which can serve different bytes for a version it has
// already published. That is the shape of the July 2026 OpenAI incident — the
// escape came through a package-registry proxy that was itself part of the
// isolated environment. Closing that needs integrity hashes, which the npm CLI
// checks only from a lockfile and a `-g` install has none of. Pinning is the half
// that is cheap.
//
// **The cost is staleness**, and it is real: an agent that does not update itself
// stays on the version recorded here until someone bumps it. That is why
// Bootstrap announces the version it is installing rather than installing
// silently — a stale pin should be readable on screen, not inferred from an agent
// behaving like an old one.
//
// To bump an agent: change the version here, and nowhere else. Two copies of an
// install string is the drift the package doc in bootstrap.go was written about,
// and a version is part of the install string.

// InstallPin records how one lazily-installed agent's first-run version is
// decided.
//
// Exactly one of the two fields is set, and that is the point of the type:
// "pinned to X" and "deliberately not pinned, because Y" are both answers, and a
// third state — nobody decided — is what this exists to make impossible.
// TestEveryLazyInstalledAgentIsPinnedOrSaysWhy is where that stops being a
// convention.
type InstallPin struct {
	// Version is the exact version installed on first run.
	Version string
	// Unpinned is why this agent has no version pin. Set only when Version is
	// empty, and it must be a reason rather than a note — "the installer takes no
	// version", not "TODO".
	Unpinned string
}

// installPins is keyed by the *binary* name the bootstrap execs, which is not
// always the wrapper's name: `cursor` installs `cursor-agent` and `continue`
// installs `cn`. The binary is what Bootstrap is handed, so it is what the lookup
// has to use.
var installPins = map[string]InstallPin{
	// npm-distributed. Versions are the published `latest` as of 2026-08-02.
	"gemini":   {Version: "0.53.1"},  // @google/gemini-cli
	"opencode": {Version: "1.18.11"}, // opencode-ai
	"droid":    {Version: "0.186.0"}, // droid
	"cline":    {Version: "3.0.49"},  // cline
	"crush":    {Version: "0.88.0"},  // @charmland/crush
	"copilot":  {Version: "1.0.77"},  // @github/copilot
	"cn":       {Version: "1.5.47"},  // @continuedev/cli
	"qwen":     {Version: "0.21.3"},  // @qwen-code/qwen-code
	// Amp publishes a continuously-generated version rather than semver, so this
	// line goes stale faster than the others by design rather than by neglect.
	"amp": {Version: "0.0.1785646934-g35813b"}, // @ampcode/cli

	// Installed by their own routes; the version reaches each installer
	// differently, which is why the install strings live with their wrappers and
	// only the number lives here.
	"aider":     {Version: "0.86.2"}, // uv tool install aider-chat==<v>
	"goose":     {Version: "1.45.0"}, // GOOSE_VERSION=v<v> on the vendor script
	"openhands": {Version: "1.16.0"}, // release asset URL

	"cursor-agent": {Unpinned: "cursor.com/install regenerates the script per release with the " +
		"version baked into it, and documents no way to ask for a different one; there is " +
		"nothing to pass"},
	"claude": {Unpinned: "the installer does take a version, but Claude Code updates itself into " +
		"the persisted HOME — which is the reason that HOME exists — so a pin would govern only " +
		"a first install the agent replaces on its own, and the base image usually satisfies that " +
		"install anyway"},
}

// PinFor returns the recorded pin for a binary. The second result is false when
// the binary is not in the table at all, which is the state the test refuses to
// let a new adapter leave behind.
func PinFor(bin string) (InstallPin, bool) {
	p, ok := installPins[bin]
	return p, ok
}

// PinnedBins returns every binary in the table, for tests and for documentation
// that would otherwise re-list them and drift.
func PinnedBins() []string {
	out := make([]string, 0, len(installPins))
	for b := range installPins {
		out = append(out, b)
	}
	return out
}

// npmSpec renders the package argument for an npm install, pinned when the table
// says so.
//
// A binary with no entry falls back to the unpinned spec rather than refusing.
// That is deliberate and is the one place here that fails open: a maintainer who
// adds an adapter and forgets the pin should get a failing test, not a user whose
// agent will not start. The enforcement belongs in the test, where it costs the
// maintainer a minute, rather than at runtime, where it costs a user their run.
func npmSpec(bin, pkg string) string {
	if p, ok := installPins[bin]; ok && p.Version != "" {
		return pkg + "@" + p.Version
	}
	return pkg
}

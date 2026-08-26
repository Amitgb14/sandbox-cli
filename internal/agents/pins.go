package agents

// Where the first-run version of every lazily-installed agent is decided, in one
// table.
//
// Eight of the twelve wrappers install their agent on first run, from a vendor
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
// **What it does not buy**, and must not be described as buying:
//
//   - Anything against a compromised *registry*, which can serve different bytes
//     for a version it has already published. That is the shape of the July 2026
//     OpenAI incident — the escape came through a package-registry proxy that was
//     itself part of the isolated environment.
//
//   - **Anything below the top of the tree.** This pins the named package only.
//     `npm install -g <pkg>@<v>` still resolves every transitive dependency's
//     range fresh at install time, with no lockfile — and the attacks that
//     actually happen are mostly down there rather than at the top
//     (`event-stream`, `ua-parser-js`, the 2025 chalk/debug compromise). Some of
//     the agents below ship a bundled single-file build and are genuinely
//     unaffected; others are not, and this table does not distinguish them, so
//     assume the weaker case.
//
// Both need integrity hashes, which the npm CLI verifies only from a lockfile and
// a `-g` install has none of. The half a global install *can* do is record
// `npm view <pkg>@<v> dist.integrity` alongside the version here and check it
// after download; that is the upgrade path if this is ever worth strengthening.
// Pinning the top of the tree is the cheap part, and it is all this is.
//
// **Bumping.** Twelve hand-maintained versions with no process is its own failure
// mode — a pin nobody bumps becomes an old agent nobody can explain, which is the
// staleness named below arriving by neglect rather than by design. The intent is
// a sweep at each release, with amp as the canary since it goes stale fastest,
// and that intent is currently the whole mechanism.
//
// The mechanism it wants is small and does not exist yet: a CI step diffing each
// entry against `npm view <pkg> version` (and the three release URLs) and failing,
// or opening an issue, when they drift. That turns twelve numbers somebody has to
// remember into a thing that reports on itself, which is the difference between a
// documented intent and a process.
//
// **The cost is staleness**, and how much it costs depends on whether the agent
// replaces its own binary afterwards — see SelfUpdates, which is recorded only
// where it has been verified. An agent that does not update itself stays on the
// version recorded here until someone bumps it. That is why
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
// Exactly one of Version and Unpinned is set, and that is the point of the type:
// "pinned to X" and "deliberately not pinned, because Y" are both answers, and a
// third state — nobody decided — is what this exists to make impossible.
// TestEveryLazyInstalledAgentIsPinnedOrSaysWhy is where that stops being a
// convention. SelfUpdates is independent of both and may be empty.
type InstallPin struct {
	// Version is the exact version installed on first run.
	Version string
	// Unpinned is why this agent has no version pin. Set only when Version is
	// empty, and it must be a reason rather than a note — "the installer takes no
	// version", not "TODO".
	Unpinned string

	// SelfUpdates records whether the agent replaces its own binary in the
	// persisted HOME after the first install, because that is what decides how
	// much a stale pin actually costs: an agent that updates itself is pinned only
	// for its first minute, and one that does not stays on this exact version
	// until somebody bumps the line.
	//
	// "" means **nobody has checked**, not "no". Only the two below carry evidence,
	// and guessing the rest from vendor habits would be the sort of confident wrong
	// value TestEveryAgentHasAVerifiedHeadlessArgv exists to keep out of the
	// neighbouring table. Verifying one means running it twice across a release,
	// which is why this is mostly empty rather than mostly filled.
	SelfUpdates string
}

// Self-update states for InstallPin.SelfUpdates.
const (
	selfUpdateYes = "yes" // verified to replace its own binary
	selfUpdateNo  = "no"  // verified not to
)

// installPins is keyed by the *binary* name the bootstrap execs, which is not
// always the wrapper's name: `cursor` installs `cursor-agent` and `continue`
// installs `cn`. The binary is what Bootstrap is handed, so it is what the lookup
// has to use.
var installPins = map[string]InstallPin{
	// npm-distributed. Versions are the published `latest` as of 2026-08-02.
	"gemini": {Version: "0.53.1"}, // @google/gemini-cli
	// opencode ships OPENCODE_DISABLE_AUTOUPDATE (see its EnvAllow), which is only
	// a knob worth having if the thing updates itself.
	"opencode": {Version: "1.18.11", SelfUpdates: selfUpdateYes}, // opencode-ai
	"cline":    {Version: "3.0.49"},                              // cline
	"kilocode": {Version: "7.4.23"},                              // @kilocode/cli
	"copilot":  {Version: "1.0.77"},                              // @github/copilot
	"qwen":     {Version: "0.21.3"},                              // @qwen-code/qwen-code
	// Amp publishes a continuously-generated version rather than semver, so this
	// line goes stale faster than the others by design rather than by neglect.

	// Installed by their own routes; the version reaches each installer
	// differently, which is why the install strings live with their wrappers and
	// only the number lives here.
	"goose":     {Version: "1.45.0"}, // GOOSE_VERSION=v<v> on the vendor script
	"openhands": {Version: "1.16.0"}, // release asset URL

	// Devin's top-level installer takes no version: it reads PINNED_VERSION,
	// which its own comment says the *versioned* setup scripts set
	// (`cli/<version>/setup.sh`) and the top-level one leaves empty to get "the
	// latest promoted version". So pinning means knowing a version and fetching a
	// different URL, and Cognition publishes no index of them that this could
	// read. Recorded rather than guessed: naming a version from an example in a
	// comment would pin every sandbox to whatever that example happened to be.
	"devin": {Unpinned: "cli.devin.ai/install.sh installs the latest promoted version and offers " +
		"no version switch; pinning means fetching cli/<version>/setup.sh, and no index of " +
		"versions is published"},
	"cursor-agent": {Unpinned: "cursor.com/install regenerates the script per release with the " +
		"version baked into it, and documents no way to ask for a different one; there is " +
		"nothing to pass"},
	"claude": {SelfUpdates: selfUpdateYes, Unpinned: "the installer does take a version, but Claude Code updates itself into " +
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

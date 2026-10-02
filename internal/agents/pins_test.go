package agents

import (
	"strings"
	"testing"
)

// TestEveryPinIsEitherAVersionOrAReason is the invariant the InstallPin type
// exists for: "pinned to X" and "deliberately not pinned, because Y" are both
// answers, and the third state — an entry nobody finished — is the one that would
// quietly reintroduce `npm install -g <pkg>` with no version.
func TestEveryPinIsEitherAVersionOrAReason(t *testing.T) {
	for _, bin := range PinnedBins() {
		p, _ := PinFor(bin)
		switch {
		case p.Version != "" && p.Unpinned != "":
			t.Errorf("%s: has both a version (%q) and a reason not to be pinned (%q); it can only be one",
				bin, p.Version, p.Unpinned)
		case p.Version == "" && p.Unpinned == "":
			t.Errorf("%s: has neither a version nor a reason.\n"+
				"Record the version this agent should install on first run, or say why it has none.\n"+
				"An empty entry is how an unpinned install gets back in.", bin)
		case p.Unpinned != "" && len(p.Unpinned) < 20:
			// A reason has to be a reason. "TODO" and "n/a" are how this decays.
			t.Errorf("%s: %q is not a reason", bin, p.Unpinned)
		}
	}
}

// TestNpmBootstrapPinsTheVersion checks the thing the pin is actually for: the
// install command the container runs names a version, so the first run installs
// what someone here chose rather than what the registry served that day.
func TestNpmBootstrapPinsTheVersion(t *testing.T) {
	argv := NpmBootstrap("gemini", "@google/gemini-cli")
	if len(argv) != 4 {
		t.Fatalf("bootstrap argv = %#v, want 4 elements", argv)
	}
	p, ok := PinFor("gemini")
	if !ok || p.Version == "" {
		t.Fatal("gemini has no pinned version; this test is checking nothing")
	}
	want := "@google/gemini-cli@" + p.Version
	if !strings.Contains(argv[2], want) {
		t.Errorf("install command does not name the pinned package %q:\n%s", want, argv[2])
	}
	// The version is announced, not installed silently — a pin's cost is
	// staleness, and staleness nobody can see is the kind that lasts.
	if !strings.Contains(argv[2], "installing gemini "+p.Version) {
		t.Errorf("install is not announced with its version:\n%s", argv[2])
	}
}

// TestUnpinnedAgentsReadExactlyAsBefore guards the fallback in npmSpec and
// installedVersionSuffix: an agent the table deliberately leaves unpinned must
// produce the install string and the announcement it produced before pins
// existed, rather than a dangling "@" or a trailing space.
func TestUnpinnedAgentsReadExactlyAsBefore(t *testing.T) {
	argv := NpmBootstrap("not-in-the-table", "some-pkg")
	if !strings.Contains(argv[2], `--prefix "$HOME/.local" some-pkg`) {
		t.Errorf("unpinned install string is not the bare package:\n%s", argv[2])
	}
	if strings.Contains(argv[2], "some-pkg@") {
		t.Errorf("unpinned install string has a dangling version separator:\n%s", argv[2])
	}
	if !strings.Contains(argv[2], "installing not-in-the-table into") {
		t.Errorf("unpinned announcement is malformed:\n%s", argv[2])
	}
}

// TestRegistryAgentsThatInstallLazilyArePinned covers the descriptors this
// package owns. codex is deliberately absent from the pin table: it is baked into
// the base image and its Command is the bare binary, so there is no install to
// pin.
func TestRegistryAgentsThatInstallLazilyArePinned(t *testing.T) {
	// agent -> the binary its bootstrap installs, or "" when it does not install.
	installs := map[string]string{
		"cline":    "cline",
		"claude":   "claude",
		"codex":    "",
		"gemini":   "gemini",
		"opencode": "opencode",
	}
	for _, name := range Names() {
		bin, ok := installs[name]
		if !ok {
			t.Errorf("agent %q is in the registry but not in this table.\n"+
				"Say which binary it installs on first run, or \"\" if the image carries it,\n"+
				"and give it a line in installPins either way.", name)
			continue
		}
		if bin == "" {
			continue
		}
		if _, ok := PinFor(bin); !ok {
			t.Errorf("%s installs %q on first run with no entry in installPins", name, bin)
		}
	}
}

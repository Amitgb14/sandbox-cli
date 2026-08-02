package cli

import (
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
)

// wrapperInstalls records, for every agent wrapper, the binary it installs on
// first run — or "" when the base image carries it and there is no install to
// pin.
//
// It is a hand-maintained table for the same reason
// TestEveryAgentHasAVerifiedHeadlessArgv's is: the binary a wrapper installs is
// not derivable from its cobra command (two of them disagree with the wrapper's
// own name — `cursor` installs `cursor-agent`, `continue` installs `cn`), and the
// point of the test is that adding an adapter means adding a line here rather
// than quietly getting an unpinned install.
//
// **What that does and does not guarantee.** It catches the case it was written
// for — a wrapper added by copying another file and never listed here. It does
// not verify that the name in this table is the binary the wrapper actually
// installs: get that wrong and the test happily checks a pin for a binary nobody
// bootstraps while the real one goes unpinned. Closing that means reaching into
// each wrapper's rendered argv, which is a bigger test than the risk justifies
// while these fifteen entries are right; recorded so the guarantee is not read as
// stronger than it is.
var wrapperInstalls = map[string]string{
	"claude":    "claude",
	"codex":     "", // baked into the base image; Command is the bare binary
	"gemini":    "gemini",
	"opencode":  "opencode",
	"cline":     "cline",
	"goose":     "goose",
	"crush":     "crush",
	"aider":     "aider",
	"copilot":   "copilot",
	"cursor":    "cursor-agent",
	"qwen":      "qwen",
	"amp":       "amp",
	"continue":  "cn",
	"openhands": "openhands",
	"droid":     "droid",
}

// TestEveryLazyInstalledAgentIsPinnedOrSaysWhy is where the pin table stops being
// a convention.
//
// Eleven of the fifteen wrappers download their agent from a vendor host on first
// run, into a HOME that persists across every project. A new adapter added by
// copying an existing file is exactly how one of those goes back to resolving
// whatever the registry serves that day — so a wrapper with no recorded pin fails
// here rather than shipping.
func TestEveryLazyInstalledAgentIsPinnedOrSaysWhy(t *testing.T) {
	seen := map[string]bool{}
	for _, cmd := range agentCmds() {
		name := strings.Fields(cmd.Use)[0]
		seen[name] = true
		t.Run(name, func(t *testing.T) {
			bin, ok := wrapperInstalls[name]
			if !ok {
				t.Fatalf("wrapper %q is not in wrapperInstalls.\n"+
					"Say which binary it installs on first run, or \"\" if the base image carries it.\n"+
					"Without a line here the adapter can install an unpinned version and nothing notices.", name)
			}
			if bin == "" {
				return
			}
			p, ok := agents.PinFor(bin)
			if !ok {
				t.Fatalf("%s installs %q on first run, and internal/agents/pins.go has no entry for it.\n"+
					"Record the version to install, or say why it cannot be pinned.", name, bin)
			}
			if p.Version == "" && p.Unpinned == "" {
				t.Errorf("%s: pin entry for %q is empty", name, bin)
			}
		})
	}
	for name := range wrapperInstalls {
		if !seen[name] {
			t.Errorf("wrapperInstalls lists %q, which is not a wrapper any more", name)
		}
	}
}

// TestSelfRoutedInstallersCarryTheirPin checks the three agents whose install
// strings are assembled by hand rather than by agents.NpmBootstrap. Each spells
// its version differently, so each is a separate chance to drop it — and dropping
// it in openhands' case would leave `oh_ver=` empty and build a release URL with
// a hole in it, which is a broken install rather than an unpinned one.
func TestSelfRoutedInstallersCarryTheirPin(t *testing.T) {
	cases := []struct {
		bin     string
		install string
		want    func(v string) string
	}{
		{"aider", aiderInstall, func(v string) string { return "aider-chat==" + v }},
		{"goose", gooseInstall, func(v string) string { return "GOOSE_VERSION=v" + v }},
		{"openhands", openhandsInstall, func(v string) string { return "oh_ver=" + v }},
	}
	for _, c := range cases {
		t.Run(c.bin, func(t *testing.T) {
			p, ok := agents.PinFor(c.bin)
			if !ok || p.Version == "" {
				t.Fatalf("%s has no pinned version; this installer would render without one", c.bin)
			}
			if want := c.want(p.Version); !strings.Contains(c.install, want) {
				t.Errorf("install string does not carry %q:\n%s", want, c.install)
			}
		})
	}
}

// TestOpenhandsNoLongerAsksForLatest pins the behaviour change the pin table
// caused: the version used to come from a releases-API call at run time, which is
// the same "whatever exists right now" the pins exist to remove. It also removed
// a dependency on api.github.com being reachable, which under `--allow` it often
// is not.
func TestOpenhandsNoLongerAsksForLatest(t *testing.T) {
	if strings.Contains(openhandsInstall, "api.github.com") {
		t.Errorf("openhands install resolves its version at run time again:\n%s", openhandsInstall)
	}
}

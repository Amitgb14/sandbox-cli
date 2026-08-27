package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
)

// TestStudioAgentSeedFollowsAgentCmds pins an invariant that a comment could not
// hold: Studio's offline seed lists the adapters "in `cli.agentCmds()` order,
// newest-supported last", and it drifted the first *and* second time an adapter
// was added after that comment was written.
//
// The seed is what Studio's fixture mode renders, so a wrong order there shows a
// different agent list from the one the daemon answers with — and a missing entry
// hides an agent live mode offers. Reading TypeScript from a Go test is not
// elegant; it is the only place both facts exist.
func TestStudioAgentSeedFollowsAgentCmds(t *testing.T) {
	path := filepath.Join("..", "..", "studio", "src", "lib", "constants.ts")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("studio sources not present: %v", err)
	}
	seed := regexp.MustCompile(`(?m)^    name: "(\w+)",$`).FindAllStringSubmatch(string(raw), -1)

	var want []string
	for _, cmd := range agentCmds() {
		want = append(want, strings.Fields(cmd.Use)[0])
	}
	// Equality, not "at least as many". The first version of this compared
	// `len(seed) < len(want)` and then only the indexes agentCmds() has, which
	// catches an adapter *added* and never seeded — but a seed entry left behind
	// for a removed agent sat past the end of the loop and passed silently. That
	// is the direction a removal takes, and Studio would have gone on offering an
	// agent the daemon no longer has.
	if len(seed) != len(want) {
		if gaps := missing(want, flatten(seed)); gaps != "" {
			t.Fatalf("the seed lists %d agents, agentCmds() has %d: %s is missing from %s",
				len(seed), len(want), gaps, path)
		}
		t.Fatalf("the seed lists %d agents, agentCmds() has %d: %s is seeded but has no wrapper — "+
			"a removed adapter left behind in %s is one Studio still offers",
			len(seed), len(want), missing(flatten(seed), want), path)
	}
	for i, w := range want {
		if got := seed[i][1]; got != w {
			t.Errorf("seed[%d] = %q, agentCmds()[%d] = %q — the seed must follow that order,\n"+
				"newest-supported last, or Studio's offline mode disagrees with the daemon", i, got, i, w)
		}
	}
}

func flatten(m [][]string) []string {
	out := make([]string, 0, len(m))
	for _, g := range m {
		out = append(out, g[1])
	}
	return out
}

// missing returns the names in want that have does not contain. Called both ways
// round by the caller above — a wrapper with no seed entry, and a seed entry with
// no wrapper are the same question asked from opposite ends.
func missing(want, have []string) string {
	seen := map[string]bool{}
	for _, h := range have {
		seen[h] = true
	}
	var gaps []string
	for _, w := range want {
		if !seen[w] {
			gaps = append(gaps, w)
		}
	}
	return strings.Join(gaps, ", ")
}

// wrapperEnvAllow is every adapter's forwarded-variable list, by wrapper name.
//
// The five with a descriptor read theirs from internal/agents; the other seven
// keep a package-level var, since a fleet cannot name them and only the wrapper
// needs the list. Written out rather than discovered so that a new adapter fails
// the test below until somebody classifies it — the same bargain wrapperInstalls
// makes one file over.
func wrapperEnvAllow(t *testing.T) map[string][]string {
	t.Helper()
	out := map[string][]string{
		"cursor":    cursorEnvAllow,
		"copilot":   copilotEnvAllow,
		"devin":     devinEnvAllow,
		"goose":     gooseEnvAllow,
		"kilocode":  kilocodeEnvAllow,
		"openhands": openhandsEnvAllow,
		"qwen":      qwenEnvAllow,
	}
	for _, n := range agents.Names() {
		d, ok := agents.Lookup(n)
		if !ok {
			continue
		}
		out[n] = d.EnvAllow
	}
	for _, cmd := range agentCmds() {
		name := strings.Fields(cmd.Use)[0]
		if _, ok := out[name]; !ok {
			t.Fatalf("wrapper %q has no entry in wrapperEnvAllow.\n"+
				"Add it (the descriptor's EnvAllow, or the wrapper's own var) so the seed check covers it", name)
		}
	}
	return out
}

// TestStudioAgentSeedEnvAllowMatchesTheCode pins the *contents* of the seed, not
// just its order.
//
// The seed's envAllow is what Studio's /agents screen shows offline, on a page
// whose whole subject is what crosses the boundary. It had drifted in both
// directions at once: eleven of the twelve listed a truncated set, so offline
// mode understated an agent's reach, and claude's listed ANTHROPIC_MODEL, which
// no wrapper and no descriptor has ever forwarded. Overstating is the worse half
// — somebody sets the variable on the host, the container never sees it, and the
// screen that told them it would is the one place they would check.
//
// Sorted, because handleAgents sorts before answering: offline and live should
// render the same list in the same order, or switching between them looks like a
// change in what is forwarded.
func TestStudioAgentSeedEnvAllowMatchesTheCode(t *testing.T) {
	path := filepath.Join("..", "..", "studio", "src", "lib", "constants.ts")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("studio sources not present: %v", err)
	}
	entry := regexp.MustCompile(`(?s)\{\s*name: "(\w+)",(.*?)\n  \},`)
	list := regexp.MustCompile(`(?s)envAllow: \[(.*?)\]`)
	varName := regexp.MustCompile(`"([A-Z_0-9]+)"`)
	delivery := regexp.MustCompile(`delivery: "(\w+)"`)

	code := wrapperEnvAllow(t)
	seen := map[string]bool{}
	for _, m := range entry.FindAllStringSubmatch(string(raw), -1) {
		name, body := m[1], m[2]
		want, ok := code[name]
		if !ok {
			continue // not an agent seed; the file holds other tables
		}
		seen[name] = true
		// delivery, for the five the daemon serves. handleAgents answers "baked"
		// for the four in the image and "npm" for the rest, so a seed that says
		// anything else makes offline mode contradict live mode in a field about
		// where the binary came from. The other seven keep a richer vocabulary
		// ("installer", "pip") on purpose — the daemon never answers for them, so
		// there is nothing for those words to disagree with.
		if _, isDescriptor := agents.Lookup(name); isDescriptor {
			want := "npm"
			if bakedInImage[name] {
				want = "baked"
			}
			if dm := delivery.FindStringSubmatch(body); dm != nil && dm[1] != want {
				t.Errorf("%s: the seed says delivery %q, GET /v1/agents answers %q — "+
					"offline and live would disagree about where the binary came from", name, dm[1], want)
			}
		}

		lm := list.FindStringSubmatch(body)
		if lm == nil {
			t.Errorf("%s: the seed has no envAllow", name)
			continue
		}
		got := varName.FindAllStringSubmatch(lm[1], -1)
		var have []string
		for _, g := range got {
			have = append(have, g[1])
		}
		sorted := append([]string(nil), want...)
		slices.Sort(sorted)
		if !slices.Equal(have, sorted) {
			for _, h := range have {
				if !slices.Contains(sorted, h) {
					t.Errorf("%s: the seed says %s is forwarded and no descriptor or wrapper forwards it — "+
						"offline mode is claiming a variable crosses the boundary when it does not", name, h)
				}
			}
			t.Errorf("%s: seed envAllow = %v,\n    want %v (sorted, as handleAgents answers)\n    in %s",
				name, have, sorted, path)
		}
	}
	for name := range code {
		if !seen[name] {
			t.Errorf("%s has an envAllow in Go but no seed entry in %s", name, path)
		}
	}
}

// bakedInImage mirrors studioapi.bakedAgents, which mirrors the image Dockerfile.
// Duplicated rather than exported because the two packages answer to different
// things — this one only needs to know what the daemon will say.
var bakedInImage = map[string]bool{
	"claude": true, "codex": true, "gemini": true, "opencode": true,
}

package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
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

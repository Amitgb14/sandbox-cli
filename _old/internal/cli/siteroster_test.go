package cli

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"testing"
)

// TestSiteRosterCountsMatchAgentCmds pins the two places the marketing site
// states how many agents there are as a *number in prose*, rather than by
// listing them.
//
// Both had drifted, and neither was caught by the sweep that updated a dozen
// other roster claims or by the review that followed it — for the same reason in
// both cases: the number is not next to the word "adapter", so it does not turn
// up when somebody greps for the count they are changing. The hero stat had said
// "15" since the landing page was rebuilt, through every roster change since; the
// page description said "Claude Code, Codex, Gemini and 12 more agents", which is
// fifteen, and it is the sentence search results show.
//
// A list that gets out of date is visibly wrong. A count that gets out of date
// reads as authoritative, which is why these two are worth a test and the prose
// elsewhere is not: these are the only site figures that are a pure function of
// agentCmds(), so they are the only ones a test can check without also deciding
// what the sentence around them ought to say.
func TestSiteRosterCountsMatchAgentCmds(t *testing.T) {
	want := len(agentCmds())

	t.Run("hero stat", func(t *testing.T) {
		src := readSite(t, filepath.Join("..", "..", "web", "src", "lib", "site.ts"))
		m := regexp.MustCompile(`value: "(\d+)", label: "agents wrapped"`).FindStringSubmatch(src)
		if m == nil {
			t.Skip(`no "agents wrapped" hero stat in site.ts; nothing to pin`)
		}
		if got, _ := strconv.Atoi(m[1]); got != want {
			t.Errorf("the hero says %d agents wrapped, agentCmds() has %d —\n"+
				"web/src/lib/site.ts, HERO_STATS", got, want)
		}
	})

	// "Run Claude Code, Codex, Gemini and N more agents …" — three are named, so
	// the figure is the rest of the roster and moves with it.
	t.Run("page description", func(t *testing.T) {
		src := readSite(t, filepath.Join("..", "..", "web", "src", "app", "layout.tsx"))
		m := regexp.MustCompile(`Run Claude Code, Codex, Gemini and (\d+) more agents`).FindStringSubmatch(src)
		if m == nil {
			t.Skip("the description no longer counts agents; nothing to pin")
		}
		named := 3
		if got, _ := strconv.Atoi(m[1]); got != want-named {
			t.Errorf("the page description says %d more agents beside the %d it names (%d total), "+
				"agentCmds() has %d —\nweb/src/app/layout.tsx. This is the sentence search results show",
				got, named, got+named, want)
		}
	})
}

// readSite returns the file, or skips: the Go module is usable without the site
// checked out beside it, and a missing web/ is not a failing invariant.
func readSite(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("site sources not present: %v", err)
	}
	return string(raw)
}

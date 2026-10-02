package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agentctx"
)

// TestWrapperHandlesOnlyItsOwnSubcommands pins the one exception to the wrapper
// rule that everything past the sandbox flags belongs to the agent. Getting this
// wrong in either direction is bad: too eager and a word the user meant for the
// agent is swallowed; too shy and `sandbox-cli claude context list` tries to
// start a container.
func TestWrapperHandlesOnlyItsOwnSubcommands(t *testing.T) {
	cases := []struct {
		name     string
		agent    string
		guest    []string
		explicit bool
		want     bool
	}{
		{name: "agent-scoped context subcommand", agent: "claude", guest: []string{"context", "stores"}, want: true},
		{name: "bare context", agent: "claude", guest: []string{"context"}, want: true},
		{name: "explicit -- hands it to the agent", agent: "claude", guest: []string{"context", "stores"}, explicit: true},
		{name: "an agent prompt that merely mentions context", agent: "claude", guest: []string{"-p", "context"}},
		{name: "no guest args at all", agent: "claude", guest: nil},
		{name: "not an adapter (plain run)", agent: "", guest: []string{"context"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := wrapperHandles(c.agent, c.guest, c.explicit); got != c.want {
				t.Errorf("wrapperHandles(%q, %#v, explicit=%v) = %v, want %v", c.agent, c.guest, c.explicit, got, c.want)
			}
		})
	}
}

// TestEveryAgentWrapperAnswersContext keeps the alias uniform: it would be a
// nasty surprise for `sandbox-cli claude context` to work and
// `sandbox-cli codex context` to be forwarded to codex as arguments.
func TestEveryAgentWrapperAnswersContext(t *testing.T) {
	for _, cmd := range agentCmds() {
		agent := cmd.Annotations[agentAnnotation]
		if !wrapperHandles(agent, []string{"context"}, false) {
			t.Errorf("%s does not answer `context`", strings.Fields(cmd.Use)[0])
		}
	}
}

// TestEmptyListingExplainsItself is the reason there is no second command: when
// a listing comes up empty, the answer to "where did you even look?" has to be
// in that same output, not behind a command the user has to be told about.
func TestEmptyListingExplainsItself(t *testing.T) {
	// An agent with no store descriptor says so, and says the agent still runs.
	err := explainNoStore("cursor")
	if err == nil {
		t.Fatal("an agent with no known store must report why the listing is empty")
	}
	if !strings.Contains(err.Error(), "does not know where cursor") {
		t.Errorf("unhelpful message: %v", err)
	}

	// An agent whose store is known but absent names the directories it searched,
	// so a user whose sessions live elsewhere can see that and say so.
	err = explainNoStore("claude")
	if err == nil {
		t.Skip("this machine has claude sessions; the not-found path cannot be exercised here")
	}
	if !strings.Contains(err.Error(), "looked in") {
		t.Errorf("a not-found message must say where it looked: %v", err)
	}
}

// TestStoreLineDescribesAStaleRecord keeps the sticky registry visible where it
// matters: a count carried over from an earlier probe must not be presented as
// what is on disk right now.
func TestStoreLineDescribesAStaleRecord(t *testing.T) {
	fresh := agentctx.Finding{Agent: "claude", Sessions: 12, Dir: "/h/.claude/projects"}
	if got := storeLine(fresh); strings.Contains(got, "not visible") {
		t.Errorf("a current store must not be described as stale: %q", got)
	}
	stale := fresh
	stale.Stale = true
	stale.VerifiedAt = time.Now().Add(-2 * time.Hour)
	got := storeLine(stale)
	if !strings.Contains(got, "not visible right now") || !strings.Contains(got, "2h ago") {
		t.Errorf("a stale store must say so and when it was last seen: %q", got)
	}
}

func TestShortenHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory on this machine")
	}
	if got, want := shortenHome(filepath.Join(home, ".claude", "projects")), filepath.Join("~", ".claude", "projects"); got != want {
		t.Errorf("shortenHome = %q, want %q", got, want)
	}
	// A path outside the home is shown in full: which home a store lives in is
	// exactly the detail that matters when two of them exist.
	if got := shortenHome("/var/tmp/x"); got != "/var/tmp/x" {
		t.Errorf("shortenHome = %q, want the path unchanged", got)
	}
	if got := shortenHome(""); got != "-" {
		t.Errorf("shortenHome(\"\") = %q, want %q", got, "-")
	}
}

// TestExpandResumeIDLeavesArgsAloneUnlessItIsSure covers the timid half of the
// rewrite: sandbox-cli changes an argument the user typed, so every case where it
// is not certain must pass through untouched.
func TestExpandResumeIDLeavesArgsAloneUnlessItIsSure(t *testing.T) {
	cases := []struct {
		name  string
		agent string
		guest []string
	}{
		{name: "no resume argument at all", agent: "claude", guest: []string{"-p", "do the thing"}},
		{name: "resume with no value", agent: "claude", guest: []string{"--resume"}},
		{name: "value is another flag", agent: "claude", guest: []string{"--resume", "--verbose"}},
		{name: "agent with no known store", agent: "cursor", guest: []string{"--resume", "abc"}},
		{name: "not an adapter", agent: "", guest: []string{"--resume", "abc"}},
		{name: "id nothing matches", agent: "claude", guest: []string{"--resume", "zzzzzzzz"}},
		{
			name: "already a full uuid", agent: "claude",
			guest: []string{"--resume", "37888763-3d07-451a-920c-d458c987cda8"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := expandResumeID(c.agent, "/nowhere/in/particular", c.guest)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, c.guest) {
				t.Errorf("args were rewritten: %#v -> %#v", c.guest, got)
			}
		})
	}
}

// TestSessionIDCell pins both halves of the display choice: abbreviated is what
// you read and hand back to sandbox-cli, whole is what you need to run the agent
// directly, since prefix expansion is sandbox-cli's own.
func TestSessionIDCell(t *testing.T) {
	const id = "37888763-3d07-451a-920c-d458c987cda8"
	if got := sessionIDCell(id, false); got != "37888763" {
		t.Errorf("default = %q, want the abbreviated id", got)
	}
	if got := sessionIDCell(id, true); got != id {
		t.Errorf("--full = %q, want the whole id", got)
	}
}

func TestShortSessionID(t *testing.T) {
	// The first block of a UUID is what a user copies out of the listing. The
	// agents themselves reject it — expandResumeID is what makes it work.
	if got, want := shortSessionID("37888763-3d07-451a-920c-d458c987cda8"), "37888763"; got != want {
		t.Errorf("shortSessionID = %q, want %q", got, want)
	}
	// A store that names sessions some other way must still shorten safely.
	if got := shortSessionID("short"); got != "short" {
		t.Errorf("shortSessionID = %q, want it unchanged", got)
	}
	if got := shortSessionID("a-very-long-non-uuid-session-name"); len(got) != 12 {
		t.Errorf("shortSessionID = %q, want it capped at 12 characters", got)
	}
}

// TestUnknownAndEmptyCellsAreDistinguishable is the honest-partial rule at the
// display layer: a session whose format cannot be read yet must not look like a
// session that simply has no prompts in it.
func TestUnknownAndEmptyCellsAreDistinguishable(t *testing.T) {
	unread := agentctx.Session{Partial: true}
	empty := agentctx.Session{}

	if turnsCell(unread) != "?" {
		t.Errorf("an unread session's turn count = %q, want %q", turnsCell(unread), "?")
	}
	if turnsCell(empty) != "0" {
		t.Errorf("a read session with no prompts = %q, want %q", turnsCell(empty), "0")
	}
	if titleCell(unread) == titleCell(empty) {
		t.Errorf("unknown and empty titles must read differently, both are %q", titleCell(empty))
	}
}

func TestTitleCellTruncatesLongTitles(t *testing.T) {
	long := strings.Repeat("x", 200)
	got := titleCell(agentctx.Session{Title: long})
	if len([]rune(got)) > 64 {
		t.Errorf("title cell is %d runes, want it capped for the table", len([]rune(got)))
	}
	if !strings.HasSuffix(got, "…") {
		t.Errorf("a truncated title should show that it was cut: %q", got)
	}
}

func contains(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

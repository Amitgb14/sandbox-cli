package agentctx

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// claudeTranscript is a transcript in the shapes verified against a real Claude
// Code store: user prompts as plain strings, tool results coming back as *user*
// messages with a tool_result block, a prompt that carried an image as a text
// block array, an injected meta message, a sidechain from a subagent, and the
// generated title Claude writes for the session.
const claudeTranscript = `{"type":"mode","mode":"normal","sessionId":"3f2a1b4c-0000-4000-8000-000000000001"}
{"parentUuid":null,"type":"user","isMeta":true,"cwd":"/workspace","timestamp":"2026-07-25T10:00:00.000Z","sessionId":"3f2a1b4c-0000-4000-8000-000000000001","message":{"role":"user","content":"<local-command-caveat>Caveat: generated while running local commands</local-command-caveat>"}}
{"type":"user","cwd":"/workspace","timestamp":"2026-07-25T10:00:01.000Z","sessionId":"3f2a1b4c-0000-4000-8000-000000000001","message":{"role":"user","content":"add pagination to /orders"}}
{"type":"assistant","message":{"role":"assistant","content":[{"type":"tool_use","name":"Edit"}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}
{"type":"user","message":{"role":"user","content":[{"type":"tool_result","content":"ok"}]}}
{"type":"user","isSidechain":true,"message":{"role":"user","content":"subagent prompt"}}
{"type":"user","message":{"role":"user","content":[{"type":"image"},{"type":"text","text":"and match this mockup"}]}}
{"type":"last-prompt","lastPrompt":"and match this mockup","sessionId":"3f2a1b4c-0000-4000-8000-000000000001"}
{"type":"ai-title","aiTitle":"Add pagination to the orders endpoint","sessionId":"3f2a1b4c-0000-4000-8000-000000000001"}
`

func writeTranscript(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func claudeFinding(dir string) Finding {
	return Finding{Agent: "claude", Format: FormatClaudeJSONL, State: StateVerified, Dir: dir}
}

func TestReadClaudeSessionCountsPromptsNotToolResults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "-workspace", "3f2a1b4c-0000-4000-8000-000000000001.jsonl")
	writeTranscript(t, path, claudeTranscript)

	sessions, _, err := List(claudeFinding(dir), ListOpts{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("got %d sessions, want 1", len(sessions))
	}
	s := sessions[0]

	// Two real prompts: the plain one and the one with an image attached. The two
	// tool results, the injected meta message and the subagent's sidechain are
	// not things the user typed.
	if s.Turns != 2 {
		t.Errorf("turns = %d, want 2 (tool results, meta and sidechains must not count)", s.Turns)
	}
	if s.Title != "Add pagination to the orders endpoint" {
		t.Errorf("title = %q, want the generated ai-title", s.Title)
	}
	if s.ID != "3f2a1b4c-0000-4000-8000-000000000001" {
		t.Errorf("id = %q, want the session id from the transcript", s.ID)
	}
	if s.Project != "/workspace" {
		t.Errorf("project = %q, want /workspace", s.Project)
	}
	if want := time.Date(2026, 7, 25, 10, 0, 0, 0, time.UTC); !s.Started.Equal(want) {
		t.Errorf("started = %v, want %v", s.Started, want)
	}
	if s.Partial {
		t.Error("a transcript that was read is not partial")
	}
}

func TestReadClaudeSessionTitleFallsBackToTheFirstPrompt(t *testing.T) {
	// Short sessions never get a generated title, and are exactly the ones a user
	// struggles to tell apart in a listing.
	dir := t.TempDir()
	body := `{"type":"user","cwd":"/w","message":{"role":"user","content":"fix the flaky worktree test\nsecond line ignored"}}` + "\n"
	writeTranscript(t, filepath.Join(dir, "-w", "aaa.jsonl"), body)

	sessions, _, err := List(claudeFinding(dir), ListOpts{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := sessions[0].Title; got != "fix the flaky worktree test" {
		t.Errorf("title = %q, want the first prompt's first line", got)
	}
}

func TestReadClaudeSessionSurvivesAHalfWrittenTranscript(t *testing.T) {
	// The session running right now is always mid-write, and a listing that
	// choked on it would fail exactly when it is most wanted.
	dir := t.TempDir()
	body := `{"type":"user","message":{"role":"user","content":"first"}}` + "\n" +
		`{"type":"user","message":{"role":"user","conte` + "\n"
	writeTranscript(t, filepath.Join(dir, "-w", "bbb.jsonl"), body)

	sessions, _, err := List(claudeFinding(dir), ListOpts{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 || sessions[0].Turns != 1 || sessions[0].Title != "first" {
		t.Fatalf("a truncated transcript should still list what it has: %+v", sessions)
	}
}

func TestListScopesToTheProjectAndCanListEveryProject(t *testing.T) {
	dir := t.TempDir()
	writeTranscript(t, filepath.Join(dir, "-Users-x-app", "aaa.jsonl"), claudeTranscript)
	writeTranscript(t, filepath.Join(dir, "-Users-x-other", "bbb.jsonl"), claudeTranscript)

	got, scoped, err := List(claudeFinding(dir), ListOpts{Project: "/Users/x/app"})
	if err != nil {
		t.Fatal(err)
	}
	if !scoped {
		t.Error("a store with a derivable project directory must report the listing as scoped")
	}
	if len(got) != 1 {
		t.Fatalf("got %d sessions, want only this project's 1", len(got))
	}

	all, _, err := List(claudeFinding(dir), ListOpts{Project: "/Users/x/app", All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 2 {
		t.Errorf("--all listed %d sessions, want 2", len(all))
	}
}

func TestListOfAProjectNoAgentHasWorkedInIsEmptyNotAnError(t *testing.T) {
	dir := t.TempDir()
	writeTranscript(t, filepath.Join(dir, "-Users-x-app", "aaa.jsonl"), claudeTranscript)

	got, scoped, err := List(claudeFinding(dir), ListOpts{Project: "/Users/x/untouched"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 0 || !scoped {
		t.Errorf("got %d sessions (scoped=%v), want an empty scoped listing", len(got), scoped)
	}
}

func TestListSortsNewestFirst(t *testing.T) {
	dir := t.TempDir()
	older := filepath.Join(dir, "-w", "older.jsonl")
	newer := filepath.Join(dir, "-w", "newer.jsonl")
	writeTranscript(t, older, claudeTranscript)
	writeTranscript(t, newer, claudeTranscript)
	if err := os.Chtimes(older, now.Add(-48*time.Hour), now.Add(-48*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(newer, now, now); err != nil {
		t.Fatal(err)
	}

	got, _, err := List(claudeFinding(dir), ListOpts{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || !strings.HasSuffix(got[0].Path, "newer.jsonl") {
		t.Errorf("sessions are not newest-first: %+v", got)
	}
}

// TestListAnUnreadableFormatStillNamesTheSessions is the honest-partial rule: a
// store sandbox-cli can find but not parse still yields ids and dates, marked so
// the caller does not present a missing title as an empty one.
func TestListAnUnreadableFormatStillNamesTheSessions(t *testing.T) {
	dir := t.TempDir()
	writeTranscript(t, filepath.Join(dir, "2026", "07", "25", "rollout-2026-07-25T10-00-00-0c81f4de-0000-4000-8000-00000000000a.jsonl"), "{}\n")

	f := Finding{Agent: "codex", Format: FormatCodexRollout, State: StateVerified, Dir: dir}
	got, scoped, err := List(f, ListOpts{Project: "/Users/x/app"})
	if err != nil {
		t.Fatal(err)
	}
	if scoped {
		t.Error("a date-sharded store cannot be scoped to a project and must say so")
	}
	if len(got) != 1 {
		t.Fatalf("got %d sessions, want 1", len(got))
	}
	if !got[0].Partial {
		t.Error("a session listed without a reader must be marked partial")
	}
	// The id is still recoverable: every store seen so far names the file after it.
	if want := "0c81f4de-0000-4000-8000-00000000000a"; got[0].ID != want {
		t.Errorf("id = %q, want the uuid from the file name %q", got[0].ID, want)
	}
}

// TestASessionShapedFileBesideTheSessionsIsNotASession is a bug found by running
// the listing on a real machine: Gemini keeps a `logs.json` next to its `chats/`
// directory, and a bare *.json glob offered it as a conversation with an id of
// "logs". A file that merely looks like a session is worse than no listing —
// it hands the user an id to resume that was never a conversation.
func TestASessionShapedFileBesideTheSessionsIsNotASession(t *testing.T) {
	roots, home, _ := testRoots(t)
	hash := filepath.Join(home, ".gemini", "tmp", "a1b2c3")
	writeFile(t, filepath.Join(hash, "chats", "session-1.json"), now)
	writeFile(t, filepath.Join(hash, "logs.json"), now)

	store, _ := Lookup("gemini")
	f := store.Probe(roots, now)
	if f.Sessions != 1 {
		t.Errorf("probe counted %d sessions, want 1 (logs.json is not a session)", f.Sessions)
	}

	got, _, err := List(f, ListOpts{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("listed %d sessions, want 1: %+v", len(got), got)
	}
	if got[0].ID == "logs" {
		t.Error("logs.json was listed as a resumable session")
	}
}

// TestProbeAndListAgreeOnWhatASessionIs pins the shared traversal: a count the
// user is shown and a listing they are shown must never disagree.
func TestProbeAndListAgreeOnWhatASessionIs(t *testing.T) {
	roots, home, _ := testRoots(t)
	hash := filepath.Join(home, ".gemini", "tmp", "a1b2c3")
	writeFile(t, filepath.Join(hash, "chats", "one.json"), now)
	writeFile(t, filepath.Join(hash, "chats", "two.json"), now)
	writeFile(t, filepath.Join(hash, "logs.json"), now)
	writeFile(t, filepath.Join(hash, "shell_history"), now)

	store, _ := Lookup("gemini")
	f := store.Probe(roots, now)
	got, _, err := List(f, ListOpts{All: true})
	if err != nil {
		t.Fatal(err)
	}
	if f.Sessions != len(got) {
		t.Errorf("probe says %d sessions, listing shows %d", f.Sessions, len(got))
	}
}

func TestSessionIDFromFileName(t *testing.T) {
	for in, want := range map[string]string{
		"/s/3f2a1b4c-0000-4000-8000-000000000001.jsonl":                             "3f2a1b4c-0000-4000-8000-000000000001",
		"/s/rollout-2026-07-25T10-00-00-0c81f4de-0000-4000-8000-00000000000a.jsonl": "0c81f4de-0000-4000-8000-00000000000a",
		"/s/session-notes.json": "session-notes",
	} {
		if got := sessionID(in); got != want {
			t.Errorf("sessionID(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestExpandID is the fix for a real failure: `context list` prints abbreviated
// ids, and Claude Code refuses anything that is not a full UUID. sandbox-cli
// resolves the prefix so the listing is usable — but only when it is certain.
func TestExpandID(t *testing.T) {
	dir := t.TempDir()
	full := "3f2a1b4c-0000-4000-8000-000000000001"
	writeTranscript(t, filepath.Join(dir, "-w", full+".jsonl"), claudeTranscript)
	writeTranscript(t, filepath.Join(dir, "-w", "3f2a9999-0000-4000-8000-000000000002.jsonl"), claudeTranscript)
	f := claudeFinding(dir)

	all := ListOpts{All: true}
	if got, ok := ExpandID(f, all, "3f2a1b4c"); !ok || got.ID != full {
		t.Errorf("ExpandID(unambiguous prefix) = %q,%v, want %q,true", got.ID, ok, full)
	}
	// Two sessions share this prefix: guessing one would resume the wrong
	// conversation, which is worse than the agent's own error.
	if _, ok := ExpandID(f, all, "3f2a"); ok {
		t.Error("an ambiguous prefix must be left alone")
	}
	if _, ok := ExpandID(f, all, "nope"); ok {
		t.Error("an unknown value must be left alone — it may not be an id at all")
	}
	if _, ok := ExpandID(f, all, full); ok {
		t.Error("a full id needs no expansion")
	}
}

// TestExpandIDIsScopedToTheProject is the fix for a resume that failed with the
// right id: a sandbox mounts only the current project's history, so a session in
// another project's directory is unreachable however correct the id is. The
// expansion has to see the same scope the container will.
func TestExpandIDIsScopedToTheProject(t *testing.T) {
	dir := t.TempDir()
	here := "3f2a1b4c-0000-4000-8000-000000000001"
	elsewhere := "9c910000-0000-4000-8000-000000000002"
	writeTranscript(t, filepath.Join(dir, ProjectBucket("/Users/x/app"), here+".jsonl"), claudeTranscript)
	writeTranscript(t, filepath.Join(dir, ProjectBucket("/Users/x/other"), elsewhere+".jsonl"), claudeTranscript)
	f := claudeFinding(dir)

	inThisProject := ListOpts{Project: "/Users/x/app"}
	if got, ok := ExpandID(f, inThisProject, "3f2a1b4c"); !ok || got.ID != here {
		t.Errorf("a session in this project must expand: %q,%v", got.ID, ok)
	}
	if _, ok := ExpandID(f, inThisProject, "9c910000"); ok {
		t.Error("a session in another project must not expand — the sandbox cannot open it")
	}
	// It is still findable, which is what lets the caller say where it lives
	// instead of leaving the user with a bare "no session found".
	got, ok := ExpandID(f, ListOpts{All: true}, "9c910000")
	if !ok {
		t.Fatal("the session should still be locatable across the whole store")
	}
	if filepath.Base(got.Dir()) != ProjectBucket("/Users/x/other") {
		t.Errorf("Dir() = %q, want the other project's directory", got.Dir())
	}
}

// TestSessionRefsReadNoTranscripts pins the cheap path: expansion happens on
// every agent run, so it must not open dozens of multi-megabyte files.
func TestSessionRefsReadNoTranscripts(t *testing.T) {
	dir := t.TempDir()
	// Unparseable content: the ids must come from the file names either way.
	writeTranscript(t, filepath.Join(dir, "-w", "aaa11111-0000-4000-8000-00000000000b.jsonl"), "not json at all")

	got := SessionRefs(claudeFinding(dir), ListOpts{All: true})
	if len(got) != 1 || got[0].ID != "aaa11111-0000-4000-8000-00000000000b" {
		t.Errorf("SessionRefs = %+v, want the id from the file name", got)
	}
}

func TestFindResolvesAnUnambiguousPrefix(t *testing.T) {
	sessions := []Session{{ID: "3f2a1b4c-1111"}, {ID: "3f2a9999-2222"}, {ID: "abcd0000-3333"}}

	if got, amb := Find(sessions, "abcd"); got.ID != "abcd0000-3333" || amb != nil {
		t.Errorf("unambiguous prefix did not resolve: %+v %+v", got, amb)
	}
	if got, amb := Find(sessions, "3f2a"); len(amb) != 2 || got.ID != "" {
		t.Errorf("ambiguous prefix must return the candidates, got %+v %+v", got, amb)
	}
	if got, amb := Find(sessions, "3f2a1b4c-1111"); got.ID != "3f2a1b4c-1111" || amb != nil {
		t.Errorf("an exact id must win outright: %+v %+v", got, amb)
	}
	if _, amb := Find(sessions, "zzz"); len(amb) != 0 {
		t.Error("a prefix matching nothing must return no candidates")
	}
}

// TestOneLineStripsTerminalControlSequences pins that a session title cannot act
// on the terminal that prints it. Titles come from transcripts the agent writes,
// and `context list` renders them directly — so an ESC left in one is a command,
// not text. Reachable: screen clear, window title via OSC, and OSC 52 clipboard
// writes on terminals that allow them.
func TestOneLineStripsTerminalControlSequences(t *testing.T) {
	cases := map[string]string{
		"\x1b[2J\x1b[H\x1b]0;OWNED\x07\x1b[31mBENIGN\x1b[0m": "[2J [H ]0;OWNED [31mBENIGN [0m",
		"plain title":                 "plain title",
		"tab\tseparated":              "tab separated",
		"first line\nsecond line":     "first line",
		"\x1b]52;c;cGF5bG9hZA==\x07x": "]52;c;cGF5bG9hZA== x",
		"unicode ✓ kept":              "unicode ✓ kept",
		"del\x7fchar":                 "del char",
	}
	for in, want := range cases {
		if got := oneLine(in); got != want {
			t.Errorf("oneLine(%q) = %q, want %q", in, got, want)
		}
	}
	// The property that actually matters, stated directly.
	for _, in := range []string{"\x1b[2J", "a\x1bb", "x\x9bc", "\x00\x07"} {
		got := oneLine(in)
		for _, r := range got {
			if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
				t.Errorf("oneLine(%q) = %q still carries a control character %q", in, got, r)
			}
		}
	}
}

// TestReadClaudeSessionRefusesNonRegularFiles pins that a listing cannot be used
// to read arbitrary host files. Transcripts live in the agent's persisted HOME,
// which is bind-mounted read-write into the container — so the agent can drop a
// symlink named <uuid>.jsonl there and, before this, `context list` opened and
// rendered whatever it pointed at.
func TestReadClaudeSessionRefusesNonRegularFiles(t *testing.T) {
	dir := t.TempDir()
	secret := filepath.Join(dir, "host-secret.txt")
	// Shaped like a transcript so it would genuinely render if it were read.
	line := `{"type":"user","message":{"role":"user","content":"TOP-SECRET-HOST-CONTENT"}}` + "\n"
	if err := os.WriteFile(secret, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "11111111-2222-3333-4444-555555555555.jsonl")
	if err := os.Symlink(secret, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	var s Session
	readClaudeSession(link, &s)
	if s.Title != "" || s.Turns != 0 {
		t.Errorf("a symlinked transcript was read: title=%q turns=%d", s.Title, s.Turns)
	}

	// The regression: a real transcript at the same path must still be read.
	if err := os.Remove(link); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(link, []byte(line), 0o600); err != nil {
		t.Fatal(err)
	}
	var real Session
	readClaudeSession(link, &real)
	if real.Title == "" {
		t.Error("a regular transcript must still be read")
	}
}

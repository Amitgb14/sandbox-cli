package agentctx

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// writeFile creates a file (and its parents) with a fixed mtime, so tests can
// order locations by recency without sleeping.
func writeFile(t *testing.T, path string, mod time.Time) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mod, mod); err != nil {
		t.Fatal(err)
	}
}

func testRoots(t *testing.T) (Roots, string, string) {
	t.Helper()
	base := t.TempDir()
	home := filepath.Join(base, "home")
	agents := filepath.Join(base, "agents")
	return Roots{
		Home:  home,
		Agent: func(a string) string { return filepath.Join(agents, a) },
	}, home, agents
}

var now = time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)

func TestProbeFindsClaudeSessionsAndIgnoresSidechains(t *testing.T) {
	roots, home, _ := testRoots(t)
	bucket := filepath.Join(home, ".claude", "projects", "-workspace")
	writeFile(t, filepath.Join(bucket, "aaa.jsonl"), now.Add(-2*time.Hour))
	writeFile(t, filepath.Join(bucket, "bbb.jsonl"), now.Add(-1*time.Hour))
	// A session's subagent transcripts sit one level deeper. They are sidechains
	// of a session, not sessions, and must not be counted as resumable.
	writeFile(t, filepath.Join(bucket, "aaa", "subagents", "agent-1.jsonl"), now)

	s, _ := Lookup("claude")
	f := s.Probe(roots, now)

	if f.State != StateVerified {
		t.Fatalf("state = %q, want %q", f.State, StateVerified)
	}
	if f.Sessions != 2 {
		t.Errorf("sessions = %d, want 2 (subagent transcripts must not count)", f.Sessions)
	}
	if want := filepath.Join(home, ".claude", "projects"); f.Dir != want {
		t.Errorf("dir = %q, want %q", f.Dir, want)
	}
	if f.Root != RootHome {
		t.Errorf("root = %q, want %q", f.Root, RootHome)
	}
	if f.MintIDFlag != "--session-id" {
		t.Errorf("mint flag = %q, want --session-id", f.MintIDFlag)
	}
	if !f.VerifiedAt.Equal(now) || !f.FirstSeen.Equal(now) {
		t.Errorf("verified/first-seen not stamped: %+v", f)
	}
}

func TestProbePrefersTheMostRecentlyUsedLocation(t *testing.T) {
	// The claude wrapper can leave sessions in two places: the host history it
	// mounts by default, and the persisted agent HOME used with --no-sync. The
	// bigger store is not necessarily the live one.
	roots, home, agents := testRoots(t)
	writeFile(t, filepath.Join(home, ".claude", "projects", "-workspace", "old1.jsonl"), now.Add(-72*time.Hour))
	writeFile(t, filepath.Join(home, ".claude", "projects", "-workspace", "old2.jsonl"), now.Add(-71*time.Hour))
	writeFile(t, filepath.Join(agents, "claude", ".claude", "projects", "-workspace", "new.jsonl"), now.Add(-time.Minute))

	s, _ := Lookup("claude")
	f := s.Probe(roots, now)

	if want := filepath.Join(agents, "claude", ".claude", "projects"); f.Dir != want {
		t.Errorf("dir = %q, want the recently used store %q", f.Dir, want)
	}
	if len(f.Locations) != 2 {
		t.Errorf("locations = %d, want both stores recorded", len(f.Locations))
	}
}

func TestProbeCodexDateShardedLayout(t *testing.T) {
	roots, _, agents := testRoots(t)
	dir := filepath.Join(agents, "codex", ".codex", "sessions", "2026", "07", "25")
	writeFile(t, filepath.Join(dir, "rollout-2026-07-25T10-00-00-abc.jsonl"), now)
	// A file that does not match the session pattern is not a session.
	writeFile(t, filepath.Join(dir, "notes.jsonl"), now)

	s, _ := Lookup("codex")
	f := s.Probe(roots, now)

	if f.State != StateVerified || f.Sessions != 1 {
		t.Fatalf("state=%q sessions=%d, want verified/1", f.State, f.Sessions)
	}
}

func TestProbeReportsEmptyAndMissingDistinctly(t *testing.T) {
	roots, _, agents := testRoots(t)
	// codex home exists (the agent has been run and logged in) but holds no
	// sessions; gemini has never been run at all.
	if err := os.MkdirAll(filepath.Join(agents, "codex", ".codex", "sessions"), 0o700); err != nil {
		t.Fatal(err)
	}

	codex, _ := Lookup("codex")
	if got := codex.Probe(roots, now).State; got != StateEmpty {
		t.Errorf("codex state = %q, want %q", got, StateEmpty)
	}
	gemini, _ := Lookup("gemini")
	g := gemini.Probe(roots, now)
	if g.State != StateMissing {
		t.Errorf("gemini state = %q, want %q", g.State, StateMissing)
	}
	if g.Dir != "" {
		t.Errorf("a missing store must not report a directory, got %q", g.Dir)
	}
}

func TestMergeKeepsAVerifiedStoreWhenALaterProbeCannotSeeIt(t *testing.T) {
	// This is the reason the registry exists: an agent home that is not mounted
	// right now must not erase a path that was confirmed before.
	old := Finding{
		Agent: "claude", State: StateVerified, Dir: "/h/.claude/projects", Root: RootHome,
		Sessions: 12, FirstSeen: now.Add(-48 * time.Hour), VerifiedAt: now.Add(-time.Hour), CheckedAt: now.Add(-time.Hour),
	}
	fresh := Finding{Agent: "claude", State: StateMissing, CheckedAt: now, Resume: []string{"--resume"}}

	got := Merge(old, fresh)

	if got.Dir != old.Dir || got.Sessions != 12 {
		t.Fatalf("verified store was lost: %+v", got)
	}
	if !got.Stale {
		t.Error("a store kept from an earlier probe must be marked stale")
	}
	if !got.CheckedAt.Equal(now) {
		t.Error("checked-at must advance even when the probe found nothing")
	}
	if !got.VerifiedAt.Equal(old.VerifiedAt) {
		t.Error("verified-at must not advance on a probe that verified nothing")
	}
	if len(got.Resume) != 1 {
		t.Error("resume argv should come from the fresh descriptor")
	}
}

func TestMergeVerifiedProbeKeepsTheOriginalFirstSeen(t *testing.T) {
	old := Finding{Agent: "codex", State: StateVerified, FirstSeen: now.Add(-72 * time.Hour)}
	fresh := Finding{Agent: "codex", State: StateVerified, Sessions: 3, CheckedAt: now, VerifiedAt: now}

	got := Merge(old, fresh)

	if !got.FirstSeen.Equal(old.FirstSeen) {
		t.Errorf("first seen = %v, want the original %v", got.FirstSeen, old.FirstSeen)
	}
	if got.Sessions != 3 || got.Stale {
		t.Errorf("a fresh verified probe must win outright: %+v", got)
	}
}

func TestRegistryRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "stores.json")
	reg := loadRegistryAt(path)
	reg.Record(Finding{Agent: "claude", State: StateVerified, Dir: "/h/.claude/projects", Sessions: 7, CheckedAt: now, VerifiedAt: now})
	if err := reg.Save(); err != nil {
		t.Fatal(err)
	}

	again := loadRegistryAt(path)
	f, ok := again.Get("claude")
	if !ok || f.Dir != "/h/.claude/projects" || f.Sessions != 7 {
		t.Fatalf("registry did not survive a round trip: %+v", f)
	}
	if !again.ProbedAt.Equal(now) {
		t.Errorf("probed-at = %v, want %v", again.ProbedAt, now)
	}
}

func TestRegistryIgnoresAnUnreadableOrUnknownVersionFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stores.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := len(loadRegistryAt(path).Agents); got != 0 {
		t.Errorf("corrupt registry should read as empty, got %d agents", got)
	}

	other := filepath.Join(dir, "v99.json")
	if err := os.WriteFile(other, []byte(`{"version":99,"agents":{"claude":{"agent":"claude"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := len(loadRegistryAt(other).Agents); got != 0 {
		t.Errorf("unknown registry version should be discarded, got %d agents", got)
	}
}

// TestFindingJSONOmitsTimestampsThatWereNeverSet guards the machine-readable
// output: a store that has never been verified must not carry a verified-at
// date. Go's zero time would encode as "0001-01-01T00:00:00Z", which any script
// reading the file would take for a real one.
func TestFindingJSONOmitsTimestampsThatWereNeverSet(t *testing.T) {
	f := Finding{Agent: "codex", State: StateMissing, CheckedAt: now}
	data, err := json.Marshal(f)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "0001-01-01") {
		t.Errorf("unset timestamps leaked into JSON: %s", data)
	}
	var back map[string]any
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"verified_at", "first_seen", "newest"} {
		if _, present := back[k]; present {
			t.Errorf("%q should be absent when it was never set: %s", k, data)
		}
	}
	if _, present := back["checked_at"]; !present {
		t.Errorf("checked_at was set and must be present: %s", data)
	}

	// A location with no sessions in it gets the same treatment.
	l, err := json.Marshal(Location{Root: RootAgent, Dir: "/x"})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(l), "0001-01-01") {
		t.Errorf("unset location timestamp leaked into JSON: %s", l)
	}
}

func TestProjectBucket(t *testing.T) {
	for in, want := range map[string]string{
		"/workspace":            "-workspace",
		"/Users/x/proj":         "-Users-x-proj",
		"/Users/x/my.app":       "-Users-x-my-app",
		"/home/a/.config/thing": "-home-a--config-thing",
	} {
		if got := ProjectBucket(in); got != want {
			t.Errorf("ProjectBucket(%q) = %q, want %q", in, got, want)
		}
	}
}

// The bucket name has to be the one Claude Code uses, character for character —
// it is a directory both sides open, not a label we choose. Reported as #57: an
// underscore in the project path put sandboxed sessions in `…-intrupt_api`
// while the host read `…-intrupt-api`, so nothing written in the sandbox could
// be resumed outside it.
//
// The expectations are real bucket names observed under ~/.claude/projects on
// the reporting machine, all created by Claude Code itself, not derived from
// this implementation.
func TestProjectBucketMatchesClaudeCode(t *testing.T) {
	for _, tc := range []struct{ path, want string }{
		// The case from the report: '_' collapses like '/' and '.'.
		{
			"/Users/amitghadge/project/llm/human-in-loop/intrupt_api",
			"-Users-amitghadge-project-llm-human-in-loop-intrupt-api",
		},
		// A dotted directory gives a double hyphen, which is what confirmed the
		// rule is replacement rather than removal.
		{
			"/Users/amitghadge/.config/sandbox/worktrees/intrupt_api-fdce81c9/feature-enable-team-plan",
			"-Users-amitghadge--config-sandbox-worktrees-intrupt-api-fdce81c9-feature-enable-team-plan",
		},
		// The container's own path, which the history mount targets.
		{"/workspace", "-workspace"},
		{"/workspace/web", "-workspace-web"},
		// Ordinary paths keep working exactly as before.
		{"/Users/x/proj", "-Users-x-proj"},
	} {
		if got := ProjectBucket(tc.path); got != tc.want {
			t.Errorf("ProjectBucket(%q)\n got  %q\n want %q", tc.path, got, tc.want)
		}
	}
}

// Nothing but letters, digits and '-' may survive, whatever the path contains.
// Guessing a specific set of characters would fix the reported path and leave
// the next one broken.
func TestProjectBucketEmitsOnlySafeCharacters(t *testing.T) {
	got := ProjectBucket("/a b/c@d/e+f/g_h/i.j/k~l")
	for _, r := range got {
		ok := r == '-' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')
		if !ok {
			t.Fatalf("ProjectBucket kept %q in %q", r, got)
		}
	}
	if want := "-a-b-c-d-e-f-g-h-i-j-k-l"; got != want {
		t.Errorf("ProjectBucket = %q, want %q", got, want)
	}
}

func TestLookupUnknownAgent(t *testing.T) {
	if _, ok := Lookup("aider"); ok {
		t.Error("aider has no verified store descriptor; Lookup must say so")
	}
	if got := Untracked("aider", now).State; got != StateUntracked {
		t.Errorf("state = %q, want %q", got, StateUntracked)
	}
}

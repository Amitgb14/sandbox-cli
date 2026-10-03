package agentctx

import (
	"os"
	"path/filepath"
	"testing"
)

// writeSession puts a minimal claude transcript at dir/id.jsonl.
func writeSession(t *testing.T, dir, id string) string {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, id+".jsonl")
	line := `{"type":"user","cwd":"/workspace","message":{"role":"user","content":"hi"}}` + "\n"
	if err := os.WriteFile(p, []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// TestListSearchesEveryLocation is the regression for issue #15's second half.
// The claude wrapper has two stores — the host's own history and the persisted
// agent HOME — and List used to walk only whichever had been most recently
// active. A session in the other one was invisible to the command whose entire
// job is finding conversations.
func TestListSearchesEveryLocation(t *testing.T) {
	root := t.TempDir()
	winner := filepath.Join(root, "host", "projects")
	other := filepath.Join(root, "agenthome", "projects")

	bucket := ProjectBucket("/some/project")
	writeSession(t, filepath.Join(winner, bucket), "11111111-1111-1111-1111-111111111111")
	writeSession(t, filepath.Join(other, bucket), "22222222-2222-2222-2222-222222222222")

	f := Finding{
		Agent:  "claude",
		Format: FormatClaudeJSONL,
		Dir:    winner,
		Locations: []Location{
			{Dir: winner},
			{Dir: other},
		},
	}
	got, _, err := List(f, ListOpts{Project: "/some/project"})
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, s := range got {
		ids[s.ID] = true
	}
	if !ids["11111111-1111-1111-1111-111111111111"] {
		t.Error("session in the primary location is missing")
	}
	if !ids["22222222-2222-2222-2222-222222222222"] {
		t.Error("session in the second location is missing — this is issue #15")
	}
}

// TestListDoesNotDuplicateAcrossLocations: the same directory listed twice (Dir
// is normally also one of Locations) must not yield the session twice.
func TestListDoesNotDuplicateAcrossLocations(t *testing.T) {
	dir := t.TempDir()
	bucket := ProjectBucket("/p")
	writeSession(t, filepath.Join(dir, bucket), "33333333-3333-3333-3333-333333333333")

	f := Finding{
		Agent:     "claude",
		Format:    FormatClaudeJSONL,
		Dir:       dir,
		Locations: []Location{{Dir: dir}, {Dir: dir}},
	}
	got, _, err := List(f, ListOpts{Project: "/p"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d sessions, want 1 (duplicated across locations)", len(got))
	}
}

// TestPooledSessionsFindsTheSharedWorkspaceBucket: sessions recorded before the
// per-project history mount was fixed all sit in one -workspace bucket. They
// cannot be attributed to a project, but they must at least be reported.
func TestPooledSessionsFindsTheSharedWorkspaceBucket(t *testing.T) {
	root := t.TempDir()
	other := filepath.Join(root, "agenthome", "projects")
	writeSession(t, filepath.Join(other, "-workspace"), "44444444-4444-4444-4444-444444444444")

	f := Finding{
		Agent:     "claude",
		Format:    FormatClaudeJSONL,
		Dir:       filepath.Join(root, "host", "projects"),
		Locations: []Location{{Dir: other}},
	}
	dir, n := PooledSessions(f)
	if n != 1 {
		t.Fatalf("PooledSessions found %d, want 1", n)
	}
	if filepath.Base(dir) != "-workspace" {
		t.Errorf("dir = %q, want the -workspace bucket", dir)
	}

	// A store with no such bucket — the normal state after the fix — reports none.
	clean := Finding{Agent: "claude", Format: FormatClaudeJSONL, Dir: t.TempDir()}
	if _, n := PooledSessions(clean); n != 0 {
		t.Errorf("PooledSessions on a clean store found %d, want 0", n)
	}
}

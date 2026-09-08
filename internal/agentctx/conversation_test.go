package agentctx

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// writeStarted writes a transcript whose first user turn carries a start time,
// which is what PickSessionIn filters on.
func writeStarted(t *testing.T, dir, id string, started time.Time, cwd string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	line := `{"type":"user","timestamp":"` + started.UTC().Format(time.RFC3339) +
		`","cwd":"` + cwd + `","message":{"role":"user","content":"hi"}}` + "\n"
	if err := os.WriteFile(filepath.Join(dir, id+".jsonl"), []byte(line), 0o644); err != nil {
		t.Fatal(err)
	}
}

// ConversationFor must search the store a sandbox's transcripts actually land
// in, and this test exists because the version that did not looked identical
// from the outside.
//
// A sandbox run's conversation is written into the **host** bucket for the
// project, because the claude wrapper mounts that bucket into the container. The
// sandbox-owned store buckets by the container's own working directory, which is
// always `/workspace`. So searching the sandbox-owned store *with a project
// filter* is a query that cannot match — measured on a real machine as 14
// sessions the correct way and 0 that way.
//
// What made it dangerous is that nothing showed: an empty answer is also what
// this returns whenever it declines to guess, so a suite made only of refusals
// passes forever while the feature never once fires. Stubbing the lookups does
// not catch it either — the stub replaces the very layer that was wrong. Only a
// real store on disk does.
func TestConversationForSearchesTheStoreASandboxWritesTo(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	const project = "/repo"
	started := time.Date(2026, 9, 6, 10, 5, 0, 0, time.UTC)

	// The host store, bucketed by the project path — where a sandbox run's
	// transcript lands, through the history mount.
	writeStarted(t,
		filepath.Join(home, ".claude", "projects", ProjectBucket(project)),
		"11111111-1111-1111-1111-111111111111", started, project)

	// And the sandbox-owned store, bucketed by the container's cwd. Present so
	// the test reflects a real machine: narrowing to this store and filtering by
	// the project path is what found nothing.
	writeStarted(t,
		filepath.Join(home, ".config", "sandbox", "agents", "claude", ".claude", "projects", "-workspace"),
		"22222222-2222-2222-2222-222222222222", started, "/workspace")

	f, sess, ok := ConversationFor("claude", project,
		started.Add(-5*time.Minute), started.Add(30*time.Minute))
	if !ok {
		t.Fatal("found no conversation for a run whose transcript is on disk")
	}
	if sess.ID != "11111111-1111-1111-1111-111111111111" {
		t.Errorf("session = %q, want the one in the project's bucket", sess.ID)
	}
	if f.Agent != "claude" || len(f.Resume) == 0 {
		t.Errorf("finding = %+v, want a verified claude store with a resume argv", f)
	}
}

// The refusals, which are decisions rather than gaps: resuming the wrong
// conversation is worse than offering none.
func TestConversationForDeclinesRatherThanGuessing(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	const project = "/repo"
	started := time.Date(2026, 9, 6, 10, 5, 0, 0, time.UTC)
	bucket := filepath.Join(home, ".claude", "projects", ProjectBucket(project))
	from, until := started.Add(-5*time.Minute), started.Add(30*time.Minute)

	if _, _, ok := ConversationFor("", project, from, until); ok {
		t.Error("answered for a run with no agent — a plain `run` has no conversation")
	}
	if _, _, ok := ConversationFor("claude", "", from, until); ok {
		t.Error("answered with no project to scope the search to")
	}

	// A session that began before the run cannot be that run's, however recently
	// it was written to.
	writeStarted(t, bucket, "33333333-3333-3333-3333-333333333333",
		started.Add(-4*time.Hour), project)
	if _, sess, ok := ConversationFor("claude", project, from, until); ok {
		t.Errorf("picked a session that began before the window: %s", sess.ID)
	}

	// Two inside the window and no prompt to separate them: nothing, rather than
	// the newest.
	writeStarted(t, bucket, "44444444-4444-4444-4444-444444444444", started, project)
	writeStarted(t, bucket, "55555555-5555-5555-5555-555555555555",
		started.Add(time.Minute), project)
	if _, sess, ok := ConversationFor("claude", project, from, until); ok {
		t.Errorf("guessed between two sessions in one window: %s", sess.ID)
	}
}

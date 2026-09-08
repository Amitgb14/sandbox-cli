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

// A run started by the CLI has the host's history bucket for the project
// mounted into its HOME, so its transcript lands under the host path. This is
// the shape internal/cli has always correlated, and the one a project filter
// alone can find.
func TestConversationForFindsACLIRunsTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	const project = "/repo"
	started := time.Date(2026, 9, 6, 10, 5, 0, 0, time.UTC)

	// A CLI run gets the host's history bucket for the project mounted into its
	// HOME, so its transcript lands under the host path.
	writeStarted(t,
		filepath.Join(home, ".claude", "projects", ProjectBucket(project)),
		"11111111-1111-1111-1111-111111111111", started, project)

	_, sess, ok := ConversationFor("claude", project,
		started.Add(-5*time.Minute), started.Add(30*time.Minute))
	if !ok || sess.ID != "11111111-1111-1111-1111-111111111111" {
		t.Fatalf("CLI-shaped run: got %q/%v, want the session in the project bucket", sess.ID, ok)
	}
}

// The shape the review caught, and the reason searching one bucket is not merely
// incomplete but wrong.
//
// A Studio run gets **no history mount** — studioapi.buildRunOptions builds none
// — so its transcript lands under the container's own working directory,
// `/workspace`. Searching only the host project bucket therefore cannot find it,
// and the one thing that bucket *does* hold is the developer's own Claude Code
// sessions for the same project. So the single conversation the old code could
// offer for a Studio run was the one that is not the run's.
func TestConversationForFindsAStudioRunsTranscript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	const project = "/repo"
	started := time.Date(2026, 9, 6, 10, 5, 0, 0, time.UTC)
	agentHome := filepath.Join(home, ".config", "sandbox", "agents", "claude", ".claude", "projects")

	// The run's own transcript, in the pooled container bucket.
	writeStarted(t, filepath.Join(agentHome, ProjectBucket(ContainerWorkspace)),
		"22222222-2222-2222-2222-222222222222", started, ContainerWorkspace)

	// And the developer's own session in the same project, well outside the
	// window — present so the test fails loudly if the window ever stops
	// separating them.
	writeStarted(t, filepath.Join(home, ".claude", "projects", ProjectBucket(project)),
		"99999999-9999-9999-9999-999999999999", started.Add(-6*time.Hour), project)

	_, sess, ok := ConversationFor("claude", project,
		started.Add(-5*time.Minute), started.Add(30*time.Minute))
	if !ok {
		t.Fatal("found no conversation for a Studio-shaped run")
	}
	if sess.ID != "22222222-2222-2222-2222-222222222222" {
		t.Fatalf("session = %q, want the run's own — not the developer's", sess.ID)
	}
}

// Both buckets holding a session inside one window is a genuine ambiguity: the
// transcripts record no more than `/workspace`, so nothing can say which is the
// run's. Nothing is offered.
func TestConversationForDeclinesWhenBothBucketsMatch(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))

	const project = "/repo"
	started := time.Date(2026, 9, 6, 10, 5, 0, 0, time.UTC)

	writeStarted(t, filepath.Join(home, ".claude", "projects", ProjectBucket(project)),
		"aaaaaaaa-1111-1111-1111-111111111111", started, project)
	writeStarted(t,
		filepath.Join(home, ".config", "sandbox", "agents", "claude", ".claude", "projects",
			ProjectBucket(ContainerWorkspace)),
		"bbbbbbbb-2222-2222-2222-222222222222", started.Add(time.Minute), ContainerWorkspace)

	if _, sess, ok := ConversationFor("claude", project,
		started.Add(-5*time.Minute), started.Add(30*time.Minute)); ok {
		t.Errorf("guessed between two buckets' sessions in one window: %s", sess.ID)
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

package protocol

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
)

// The catalog the handoff pack shipped as its worked example, read by this
// package's own types.
//
// It is here because a schema and a struct agree right up until one of them is
// edited, and the example is the only artefact that was written before the code:
// if the struct drifts, this is what notices. The two departures from the pack's
// schema are applied to the fixture rather than hidden — `pane_kind` instead of
// `kind`, and no `argv` — and both are recorded in
// docs/architecture/session-server.md.
func TestSessionExampleRoundTrips(t *testing.T) {
	b, err := os.ReadFile("testdata/session.example.json")
	if err != nil {
		t.Fatal(err)
	}

	// Strict: an unknown field here means the fixture says something the code
	// cannot hear, which for a catalog is how a pane ends up reported as stopped
	// because the field saying otherwise was skipped.
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	var sess Session
	if err := dec.Decode(&sess); err != nil {
		t.Fatalf("the example catalog does not fit the types: %v", err)
	}

	if sess.Version != Version {
		t.Errorf("version = %d, want %d", sess.Version, Version)
	}
	if len(sess.Workspaces) != 1 || len(sess.Workspaces[0].Worktrees) != 3 {
		t.Fatalf("grouping lost: %d workspaces, %d worktrees in the first",
			len(sess.Workspaces), len(sess.Workspaces[0].Worktrees))
	}
	if len(sess.Panes) != 1 {
		t.Fatalf("panes = %d, want 1", len(sess.Panes))
	}

	p := sess.Panes[0]
	if p.Kind != PaneAgent || p.Agent != "claude" {
		t.Errorf("pane = %q/%q, want agent/claude", p.Kind, p.Agent)
	}
	// The pane points *up* at its worktree, and the worktree it names has to be
	// one the catalog actually holds — a dangling worktree_id is a pane that
	// renders under nothing.
	var found bool
	for _, wt := range sess.Workspaces[0].Worktrees {
		if wt.ID == p.WorktreeID {
			found = true
		}
	}
	if !found {
		t.Errorf("pane names worktree %q, which is not in the catalog", p.WorktreeID)
	}
	// exit_code is a pointer so that 0 and "has not exited" are different
	// answers. The example's pane is running, so it must be nil rather than 0.
	if p.ExitCode != nil {
		t.Errorf("a running pane reported exit code %d; nil is the only honest value", *p.ExitCode)
	}

	// And back out again, through the same types, without losing the ids.
	out, err := json.Marshal(sess)
	if err != nil {
		t.Fatal(err)
	}
	var again Session
	if err := json.Unmarshal(out, &again); err != nil {
		t.Fatal(err)
	}
	if again.ID != sess.ID || again.Panes[0].ID != p.ID {
		t.Error("a round trip changed the ids")
	}
}

// A pane records what it *is*, not what it was told. The pack's schema has an
// `argv` array and its example had a prompt sitting in one; this pins the
// decision, because the field is exactly the kind a later reader adds back for
// convenience.
func TestPaneCannotHoldAnArgv(t *testing.T) {
	b, err := json.Marshal(Pane{ID: "p_1", Kind: PaneAgent})
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	for _, banned := range []string{"argv", "prompt", "command", "env"} {
		if _, ok := m[banned]; ok {
			t.Errorf("Pane serialises %q: the catalog is a file, and a prompt is the user's content — audit.SessionMeta has nowhere to put one for the same reason", banned)
		}
	}

	// The same claim from the reading side: a catalog that *has* an argv is a
	// catalog written by something else, and strict decoding is what says so.
	dec := json.NewDecoder(bytes.NewReader([]byte(`{"id":"p_1","pane_kind":"agent","argv":["claude","-p","secret"]}`)))
	dec.DisallowUnknownFields()
	var p Pane
	if err := dec.Decode(&p); err == nil {
		t.Error("accepted a pane carrying an argv")
	}
}

// Running() is derived from the state rather than stored beside it, so the two
// cannot disagree. The table is the whole contract: a pane that has finished is
// not running, and one whose state nobody could determine is — because the
// container is there and the *agent's* state is what is unknown.
func TestRunningFollowsTheState(t *testing.T) {
	for state, want := range map[PaneState]bool{
		StateUnknown:  true,
		StateStarting: true,
		StateWorking:  true,
		StateBlocked:  true,
		StateIdle:     true,
		StateDone:     false,
		StateFailed:   false,
		StateStopped:  false,
	} {
		if got := (Pane{State: state}).Running(); got != want {
			t.Errorf("state %q: Running() = %v, want %v", state, got, want)
		}
	}
}

// The envelope's shapes, and the one distinction a client depends on: `ok` is
// explicit, so "succeeded with nothing to say" is not the same bytes as a reply
// that lost its error.
func TestEnvelopeDistinguishesSuccessFromSilence(t *testing.T) {
	ok, err := json.Marshal(Response{ID: "c1", OK: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := string(ok); got != `{"id":"c1","ok":true}` {
		t.Errorf("empty success = %s", got)
	}
	bad, err := json.Marshal(Response{ID: "c1", OK: false,
		Error: Errorf(CodeConflict, "sandbox-x-feat is already running")})
	if err != nil {
		t.Fatal(err)
	}
	var parsed Response
	if err := json.Unmarshal(bad, &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed.OK || parsed.Error == nil || parsed.Error.Code != CodeConflict {
		t.Errorf("refusal did not survive a round trip: %s", bad)
	}
	// Both halves, always. A code with no message is how a CLI ends up printing
	// "conflict" at somebody.
	if parsed.Error.Message == "" {
		t.Error("refusal carried a code and no sentence")
	}
}

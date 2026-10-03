package session

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/audit"
	"github.com/Amitgb14/sandbox-cli/internal/protocol"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

// Spawn gives a container a pane identity, starts it through the path that
// already exists, and records it in the catalog.
//
// The signature is the design. It takes **already-built `sandbox.Options`** and
// does not construct them, which is what keeps this from becoming a third place
// that decides what a container may reach. `internal/fleet`'s rule with teeth —
// every gate on the run path must be repeated by every caller that builds
// `Options` — is a rule about *builders*, and `gates_test.go` exists because that
// rule was broken once. A spawn that assembled its own Options would inherit that
// whole obligation in exchange for nothing: the caller has already resolved the
// config, applied the flags and passed the gates, and all this adds is three
// labels and a row in a file.
//
// So what it may set is exactly the three pane fields, and `opts` is taken by
// value so it cannot reach back into the caller's. Everything else about the
// container is whatever the caller decided.
//
// Starting goes through `sandbox.Session.Start`, which is the same function
// `--detach` has always called: it prepares the spec, checks the writable mounts,
// enforces seccomp, builds the image, resolves the forwarded secrets and writes the
// audit line. None of that is repeated here, and the argv is assembled in exactly
// one place.
func (s *Server) Spawn(ctx context.Context, sess *sandbox.Session, opts sandbox.Options, kind protocol.PaneKind, forceBuild bool) (protocol.Pane, error) {
	pane, _, err := s.SpawnRecorded(ctx, sess, opts, kind, forceBuild)
	return pane, err
}

// SpawnRecorded is Spawn, and also hands back the audit record the launch wrote.
//
// It exists for the caller that will later learn something this one cannot: how the
// run *ended*. A detached launch has no exit code to wait for, so its audit line
// carries a placeholder and `Finished: false`; whoever sees the container stop
// completes the pair by writing the same record again with the real outcome. Handing
// the record over is what makes that second line describe the same run rather than a
// reconstruction of it — `internal/studioapi`'s supervisor is the caller, and the
// reason `sandbox.Session` already has this exact pair.
//
// Two entry points, one implementation, so the two cannot drift.
func (s *Server) SpawnRecorded(ctx context.Context, sess *sandbox.Session, opts sandbox.Options, kind protocol.PaneKind, forceBuild bool) (protocol.Pane, audit.SessionMeta, error) {
	if sess == nil {
		return protocol.Pane{}, audit.SessionMeta{}, fmt.Errorf("no sandbox session to start in")
	}
	if kind == "" {
		return protocol.Pane{}, audit.SessionMeta{}, fmt.Errorf("a pane needs a kind: one of agent, shell, command, verify, console")
	}

	// Minted before the container exists, because the id has to be a **label** and
	// labels are set at creation: docker cannot add one afterwards. A pane whose id
	// is not on its container is one no later `serve` can rebind, so it would come
	// back from a restart as a legacy container addressable only by name — which is
	// the whole failure this phase exists to stop.
	id := newPaneID()
	opts.PaneID = id
	opts.PaneKind = string(kind)
	opts.PaneSession = s.sessionID()

	name, meta, err := sess.StartRecorded(ctx, opts, forceBuild)
	if err != nil {
		// Nothing is recorded. A pane for a container that was refused is a row
		// claiming a run happened, which is the same reason `StartRecorded` writes no
		// audit line for a failed launch.
		return protocol.Pane{}, audit.SessionMeta{}, err
	}

	pane := protocol.Pane{
		ID:             id,
		WorktreeID:     worktreeIDFor(opts.Branch),
		Kind:           kind,
		Agent:          opts.Agent,
		Sandbox:        string(sess.Cfg.Sandbox.Resolve(sess.Cfg.Engine)),
		ContainerName:  name,
		State:          protocol.StateUnknown,
		ConversationID: opts.SessionID,
		LastSnapshot:   opts.Baseline,
		CreatedAt:      time.Now().UTC(),
		UpdatedAt:      time.Now().UTC(),
	}

	s.mu.Lock()
	s.sess.Panes = append(s.sess.Panes, pane)
	sortPanes(s.sess.Panes)
	s.mu.Unlock()

	// A failed save does **not** fail the spawn. The container is running: telling
	// the caller the launch failed would be false, and a caller that retried on it
	// would be asking for a second agent on one branch. The engine still holds the
	// pane id as a label, so the next `Adopt` recovers the row this could not write
	// — which is the point of keeping the id on the container rather than only here.
	if err := s.Save("spawn " + id); err != nil {
		return pane, meta, &SaveError{PaneID: id, Container: name, Err: err}
	}
	return pane, meta, nil
}

// SaveError says the container started and the catalog did not record it.
//
// A distinct type rather than a logged line, because the caller is the only one
// who can phrase it usefully — and because it must not be mistaken for a failure
// to launch. Callers print it and carry on.
type SaveError struct {
	PaneID    string
	Container string
	Err       error
}

func (e *SaveError) Error() string {
	return fmt.Sprintf("%s started as pane %s, but the catalog could not be written: %v\n"+
		"  the pane id is a label on the container, so `sandbox-cli serve` will pick it up",
		e.Container, e.PaneID, e.Err)
}

func (e *SaveError) Unwrap() error { return e.Err }

// sessionID is the catalog's own id, read under the lock.
func (s *Server) sessionID() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sess.ID
}

// Resolve finds the pane a reference names.
//
// The rule `internal/cli`'s `resolveSession` already keeps, restated because this
// is the second place that resolves one: **a reference is matched against panes
// this tool knows about and is never handed to the engine.** `pane kill postgres`
// must find nothing rather than somebody's database.
//
// Four forms, in one tier rather than in priority order, because an ambiguity
// between *kinds* of match is still an ambiguity: a pane id, a container name, a
// short container id, and a branch. Liveness is the only tie-break — a branch with
// one finished pane and one running pane can only mean the running one, which is
// the same exception the session resolver makes.
func (s *Server) Resolve(ref string) (protocol.Pane, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return protocol.Pane{}, protocol.Errorf(protocol.CodeInvalid,
			"name a pane; `sandbox-cli pane list` shows what there is")
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	var matches []protocol.Pane
	for _, p := range s.sess.Panes {
		switch {
		case p.ID == ref,
			p.ContainerName == ref,
			p.ContainerID != "" && (p.ContainerID == ref || shortID(p.ContainerID) == ref),
			branchOfID(p.WorktreeID) == sanitizeID(ref):
			matches = append(matches, p)
		}
	}
	switch len(matches) {
	case 0:
		return protocol.Pane{}, protocol.Errorf(protocol.CodeNotFound,
			"no pane matches %q; `sandbox-cli pane list --all` shows every one", ref)
	case 1:
		return matches[0], nil
	}
	var live []protocol.Pane
	for _, p := range matches {
		if p.Running() {
			live = append(live, p)
		}
	}
	if len(live) == 1 {
		return live[0], nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%q matches %d panes; name one by its pane id:", ref, len(matches))
	for _, p := range matches {
		fmt.Fprintf(&b, "\n  %s  %s  (%s)", p.ID, p.ContainerName, p.State)
	}
	return protocol.Pane{}, protocol.Errorf(protocol.CodeAmbiguous, "%s", b.String())
}

func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

package protocol

import "time"

// Session is the catalog: what this daemon knows is running, grouped the way a
// person thinks about it.
//
// It holds **layout**, and the distinction from the other thing called a snapshot
// in this tool is worth stating where both are in view. `internal/rescue` writes
// workspace *files* to git under `refs/sandbox/snapshots/`; that is what survives
// a crash and what `recover restore` gives back. This is which worktrees exist and
// which containers are in them. Neither can answer the other's question, and
// restoring a layout deliberately starts no agent — `LastSnapshot` below is a
// pointer from one layer to the other, not a copy of it.
//
// Shaped to match schemas/session.schema.json from the handoff pack, with two
// deliberate departures recorded in docs/architecture/session-server.md: a pane
// carries no `argv`, and the catalog is scoped to one repository.
type Session struct {
	Version int    `json:"version"`
	ID      string `json:"id"`
	Name    string `json:"name"`
	// Root is the repository this session belongs to. One session, one repository:
	// the id is derived from it, so two repositories cannot share a socket and a
	// request never has to say which repository it meant.
	Root string `json:"root"`
	// Profile is omitted rather than empty when it is not known, which is the rule
	// this codebase keeps everywhere else: absent means absent, never a placeholder.
	// `""` would also be an invalid value — the field is dev or prod — so a reader
	// validating the catalog would call it malformed rather than incomplete.
	//
	// It is not known on the read path by design: a listing records nothing, so it
	// does not load the project config and has no business failing on one. Only the
	// daemon, which writes the catalog, resolves a profile — and refuses if it
	// cannot.
	Profile string `json:"profile,omitempty"`

	Share     bool   `json:"share,omitempty"`
	SharePath string `json:"share_path,omitempty"`
	Engine    string `json:"engine,omitempty"`

	CreatedAt time.Time `json:"created_at,omitzero"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`

	// Focus is what a client was last looking at. Server-stored and
	// server-ignored: it exists so a UI reopening a session lands where it was,
	// and nothing about a run depends on it.
	Focus *Focus `json:"focus,omitempty"`

	Workspaces []Workspace `json:"workspaces"`

	// Panes is flat, and the nesting lives in Workspace.Worktrees as ids. A pane
	// points *up* at its worktree rather than worktrees holding their panes,
	// because adoption discovers panes from container labels — one at a time, in
	// whatever order the engine lists them — and a tree built from the leaves in
	// arbitrary order is a tree that is wrong halfway through.
	Panes []Pane `json:"panes"`
}

// Focus is the last thing a client was looking at.
type Focus struct {
	Workspace string `json:"workspace,omitempty"`
	Worktree  string `json:"worktree,omitempty"`
	Pane      string `json:"pane,omitempty"`
}

// Workspace is a repository.
type Workspace struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	// RepoPath is the main checkout; RepoHash is worktree.RepoID, which already
	// carries the directory name *and* a hash of its absolute path. It is the same
	// value stamped as the `sandbox.repo` label, used to build the container name,
	// and used to name the managed worktree directory — so "this pane belongs to
	// that repository" is true by construction rather than by comparison.
	RepoPath  string     `json:"repo_path"`
	RepoHash  string     `json:"repo_hash"`
	Worktrees []Worktree `json:"worktrees"`
}

// Worktree is a git worktree: one branch, one directory, mounted at /workspace.
type Worktree struct {
	ID     string `json:"id"`
	Branch string `json:"branch"`
	Path   string `json:"path"`
	// Main marks the user's own checkout rather than a sandbox-managed worktree.
	// It is in the catalog because a pane can run against it, and it is the one
	// worktree `worktree rm` must never touch.
	Main  bool   `json:"main,omitempty"`
	Label string `json:"label,omitempty"`
}

// PaneKind is what a pane is for.
//
// Named `pane_kind` on the wire and as a container label, *not* `kind`:
// `sandbox-cli list` already has a KIND column and it already means
// interactive-vs-fleet. Two different KINDs in one tool is the kind of collision
// that is obvious in a diff and invisible in a terminal.
type PaneKind string

const (
	// PaneAgent is a coding agent — the case everything else exists to serve.
	PaneAgent PaneKind = "agent"
	// PaneShell is an interactive shell in the sandbox.
	PaneShell PaneKind = "shell"
	// PaneCommand is `run -- something`: one command, an exit code, done.
	PaneCommand PaneKind = "command"
	// PaneVerify is a fleet task's definition of done. Its exit code decides the
	// task, which is why it is a pane of its own rather than a property of the
	// agent pane: the agent's opinion of its work is not evidence.
	PaneVerify PaneKind = "verify"
	// PaneConsole is an agent started in its interactive argv on a container that
	// keeps a terminal, so somebody can answer it.
	PaneConsole PaneKind = "console"
)

// PaneState is how a pane is doing.
//
// Phase 1 fills in only what a container can prove: Running, Stopped, Failed,
// Unknown. The middle states — working, blocked, idle — need
// `internal/detect` reading a transcript, and are phase 4. Until then nothing
// synthesises them, because StateUnknown is an honest answer and StateBlocked is
// a guess that would have somebody waiting for an agent that is not asking.
type PaneState string

const (
	// StateUnknown is the default, and stays the default whenever the evidence
	// runs out. Never a placeholder for "probably fine".
	StateUnknown PaneState = "unknown"
	// StateStarting is created but not yet running.
	StateStarting PaneState = "starting"
	// StateWorking, StateBlocked and StateIdle are phase 4: they describe the
	// agent, not the container, and only a transcript can tell them apart.
	StateWorking PaneState = "working"
	StateBlocked PaneState = "blocked"
	StateIdle    PaneState = "idle"
	// StateDone is a container that exited 0, StateFailed non-zero. The split is
	// the only thing an exit code can honestly say.
	StateDone   PaneState = "done"
	StateFailed PaneState = "failed"
	// StateStopped is a pane whose container is gone — killed, reaped, or lost to
	// a reboot. The pane survives it, which is the point of the catalog: what ran
	// is worth knowing after it stops running.
	StateStopped PaneState = "stopped"
)

// KnownPaneState reports whether s names a state at all.
//
// Needed because a caller validating `--state` had nothing to ask: `detect.Describe`
// answers every input, by design — a state it has not heard of is exactly when
// somebody needs a sentence — so checking it for emptiness validated nothing and
// `--state typo` became a wait that could never end.
func KnownPaneState(s PaneState) bool {
	switch s {
	case StateUnknown, StateStarting, StateWorking, StateBlocked,
		StateIdle, StateDone, StateFailed, StateStopped:
		return true
	}
	return false
}

// PaneStateNames is every state, for a message that has to list them.
func PaneStateNames() []string {
	return []string{
		string(StateUnknown), string(StateStarting), string(StateWorking),
		string(StateBlocked), string(StateIdle), string(StateDone),
		string(StateFailed), string(StateStopped),
	}
}

// Pane is one sandbox: a container, and the facts about it a later command needs.
//
// What it deliberately does **not** hold is the argv. The handoff pack's own
// security notes say not to copy prompts into the catalog, while its schema has a
// plain `argv` array and its example has a prompt sitting in one. `audit.SessionMeta`
// records environment variables by name and has nowhere to put a value for the same
// reason: a catalog is a file, a prompt is the user's content, and the argv is
// already on the container where `docker inspect` can show it to whoever can
// already see the container. A pane records what it *is*, not what it was told.
type Pane struct {
	ID         string   `json:"id"`
	WorktreeID string   `json:"worktree_id"`
	Kind       PaneKind `json:"pane_kind"`
	Agent      string   `json:"agent,omitempty"`

	// Sandbox is what isolated this pane, recorded rather than inferred. A
	// catalog read next week should not have to ask which engine the machine has
	// now.
	Sandbox string `json:"sandbox"`

	ContainerName string `json:"container_name,omitempty"`
	ContainerID   string `json:"container_id,omitempty"`

	State PaneState `json:"state"`
	// ExitCode is a pointer because zero is a real exit code and "has not exited"
	// is a real state, and an int cannot hold both.
	ExitCode *int `json:"exit_code,omitempty"`

	// ConversationID is the agent's own session id, so a stopped pane can be
	// carried on rather than restarted. LastSnapshot is the git ref under
	// refs/sandbox/snapshots/ — a pointer into the other snapshot layer, never a
	// copy of what it holds.
	ConversationID string `json:"conversation_id,omitempty"`
	LastSnapshot   string `json:"last_snapshot,omitempty"`

	// Legacy marks a container started before this daemon existed. It has no pane
	// label, so its ID *is* its container name: a synthesised `p_` id would name
	// something no engine has heard of, and mutating one by that id is refused
	// with CodeLegacyReadonly rather than silently addressing nothing.
	Legacy bool `json:"legacy,omitempty"`

	CreatedAt time.Time `json:"created_at,omitzero"`
	UpdatedAt time.Time `json:"updated_at,omitzero"`
}

// Running reports whether this pane's container is live. Derived from the state
// rather than stored beside it, so the two cannot disagree.
func (p Pane) Running() bool {
	return p.State != StateStopped && p.State != StateDone && p.State != StateFailed
}

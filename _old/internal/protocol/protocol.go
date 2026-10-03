// Package protocol is the wire format between sandbox-cli's clients — the CLI,
// Studio, an SDK — and the session server that owns the catalog of running
// sandboxes.
//
// Transport is a unix domain stream socket carrying **NDJSON**: one JSON object
// per line, UTF-8, newline terminated. The choice is deliberate and the
// alternatives were weighed. gRPC would mean a code generator and a dependency
// tree in a module whose whole argument is that it depends on cobra and yaml.v3;
// HTTP over the socket would mean a second server shape to reason about beside
// studioapi's, for a protocol whose only transport is a file in the user's config
// directory. A line of JSON can be read by `socat`, which is worth more here than
// a schema compiler.
//
// The socket is the authentication: it lives `0600` in a `0700` directory, and
// same-uid is the whole check. There is no token, because a caller who can open
// it can already run `docker` as the user — a token would be a second secret
// protecting nothing. Studio keeps its loopback bind and its bearer token; that
// is a different boundary (a *browser* may reach Studio) and it does not move.
//
// This package is types and framing only. It starts nothing, reads no config and
// imports nothing from sandbox-cli beyond the standard library, so a client can
// depend on the wire format without pulling in the engine.
package protocol

import (
	"encoding/json"
	"fmt"
)

// Version is the protocol revision reported by session.hello. A client that does
// not recognise it should say so rather than guess: the catalog is the kind of
// thing whose shape changes, and a reader that guesses is a reader that reports a
// container as stopped because it could not parse the field saying otherwise.
const Version = 1

// MaxMessage bounds one NDJSON line. Attach and log payloads that need more than
// this use a stream mode rather than a bigger envelope — a line-oriented protocol
// with unbounded lines is a protocol where one client can exhaust the server's
// memory by never sending a newline.
const MaxMessage = 1 << 20 // 1 MiB

// Request is one client call. Every field is required except params: an id so the
// response can be matched on a connection that has several in flight, and an op
// naming what to do.
type Request struct {
	ID     string          `json:"id"`
	Op     string          `json:"op"`
	Params json.RawMessage `json:"params,omitempty"`
}

// Response is the answer to exactly one Request, carrying the same id.
//
// `ok` is explicit rather than implied by the presence of `error`, because the
// two absent fields — no result and no error — are a real case (an op that
// succeeds and has nothing to say), and "no error means success" makes that
// indistinguishable from a malformed reply.
type Response struct {
	ID     string `json:"id"`
	OK     bool   `json:"ok"`
	Result any    `json:"result,omitempty"`
	Error  *Error `json:"error,omitempty"`
}

// Event is a server-initiated message. It carries no id, which is how a client
// tells it from a response: events arrive unprompted, on a connection that asked
// to receive them.
type Event struct {
	Event string `json:"event"`
	Pane  string `json:"pane,omitempty"`
	State string `json:"state,omitempty"`
	At    string `json:"at"`
}

// Error is a refusal with a machine-readable code and a sentence for the person
// reading it. Both, always: the code is what a client branches on, and the
// message is what the user sees — and a code with no message is how a CLI ends up
// printing "conflict" at somebody.
type Error struct {
	Code    Code   `json:"code"`
	Message string `json:"message"`
}

func (e *Error) Error() string { return string(e.Code) + ": " + e.Message }

// Code is the closed set of refusal kinds. Closed because a client switches on
// it: a code invented at a call site is one no caller handles.
type Code string

const (
	// CodeNotFound — the thing named does not exist. Deliberately also the answer
	// for a path containment failure, so a refusal cannot be used to probe what
	// is on the host (the same rule studioapi/files.go keeps).
	CodeNotFound Code = "not_found"

	// CodeAmbiguous — the reference matched more than one pane. Never resolved by
	// preference: stopping the wrong agent costs its work, so the candidates are
	// named and the caller chooses.
	CodeAmbiguous Code = "ambiguous"

	// CodePolicyRefused — the request asked for something the security boundary
	// does not allow. This is the code for everything in config.trust and
	// sandbox.RefuseUnsafeHostPath, and it is never downgraded to a warning.
	CodePolicyRefused Code = "policy_refused"

	// CodeEngineUnavailable — docker or podman could not be reached at all.
	// Separate from CodeSandboxUnavailable because "no daemon" and "this kind of
	// sandbox is not implemented" need different things done about them.
	CodeEngineUnavailable Code = "engine_unavailable"

	// CodeSandboxUnavailable — the requested config.SandboxKind cannot run here.
	CodeSandboxUnavailable Code = "sandbox_unavailable"

	// CodeConflict — a container of that name is already running. This is the
	// one-agent-per-branch lock surfacing: the engine refuses a duplicate name
	// atomically, which is what makes it a lock rather than a check.
	CodeConflict Code = "conflict"

	// CodeVerifyFailed — a verify pane exited non-zero. Its exit code, not the
	// agent's account of itself, is what decides a fleet task.
	CodeVerifyFailed Code = "verify_failed"

	// CodeTimeout — a wait expired. Not a failure of the thing waited on.
	CodeTimeout Code = "timeout"

	// CodeUnauthenticated — reserved. The unix socket does not use it (same-uid
	// is the check); it exists for a gateway in front of this protocol.
	CodeUnauthenticated Code = "unauthenticated"

	// CodeInvalid — malformed request, unknown op, or params that do not parse.
	CodeInvalid Code = "invalid"

	// CodeLegacyReadonly — a mutation was attempted, by pane id, on a container
	// started before this daemon existed. Those have no pane label, so their id
	// *is* their container name and a synthesised `p_` id would address nothing.
	// Resolve them by name.
	CodeLegacyReadonly Code = "legacy_readonly"
)

// Errorf builds a refusal. The code is a parameter rather than inferred from the
// message, because the sentence is written for a person and the code is read by a
// program, and deriving either from the other makes one of them worse.
func Errorf(code Code, format string, args ...any) *Error {
	return &Error{Code: code, Message: fmt.Sprintf(format, args...)}
}

// Ops implemented in phase 1. The rest of the protocol's surface is named in
// docs/architecture/session-server.md and answers CodeInvalid until its phase
// lands — an op that accepts a request and does nothing is worse than one that
// refuses, because a client cannot tell the first from success.
const (
	// OpHello must be the first op on a connection. It is what reports the
	// protocol revision, so a client that speaks a different one finds out before
	// it has asked for anything.
	OpHello = "session.hello"

	// OpSnapshot returns the whole catalog: workspaces, worktrees, panes.
	OpSnapshot = "session.snapshot"

	// OpPaneList returns panes, optionally including the ones that have exited.
	OpPaneList = "pane.list"

	// OpPaneWait blocks until a pane reaches one of a set of states, or the wait
	// expires. It is what turns the state machine into something a script can use:
	// "start six agents, tell me when one needs me" is otherwise a polling loop in
	// every caller.
	OpPaneWait = "pane.wait"
)

// HelloParams identifies the client. Advisory: nothing is gated on it, and it is
// recorded so a daemon's event log can say which client asked for something.
type HelloParams struct {
	Client  string `json:"client"`
	Version string `json:"version"`
}

// HelloResult is what a connection learns before it asks for anything.
type HelloResult struct {
	Protocol int      `json:"protocol"`
	Engine   string   `json:"engine"`
	Session  *Session `json:"session,omitempty"`
}

// PaneListParams narrows a listing. Empty means every pane in this session that
// is still running.
type PaneListParams struct {
	Workspace string `json:"workspace,omitempty"`
	Worktree  string `json:"worktree,omitempty"`
	// All includes panes whose container has exited. Off by default because the
	// common question is "what is running", and a listing that answers a
	// different question by default is one people learn to filter.
	All bool `json:"all,omitempty"`
}

// PaneWaitParams asks to be told when a pane reaches one of these states.
//
// A *set* rather than one state, because the useful questions are disjunctions:
// "blocked or done" is "tell me when you need me or you are finished", and waiting
// for one of those alone means missing the other and timing out.
type PaneWaitParams struct {
	Pane   string      `json:"pane"`
	States []PaneState `json:"states"`
	// TimeoutMS bounds the wait. Required in practice: a wait with no bound is a
	// client that hangs forever on a pane that will never reach the state, which is
	// every typo.
	TimeoutMS int `json:"timeout_ms,omitempty"`
}

// PaneWaitResult is the state that ended the wait.
//
// It carries the whole pane rather than just the state, because a caller that waited
// for `done` usually wants the exit code next, and a second round trip to get it
// would race the container being reaped.
type PaneWaitResult struct {
	State PaneState `json:"state"`
	Pane  Pane      `json:"pane"`
}

// PaneListResult is a flat list. Flat rather than nested under worktrees, because
// `pane list` prints a table and a client that wants the grouping asks for
// session.snapshot, which has it.
type PaneListResult struct {
	Panes []Pane `json:"panes"`
}

// OpPaneSpawn starts a pane.
//
// The one op that *creates* something, and therefore the only one whose params are
// a trust boundary rather than a question. Everything PaneSpawnParams may carry is
// enumerated there, and what it deliberately cannot carry is the point: a caller on
// this socket can ask for a sandbox, and cannot ask for a different *kind* of
// sandbox.
const OpPaneSpawn = "pane.spawn"

// PaneSpawnParams is a request to start a pane.
//
// **What is absent is the design.** There is no `mounts`, no `secrets`, no `env`, no
// `env_allow`, no `user`, no `image`, no `runtime`, no `no_hardening` — the fields
// `config/trust.go` refuses from a project file, for the same reasons. A socket is a
// narrower thing to trust than a terminal: same-uid is its whole authentication, so
// a caller that can open it can already run docker as you — but "can already" is not
// "should, through this" , and an op that forwarded those fields would make this
// daemon a way to launder them past every refusal that reads a config.
//
// Each field below is classified by `TestSpawnParamsAreAllClassified`, which fails
// when the struct grows one that has not been decided on. That test is the reason
// this type is safe to extend: a new field is a new way for a socket-spawned
// container to differ from the one the CLI would have started, and the decision gets
// made there rather than noticed later.
type PaneSpawnParams struct {
	// Kind is what the pane is for. Required: a pane with no kind is a container
	// nobody can say the purpose of afterwards.
	Kind PaneKind `json:"kind"`

	// Agent is the adapter to run, empty for a plain command. Only agents with a
	// verified headless mode can be named, which is `internal/agents`' own rule.
	Agent string `json:"agent,omitempty"`

	// Prompt is what the agent is asked to do, and Argv the command for a
	// kind=command pane. One or the other, never both: an agent's argv is built by
	// its descriptor, and letting a caller supply one would be letting it choose
	// flags the descriptor deliberately does not pass.
	Prompt string   `json:"prompt,omitempty"`
	Argv   []string `json:"argv,omitempty"`

	// Worktree is the branch to work in. The pane runs in that worktree, created
	// if it does not exist — the same resolution `--worktree` does.
	Worktree string `json:"worktree,omitempty"`
	// Base is the branch the work is expected to land on, recorded as a label.
	Base string `json:"base,omitempty"`

	// Share mounts the shared directory at /shared. A boolean, never a path: one
	// well-known directory the daemon creates and vets, rather than a caller
	// naming what the container reaches.
	Share bool `json:"share,omitempty"`
	// ShareName scopes that mount to a subdirectory, which avoids collisions and
	// is not an isolation boundary.
	ShareName string `json:"share_name,omitempty"`

	// Console keeps a terminal and an open stdin, so somebody can answer the
	// agent. SkipPermissions adds the agent's skip flag to a console run.
	Console         bool `json:"console,omitempty"`
	SkipPermissions bool `json:"skip_permissions,omitempty"`
	// Resume reopens a conversation by the agent's own session id. Requires
	// Console: a headless resume replays one prompt into an old conversation and
	// exits, which is not what anyone means by carrying it on.
	Resume string `json:"resume,omitempty"`

	// Verify is the task's definition of done, wrapped around the argv inside the
	// container and becoming its exit code.
	Verify string `json:"verify,omitempty"`

	// Memory and CPUs are caps. They can only narrow what the config allows, which
	// is checked rather than assumed.
	Memory string `json:"memory,omitempty"`
	CPUs   string `json:"cpus,omitempty"`

	// Allow adds egress domains. It can only *add* to the baseline — the same
	// asymmetry `fleet.yaml` has, and for the same reason: a caller that could
	// subtract would be asking for a narrower allowlist than the user configured,
	// and the way to want less egress is `network: none`.
	Allow []string `json:"allow,omitempty"`
	// Network is the posture, and may only tighten: "none" is accepted, and
	// anything that would loosen what the config has in force is refused.
	Network string `json:"network,omitempty"`

	// Git forwards the git identity and trusts the workspace.
	Git bool `json:"git,omitempty"`

	// DryRun builds the argv and starts nothing, so a caller can compare what this
	// socket would launch against what the CLI would.
	DryRun bool `json:"dry_run,omitempty"`
}

// PaneSpawnResult is the pane that was started, or the argv a dry run would have
// used.
type PaneSpawnResult struct {
	Pane *Pane `json:"pane,omitempty"`
	// Argv is set only for a dry run, and is the engine command — the same string
	// `--dry-run` prints, so the two can be compared as text.
	Argv []string `json:"argv,omitempty"`
}

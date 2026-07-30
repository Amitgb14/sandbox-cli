package studioapi

import "time"

// This file is the wire contract: every type here is what actually crosses the
// HTTP boundary, JSON-tagged the way a TypeScript client wants it (camelCase,
// omitempty on anything optional, no Go-only types). docs/studio-api/types.ts is
// a hand-maintained mirror for the frontend — keep the two in sync when this
// file changes.

// ErrorResponse is the body of every non-2xx response.
type ErrorResponse struct {
	Error string `json:"error"`
}

// HealthResponse answers "is the control plane usable right now".
type HealthResponse struct {
	Status          string `json:"status"` // "ok" | "degraded"
	Version         string `json:"version"`
	Engine          string `json:"engine"` // "docker" | "podman"
	DockerAvailable bool   `json:"dockerAvailable"`
	Project         string `json:"project"` // the host directory this server manages
	Profile         string `json:"profile"` // "dev" | "prod"
}

// AgentInfo describes one agent adapter sandbox-cli knows how to launch
// headlessly. Only agents with a verified non-interactive mode are ever listed —
// see internal/agents' package doc — because a Studio-launched run is always
// detached, and an agent that stops to ask permission would just hang.
type AgentInfo struct {
	Name       string   `json:"name"`
	PersistDir string   `json:"persistDir"`
	EnvAllow   []string `json:"envAllow"`
}

// AgentsResponse is the body of GET /agents.
type AgentsResponse struct {
	Agents []AgentInfo `json:"agents"`
}

// RunKind separates a fleet task from a run someone (or Studio) started directly
// — the same distinction `sandbox-cli list`'s KIND column makes, carried into the
// API for the same reason: a client deciding what it may stop or reap needs it.
type RunKind string

const (
	RunKindInteractive RunKind = "interactive"
	RunKindFleet       RunKind = "fleet"
)

// RunState mirrors the docker container states runtime.ContainerInfo reports,
// spelled out as a closed set so a TypeScript client can switch over them
// exhaustively instead of guessing at docker's vocabulary.
type RunState string

const (
	RunStateCreated    RunState = "created"
	RunStateRunning    RunState = "running"
	RunStatePaused     RunState = "paused"
	RunStateRestarting RunState = "restarting"
	RunStateExited     RunState = "exited"
	RunStateDead       RunState = "dead"
	RunStateUnknown    RunState = "unknown"
)

// Run is a container sandbox-cli started, addressed the same way
// `sandbox-cli list`/`kill`/`logs` addresses one: by id, name, or branch. It is
// assembled from runtime.ContainerInfo plus the sandbox.* labels stamped on the
// container — never re-derived, since docker is the state store.
type Run struct {
	ID          string   `json:"id"`          // short id (12 chars) — what the rest of the API accepts back
	ContainerID string   `json:"containerId"` // full id
	Name        string   `json:"name"`
	Kind        RunKind  `json:"kind"`
	State       RunState `json:"state"`
	ExitCode    *int     `json:"exitCode,omitempty"` // set once State is "exited"
	Detached    bool     `json:"detached"`

	Repo   string `json:"repo,omitempty"`
	Branch string `json:"branch,omitempty"`
	Base   string `json:"base,omitempty"`
	Agent  string `json:"agent,omitempty"`
	Verify string `json:"verify,omitempty"`

	CreatedAt  time.Time  `json:"createdAt"`
	StartedAt  *time.Time `json:"startedAt,omitempty"`
	FinishedAt *time.Time `json:"finishedAt,omitempty"`

	// OpenStdin/TTY say what `attach` could do with this run — see
	// runtime.ContainerInfo. A Studio-launched run always has both false: it is
	// always detached.
	OpenStdin bool `json:"openStdin"`
	TTY       bool `json:"tty"`
}

// RunsResponse is the body of GET /runs.
type RunsResponse struct {
	Runs []Run `json:"runs"`
}

// RunListQuery documents the GET /runs query parameters (not itself
// marshalled — parsed from the URL): all=1 to include finished runs (default:
// live only, matching `sandbox-cli list`), repo=, branch=, agent= to filter by
// label, fleet=1 to show only fleet-launched runs.

// RunCreateRequest is the body of POST /runs.
//
// It always launches detached: an HTTP request/response cycle has nowhere to
// hold an interactive terminal, so unlike the CLI's `run` there is no foreground
// mode here — every Studio run is what `sandbox-cli run --detach` or a fleet task
// would produce, never given `-it`.
type RunCreateRequest struct {
	// Project is a host directory to mount at /workspace. Defaults to the
	// server's configured project root. Mutually exclusive with Worktree.
	Project string `json:"project,omitempty"`

	// Worktree, when set, resolves (creating if needed) a git worktree for this
	// branch under sandbox-cli's managed worktree directory and mounts *that* as
	// the workspace instead — the same mechanism `--worktree` and `fleet` use, so
	// several runs can work in parallel without colliding. Branch defaults to
	// this value when Branch is empty.
	Worktree string `json:"worktree,omitempty"`

	// Agent is one of the names from GET /agents. Required unless Command is set.
	// When set, Prompt is run through the agent's autonomous/headless mode —
	// there is no interactive agent mode over this API.
	Agent  string `json:"agent,omitempty"`
	Prompt string `json:"prompt,omitempty"`

	// Command is a plain guest argv, for a run with no agent (mutually exclusive
	// with Agent).
	Command []string `json:"command,omitempty"`

	// Branch/Base become the sandbox.branch/sandbox.base labels. Branch defaults
	// to Worktree (or the project's current git branch) when empty.
	Branch string `json:"branch,omitempty"`
	Base   string `json:"base,omitempty"`

	// Verify is a shell command run after the agent; its exit code becomes the
	// container's, same as a fleet task's `verify:`.
	Verify string `json:"verify,omitempty"`

	Image  string `json:"image,omitempty"`
	Memory string `json:"memory,omitempty"`
	CPUs   string `json:"cpus,omitempty"`

	// Allow adds egress domains and switches the allowlist on for this run, same
	// as --allow.
	Allow []string `json:"allow,omitempty"`

	// Env sets literal KEY=VALUE pairs in the container. Reserved control
	// variables (config.IsReservedEnv) are refused, same as the CLI.
	Env map[string]string `json:"env,omitempty"`
}

// RunStopRequest is the body of POST /runs/:id/stop.
type RunStopRequest struct {
	// Force kills immediately (SIGKILL) instead of asking the guest to exit
	// first. Same distinction as `sandbox-cli kill --force`.
	Force bool `json:"force,omitempty"`
}

// RestoreMode selects what a recover call does with the recovered snapshot —
// mirrors rescue.RestoreMode.
type RestoreMode string

const (
	// RestoreModeBranch points a new branch at the snapshot. The default: it is
	// the only mode that cannot destroy anything already on disk.
	RestoreModeBranch RestoreMode = "branch"
	// RestoreModePatch returns the snapshot as a unified diff instead of touching
	// any branch or working tree.
	RestoreModePatch RestoreMode = "patch"
	// RestoreModeWorktree writes the snapshot's files back into the run's
	// workspace. Refused if that workspace has uncommitted changes.
	RestoreModeWorktree RestoreMode = "worktree"
)

// RunRecoverRequest is the body of POST /runs/:id/recover.
//
// It restores the crash-recovery snapshot (internal/rescue) most recently
// associated with this run's workspace — the same correlation
// `sandbox-cli recover` performs by agent, project and time window. There may be
// none: a run that finished cleanly, or one that never wrote a snapshot, has
// nothing to recover.
type RunRecoverRequest struct {
	Mode RestoreMode `json:"mode,omitempty"` // default RestoreModeBranch
	// Branch overrides the generated branch name (RestoreModeBranch only).
	Branch string `json:"branch,omitempty"`
}

// RunRecoverResponse is the body of a successful POST /runs/:id/recover.
type RunRecoverResponse struct {
	SessionID string      `json:"sessionId"` // the rescue.Session this snapshot came from
	Mode      RestoreMode `json:"mode"`
	Branch    string      `json:"branch,omitempty"` // set for RestoreModeBranch
	Patch     string      `json:"patch,omitempty"`  // the diff text, for RestoreModePatch
	Files     int         `json:"files"`

	// MatchesWorkingTree reports that the workspace on disk already holds what
	// the snapshot holds — the common case, since /workspace is a bind mount and
	// the snapshot is the belt, not the braces. See rescue.RestoreResult.
	MatchesWorkingTree bool `json:"matchesWorkingTree"`
}

// LogEventType discriminates a LogEvent. A client switching on it exhaustively
// knows the difference between "the run's output ended" and "the connection
// did", which is the one thing a log viewer must not guess: an incomplete
// stream that renders as a complete one is how a half-finished agent run reads
// as a finished one.
type LogEventType string

const (
	LogEventLog LogEventType = "log"
	// LogEventError carries a failure of the stream itself (docker unreachable
	// mid-follow, say), not anything the container printed to stderr.
	LogEventError LogEventType = "error"
	// LogEventEnd is the last event of a stream that finished on its own terms.
	LogEventEnd LogEventType = "end"
)

// Stream names for LogEvent.Stream.
const (
	StreamStdout = "stdout"
	StreamStderr = "stderr"
)

// LogEvent is one event of GET /runs/:id/logs, identical on both transports: a
// WebSocket text frame carries exactly this object, and an SSE `data:` line
// carries exactly this object with `event:` repeating its Type.
type LogEvent struct {
	Type   LogEventType `json:"type"`
	Stream string       `json:"stream,omitempty"` // "stdout" | "stderr", on Type "log"
	// Data is one line with its newline stripped, and is deliberately *not*
	// omitempty: a blank line is ordinary log output, and omitting the field for
	// it would make `data` optional in the contract — forcing every consumer to
	// coalesce a missing string on the hottest path in a log viewer.
	Data  string `json:"data"`
	Error string `json:"error,omitempty"` // on Type "error"
}

// RunMetrics is a single point-in-time resource sample for one run — the same
// numbers the CLI's live gauge and `sandbox-cli stats` sample from `docker
// stats`, as parsed numbers rather than docker's formatted strings.
type RunMetrics struct {
	ID            string    `json:"id"`
	MemUsageBytes int64     `json:"memUsageBytes"`
	MemLimitBytes int64     `json:"memLimitBytes,omitempty"`
	MemPercent    float64   `json:"memPercent"`
	CPUPercent    float64   `json:"cpuPercent"`
	PIDs          int       `json:"pids"`
	SampledAt     time.Time `json:"sampledAt"`
}

// StatsResponse is the body of GET /stats: one sample per live sandbox
// container, host-wide (the API equivalent of `sandbox-cli stats --once`).
type StatsResponse struct {
	Runs      []RunMetrics `json:"runs"`
	SampledAt time.Time    `json:"sampledAt"`
}

// Worktree describes one git worktree sandbox-cli manages for the project.
type Worktree struct {
	Branch     string   `json:"branch"`
	Path       string   `json:"path"`
	Dirty      []string `json:"dirty,omitempty"` // modified/untracked paths
	DirtyCount int      `json:"dirtyCount"`
}

// WorktreesResponse is the body of GET /worktrees.
type WorktreesResponse struct {
	Worktrees []Worktree `json:"worktrees"`
}

// WorktreeCreateRequest is the body of POST /worktrees.
type WorktreeCreateRequest struct {
	Branch string `json:"branch"`
}

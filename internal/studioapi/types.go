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
	EngineVersion   string `json:"engineVersion"`
	DockerAvailable bool   `json:"dockerAvailable"`
	Project         string `json:"project"` // the host directory this server manages
	Profile         string `json:"profile"` // "dev" | "prod"

	// Host is what this machine is, as the engine and the Go runtime report it.
	// Always present: a client showing "where am I running" has nowhere to put
	// an absent object, and the zero values are honest — 0 bytes means the
	// engine would not say, which is the same answer `fleet` accepts when it
	// cannot size the host.
	Host HostInfo `json:"host"`
}

// HostInfo is the daemon's view of the machine it runs on.
type HostInfo struct {
	OS       string `json:"os"`
	Arch     string `json:"arch"`
	CPUs     int    `json:"cpus"`
	MemBytes int64  `json:"memBytes"`
}

// AgentInfo describes one agent adapter sandbox-cli knows how to launch
// headlessly. Only agents with a verified non-interactive mode are ever listed —
// see internal/agents' package doc — because a Studio-launched run is always
// detached, and an agent that stops to ask permission would just hang.
type AgentInfo struct {
	Name       string   `json:"name"`
	Label      string   `json:"label"`
	PersistDir string   `json:"persistDir"`
	EnvAllow   []string `json:"envAllow"`

	// Env is what sandbox-cli itself sets in the container for this agent — the
	// keyring droid must not look for, and so on. Distinct from EnvAllow, which
	// is host values forwarded by *name* only if the host has them.
	Env []string `json:"env"`

	// HeadlessVerified is true for every agent listed here, and saying so
	// explicitly is not redundant: internal/agents only registers adapters with a
	// confirmed non-interactive argv (TestEveryAgentHasAVerifiedHeadlessArgv is
	// where that stops being a convention), because a Studio-launched run is
	// always detached and an agent that stops to ask permission does not fail —
	// it hangs. A client that cannot see the flag cannot warn about the agents
	// that would, so the field is sent rather than inferred from membership.
	HeadlessVerified bool `json:"headlessVerified"`

	// AutonomousInvocation is the argv a fleet task or a detached run would start
	// this agent with, prompt elided — the same string `fleet run --dry-run`
	// prints, so a launch preview and a dry run cannot disagree about what is
	// about to happen.
	AutonomousInvocation []string `json:"autonomousInvocation"`

	// Delivery is how the binary reaches the container. Four adapters are baked
	// into the base image and the rest are installed lazily into the persisted
	// HOME on first use — baking every adapter would put hundreds of megabytes in
	// front of every user for agents most will never run. This is a fact about
	// assets/Dockerfile rather than about the descriptor, which is why it is a
	// list here and cannot be read off agents.Descriptor.
	Delivery string `json:"delivery"` // "baked" | "npm"

	// The fields below are host-side, not from the descriptor — a descriptor
	// deliberately says only what runs inside the container and which host
	// variable *names* may cross. Studio runs on the host, so it can answer them.

	// Auth reports whether this agent has logged in yet: the sandbox-owned HOME
	// mounted for it, whether it exists, and when it last changed. Never its
	// contents — the persisted directory holds an OAuth refresh token.
	Auth AgentAuth `json:"auth"`

	// StatusLine and HistorySync are true for claude alone, and that is a
	// deliberate limit rather than an oversight: no other agent has a status-line
	// hook, and only claude mounts the host's per-project history bucket.
	StatusLine  bool `json:"statusLine"`
	HistorySync bool `json:"historySync"`

	// Sessions and ContextStore come from the persisted record of what has
	// actually been confirmed on this machine. An agent with no verified
	// descriptor is reported untracked rather than guessed at.
	Sessions     int    `json:"sessions"`
	ContextStore string `json:"contextStore"` // "verified" | "empty" | "missing" | "untracked"
}

// UsageWindow is one subscription window: how much of it is spent, and when it
// resets.
//
// Utilization is a pointer because absent and zero are different answers, and
// this is the field where confusing them matters most. A window past its reset
// has a cached figure describing the period that already ended, so it reports
// null — "we cannot honestly say" — rather than a number that is merely wrong.
type UsageWindow struct {
	Kind        string   `json:"kind"`  // "five_hour" | "seven_day"
	Label       string   `json:"label"` // for display: "5-hour", "Weekly"
	Utilization *float64 `json:"utilization"`
	ResetsAt    *string  `json:"resetsAt"`
	Scope       string   `json:"scope,omitempty"` // the model a per-model allowance covers
}

// UsageSnapshot is one reading of an agent's usage cache.
//
// FetchedAt is when the *agent* last refreshed these numbers from the server,
// not when this server read the file — and it is always sent, because these
// figures refresh only when the agent talks to the server and an unlabelled
// percentage can be hours stale.
type UsageSnapshot struct {
	Agent     string        `json:"agent"`
	Windows   []UsageWindow `json:"windows"`
	FetchedAt *string       `json:"fetchedAt"`
	Path      *string       `json:"path"`
}

// DoctorCheck is one host property, as `sandbox-cli doctor` reports it.
//
// UnderDev and UnderProd both travel because the same fact means different
// things to the two profiles — a control the host cannot provide warns under
// dev and refuses under prod — and a reader deciding whether this machine is
// ready for unattended work should not have to switch profiles and ask again.
type DoctorCheck struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Result    string `json:"result"` // "pass" | "warn" | "fail" | "unknown"
	Detail    string `json:"detail"`
	Remedy    string `json:"remedy,omitempty"`
	UnderDev  string `json:"underDev"`  // "warn" | "fail"
	UnderProd string `json:"underProd"` // "warn" | "fail"
}

// DoctorResponse is the body of GET /v1/doctor.
type DoctorResponse struct {
	Profile string        `json:"profile"`
	Checks  []DoctorCheck `json:"checks"`
}

// AgentAuth is where an agent's login is persisted, and whether it is there yet.
type AgentAuth struct {
	Persisted bool   `json:"persisted"`
	Path      string `json:"path"`
	LastSeen  string `json:"lastSeen,omitempty"` // RFC3339, or absent when never
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

	// What the run actually was, read back off the container rather than
	// re-derived from a config file that may have been edited since. The labels
	// above say what the launcher *intended*; these say what docker gave it,
	// which is the question someone reviewing a finished run is asking.
	Image      string      `json:"image"`
	Command    []string    `json:"command"`
	Workdir    string      `json:"workdir"`
	Workspace  string      `json:"workspace"` // the host path mounted at /workspace
	Engine     string      `json:"engine"`
	DurationMS *int64      `json:"durationMs"`
	Mounts     []RunMount  `json:"mounts"`
	Network    RunNetwork  `json:"network"`
	Security   RunSecurity `json:"security"`

	// EnvNames is names only, never values. The credential broker exists to keep
	// secret values off the argv and out of config files; an API response is one
	// more file, in a browser's cache. internal/audit makes the same trade and
	// has nowhere to put a value on purpose.
	EnvNames []string `json:"envNames"`
}

// RunMount is one host path a run could reach.
type RunMount struct {
	Source      string `json:"source"`
	Destination string `json:"destination"`
	ReadWrite   bool   `json:"readWrite"`
}

// RunNetwork is the egress posture this container actually ran with.
//
// Read from the container: Mode is docker's own network mode, and Allowlisted
// comes from the control variable the entrypoint acts on, so a run that asked
// for an allowlist and got one is distinguishable from one that did not.
type RunNetwork struct {
	Mode        string `json:"mode"`
	Allowlisted bool   `json:"allowlisted"`
}

// RunSecurity is the confinement docker applied, read back rather than assumed.
// Zero means unset for the numeric fields — not "zero bytes of memory".
type RunSecurity struct {
	CapDrop     []string `json:"capDrop"`
	CapAdd      []string `json:"capAdd"`
	SecurityOpt []string `json:"securityOpt"`
	PidsLimit   int64    `json:"pidsLimit"`
	MemoryBytes int64    `json:"memoryBytes"`
	NanoCPUs    int64    `json:"nanoCpus"`
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

// LogEvent is one line streamed from GET /runs/:id/logs (SSE `event: log`).
type LogEvent struct {
	Stream string `json:"stream"` // "stdout" | "stderr"
	Data   string `json:"data"`
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
	Branch string `json:"branch"`
	Path   string `json:"path"`

	// Dirty is the modified/untracked paths, and is always present — never
	// `omitempty`. A clean worktree is the common case, and omitting the field
	// for it sent no key at all, which every client then reads as `undefined`
	// rather than as "nothing dirty". A list-valued field that vanishes when
	// empty makes the ordinary case the one that crashes.
	Dirty      []string `json:"dirty"`
	DirtyCount int      `json:"dirtyCount"`

	Head   string `json:"head"`   // the abbreviated commit this branch points at
	RepoID string `json:"repoId"` // the id every container of this project is labelled with

	// Ahead and Behind are counted against Base. "3 ahead" says there is
	// something to land; "3 ahead, 40 behind" says landing it will be a merge.
	Ahead  int `json:"ahead"`
	Behind int `json:"behind"`

	// Base is the branch this work is meant to land on, taken from the label the
	// launching run stamped rather than from whatever is checked out now — the
	// label is the intent, and `land` treats a disagreement between the two as a
	// refusal rather than a preference. Null when nothing recorded one.
	Base *string `json:"base"`

	// RunID is the run currently working this branch, if one is live.
	RunID *string `json:"runId"`

	// Verified is what the branch's last run said about its own definition of
	// done: true if it passed, false if it failed or died before reaching its
	// verify, and **null when nothing checked it** — no container left to ask, or
	// a run that declared no verify at all. Null is not false. `land` refuses a
	// branch that never passed, so a client showing "unverified" and "failed" the
	// same way would be misreporting the one distinction that decides the merge.
	Verified *bool `json:"verified"`

	// CreatedAt is when the checkout appeared on disk. git records no creation
	// time for a worktree, so this is the directory's own — accurate for the
	// managed worktrees this lists, all of which sandbox-cli created.
	CreatedAt string `json:"createdAt"`
}

// WorktreesResponse is the body of GET /worktrees.
type WorktreesResponse struct {
	Worktrees []Worktree `json:"worktrees"`
}

// WorktreeCreateRequest is the body of POST /worktrees.
type WorktreeCreateRequest struct {
	Branch string `json:"branch"`
}

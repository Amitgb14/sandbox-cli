package studioapi

import (
	"github.com/Amitgb14/sandbox-cli/internal/agentctx"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/history"
)

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

	// AuthRequired reports whether this daemon was started with a -token, so a
	// client can say "you need the token" instead of failing every request with
	// a 401 it cannot explain.
	//
	// Health is the one endpoint that answers unauthenticated, which is exactly
	// why the fact belongs here: it is the only thing a client without a token
	// can still ask. It reports *that* a token is required, never any part of
	// the token itself.
	AuthRequired bool `json:"authRequired"`

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

	// CanSkipPermissions is whether this agent's approval prompts can be turned
	// off with a *flag*, which is what an interactive run would need. False for
	// the agents whose non-interactive mode is a subcommand instead (`codex
	// exec`, `opencode run`, `droid exec`): there is nothing to add to a console
	// session, and a control that silently did nothing would be worse than one
	// that is not offered.
	CanSkipPermissions bool `json:"canSkipPermissions"`

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

	// Active is whether the agent reported this window as the one currently in
	// force. Null when it said nothing — a window described only by the
	// five_hour/seven_day fields carries no such flag, and rendering "not in
	// force" from a missing one would state the absence of a field as a fact.
	Active *bool `json:"active"`
}

// UsageSnapshot is one reading of an agent's usage cache.
//
// FetchedAt is when the *agent* last refreshed these numbers from the server,
// not when this server read the file — and it is always sent, because these
// figures refresh only when the agent talks to the server and an unlabelled
// percentage can be hours stale.
type UsageSnapshot struct {
	Agent   string        `json:"agent"`
	Windows []UsageWindow `json:"windows"`

	// CanRefresh is whether the agent that owns this cache is on this machine's
	// PATH. The figures are readable without it — they come from a file, and the
	// sandbox keeps its own copy — so "there are numbers" and "they can be made
	// current" are different questions, and a client that offered a refresh it
	// cannot perform would be answering the second with the first.
	CanRefresh bool    `json:"canRefresh"`
	FetchedAt  *string `json:"fetchedAt"`
	Path       *string `json:"path"`
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

	// Profile is the posture the run was launched under, and Prompt what the
	// agent was asked to do. Both are read from labels stamped at launch: a
	// container says what confinement it got but not which profile chose it, and
	// a prompt otherwise survives only inside an agent-specific argv. Absent for
	// containers started before either label existed, which is why both omit
	// rather than send an empty string.
	Profile string `json:"profile,omitempty"`
	Prompt  string `json:"prompt,omitempty"`

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

// RunMount is one host path a run could reach. Named host/container/mode rather
// than docker's source/destination/rw because that is the vocabulary the rest of
// sandbox-cli uses — a `mounts:` entry in a config file reads the same way.
type RunMount struct {
	Host      string `json:"host"`
	Container string `json:"container"`
	Mode      string `json:"mode"` // "ro" | "rw"
	Origin    string `json:"origin,omitempty"`
}

// RunNetwork is the egress posture this container actually ran with, read back
// off the container rather than from the config that asked for it.
//
// Allow is the *resolved* list — baseline ∪ configured — because that is what
// the entrypoint was handed and therefore what the firewall and proxy actually
// enforce. Baseline says whether the built-in set is part of it, which is the
// difference between "these nine hosts plus mine" and "only mine".
type RunNetwork struct {
	Mode        string   `json:"mode"` // "default" | "none" | "allowlist"
	Baseline    bool     `json:"baseline"`
	Allow       []string `json:"allow"`
	NetworkName string   `json:"networkName,omitempty"`

	// Enforcement names how the allowlist is applied, and null when there is no
	// allowlist at all. "name" means the in-container proxy decided on the
	// hostname; "address" would mean IP rules alone, which cannot tell two hosts
	// sharing an address apart.
	Enforcement  *string `json:"enforcement"`
	IngressPorts []int   `json:"ingressPorts,omitempty"`
}

// RunSecurity is the confinement docker applied, read back rather than assumed.
type RunSecurity struct {
	NoNewPrivileges bool     `json:"noNewPrivileges"`
	CapDrop         []string `json:"capDrop"`
	CapAdd          []string `json:"capAdd"`
	PidsLimit       int64    `json:"pidsLimit"`

	// Memory and CPUs are the strings a config file would carry ("2g", "1.5"),
	// empty when unlimited — not "0", which reads as a limit of nothing.
	Memory string `json:"memory"`
	CPUs   string `json:"cpus"`

	Seccomp string `json:"seccomp"`
	User    string `json:"user"`

	// Hardening is whether the confinement this tool applies by default is
	// actually in force, so a client can say "this run was hardened" without
	// re-deriving the rule from four fields.
	Hardening bool `json:"hardening"`
}

// LogLine is one line of a run's output, as GET /runs/{id}/logs returns it
// without follow.
//
// Stream is kept rather than merged because which one a line came from is how a
// reader tells the agent's own output from the egress proxy's DENY lines
// interleaved with it — and TS is empty when docker recorded none, not a
// substituted "now", since a log's value is that it says what happened when.
type LogLine struct {
	Seq    int    `json:"seq"`
	TS     string `json:"ts"`
	Stream string `json:"stream"` // "stdout" | "stderr"
	Text   string `json:"text"`
}

// MetricSample is one reading of a running container's resource use, in the
// units a chart wants: bytes and percentages, with the time it was taken.
type MetricSample struct {
	T               string  `json:"t"`
	CPUPct          float64 `json:"cpuPct"`
	MemBytes        int64   `json:"memBytes"`
	MemLimitBytes   int64   `json:"memLimitBytes"` // 0 means unlimited
	NetRxBytes      int64   `json:"netRxBytes"`
	NetTxBytes      int64   `json:"netTxBytes"`
	BlockReadBytes  int64   `json:"blockReadBytes"`
	BlockWriteBytes int64   `json:"blockWriteBytes"`
	PIDs            int     `json:"pids"`
}

// MetricSeries is the body of GET /runs/{id}/metrics.
//
// A series of one, for now: docker reports what a container is using *now* and
// keeps no history, so anything longer has to be accumulated by a client that
// stays connected to ?stream=1. Shaped as a series anyway because that is what
// the reading is — a point on a chart — and a client should not have to change
// its type when a second point arrives.
type MetricSeries struct {
	RunID   string         `json:"runId"`
	Samples []MetricSample `json:"samples"`
	Peak    MetricPeak     `json:"peak"`
}

// MetricPeak is the high-water mark over the samples, which is what the CLI's
// footer summary prints when a run ends.
type MetricPeak struct {
	CPUPct   float64 `json:"cpuPct"`
	MemBytes int64   `json:"memBytes"`
}

// AuditRecord is one line of the run log: what ran, how it was confined, and how
// it ended.
//
// EnvNames carries names and never values, which is the rule the log itself
// keeps — the credential broker exists so secret values stay off the argv and
// out of files, and this is one more file.
type AuditRecord struct {
	Time        string   `json:"time"`
	Image       string   `json:"image"`
	Workspace   string   `json:"workspace"`
	Workdir     string   `json:"workdir"`
	Agent       *string  `json:"agent"`
	Branch      *string  `json:"branch"`
	Command     []string `json:"command"`
	Engine      string   `json:"engine"`
	Network     string   `json:"network"`
	NetworkName string   `json:"networkName"`

	// EgressEnforcementRequested is named for a *request* rather than an
	// outcome, because that is all the host can honestly know: the container
	// programs its own firewall, and this says what it was asked to do.
	EgressEnforcementRequested *string  `json:"egressEnforcementRequested"`
	EgressAllow                []string `json:"egressAllow"`
	EnvNames                   []string `json:"envNames"`

	ExitCode   int   `json:"exitCode"`
	DurationMS int64 `json:"durationMs"`
	Detached   bool  `json:"detached"`
}

// HistoryStatsResponse is the body of GET /v1/stats/history: what the run log
// says about outcomes, aggregated in the index rather than in the client.
type HistoryStatsResponse struct {
	Stats history.Stats       `json:"stats"`
	Days  []history.DayBucket `json:"days"`
}

// AuditResponse is the body of GET /v1/audit.
type AuditResponse struct {
	Records []AuditRecord `json:"records"`
}

// DiffFile is one file's change in a run's work.
//
// Hunks are empty for now and that is stated rather than hidden: this reports
// *what* changed and by how much, which is the question a reviewer asks first,
// and rendering the content is a second call the client can make against git
// itself. An empty list is not a claim that the file has no content.
type DiffFile struct {
	Path         string     `json:"path"`
	PreviousPath string     `json:"previousPath,omitempty"`
	Status       string     `json:"status"` // "added" | "modified" | "deleted" | "renamed"
	Insertions   int        `json:"insertions"`
	Deletions    int        `json:"deletions"`
	Binary       bool       `json:"binary,omitempty"`
	Hunks        []DiffHunk `json:"hunks"`
}

// DiffHunk is a contiguous run of changed lines.
type DiffHunk struct {
	Header string     `json:"header"`
	Lines  []DiffLine `json:"lines"`
}

// DiffLine is one line of a hunk.
type DiffLine struct {
	Kind    string `json:"kind"` // "add" | "del" | "ctx" | "meta"
	OldNo   *int   `json:"oldNo"`
	NewNo   *int   `json:"newNo"`
	Content string `json:"content"`
}

// ResolvedConfig is the configuration a run actually got, read off its
// container.
type ResolvedConfig struct {
	Profile  string      `json:"profile"`
	Image    string      `json:"image"`
	Workdir  string      `json:"workdir"`
	User     string      `json:"user"`
	Home     string      `json:"home"`
	Engine   string      `json:"engine"`
	Network  RunNetwork  `json:"network"`
	Security RunSecurity `json:"security"`
	Mounts   []RunMount  `json:"mounts"`
	EnvAllow []string    `json:"envAllow"`

	PersistAuth bool `json:"persistAuth"`
	Sync        bool `json:"sync"`

	// Fields is the layered provenance — which of default/user/project/flag
	// supplied each value. Empty here, deliberately: a container records the
	// resolved answer and not the layers behind it, and a guessed layer is worse
	// than none when the entire point of the view is to say where a value came
	// from.
	Fields []ResolvedField `json:"fields"`

	// Argv is what the container was started with. Display only.
	Argv []string `json:"argv"`
}

// ResolvedField is one setting and the layer that supplied it.
type ResolvedField struct {
	Key         string `json:"key"`
	Value       string `json:"value"`
	Layer       string `json:"layer"`
	RefusedFrom string `json:"refusedFrom,omitempty"`
}

// Commit is one commit on a branch.
//
// Subject and Author are text from the repository, exactly like a branch name:
// render them, never interpret them.
type Commit struct {
	SHA        string `json:"sha"`
	ShortSHA   string `json:"shortSha"`
	Subject    string `json:"subject"`
	Author     string `json:"author"`
	Date       string `json:"date"`
	Files      int    `json:"files"`
	Insertions int    `json:"insertions"`
	Deletions  int    `json:"deletions"`
}

// CommitsResponse is the body of GET /v1/worktrees/{branch}/commits.
type CommitsResponse struct {
	Base    string   `json:"base"`
	Commits []Commit `json:"commits"`
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
// would produce. The one variation is Console, which asks for a run somebody
// intends to attach to and type at; it changes the agent's argv and the
// container's stdin, and nothing about what either can reach.
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
	// When set, Prompt is run through the agent's autonomous/headless mode,
	// unless Console asks for the interactive one.
	Agent  string `json:"agent,omitempty"`
	Prompt string `json:"prompt,omitempty"`

	// Console starts the agent in its *interactive* mode on a container that
	// keeps a pty and stdin open, so `sandbox-cli attach` from any terminal can
	// answer it. Prompt, when set, seeds the first turn instead of being the
	// whole run.
	//
	// It is one field for both halves deliberately. A console without the
	// interactive argv is a keyboard wired to a headless agent that will never
	// ask anything; the interactive argv without a console is an agent waiting on
	// stdin that does not exist. Neither half is useful alone, so neither is
	// separately requestable.
	//
	// Refused with Verify: verify's exit code is the answer it exists to give,
	// and an interactive session's exit code is whenever the person quit.
	Console bool `json:"console,omitempty"`

	// SkipPermissions turns off the agent's approval prompts on a console run.
	//
	// Headless runs always have it — an agent that stops to ask does not fail,
	// it hangs — but a console run is one somebody is attached to, where being
	// asked is the point. So it is opt-in here, for the case where you want to
	// watch a run that does not wait for you. The container is the blast-radius
	// boundary either way; this changes what the agent asks, not what it can
	// reach.
	//
	// Only meaningful for agents whose non-interactive mode is a flag rather
	// than a subcommand (claude, gemini).
	SkipPermissions bool `json:"skipPermissions,omitempty"`

	// Resume carries on an existing conversation instead of starting one, by
	// the agent's own session id. Requires Console: resuming is something you
	// do interactively, and a headless resume would replay one prompt into an
	// old conversation and exit.
	Resume string `json:"resume,omitempty"`

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

// ConversationResponse is what a run said, and whether it can be answered.
type ConversationResponse struct {
	Messages []agentctx.Message `json:"messages"`

	// Writable reports whether this run can be typed at right now: it is running
	// *and* was launched with a console. Sent rather than inferred client-side,
	// because the two facts that decide it (container state, how stdin was
	// created) both live here.
	Writable bool `json:"writable"`

	// SessionID is the agent's own id for this conversation, whole rather than
	// abbreviated — Claude Code rejects anything that is not a complete UUID.
	SessionID string `json:"sessionId,omitempty"`

	// Resume is the exact line to type on the host to carry the conversation on
	// after the container is gone. Built here rather than by a client, because
	// the flags that make it work are not guessable from the id.
	Resume string `json:"resume,omitempty"`
}

// ConsoleInputRequest is one delivery of keystrokes to a run's stdin.
type ConsoleInputRequest struct {
	Data string `json:"data"`

	// Enter appends the carriage return that submits it. Separate from Data so a
	// client can send a partial line — and because \r is not what a caller would
	// guess: the pty is in raw mode, where a \n arrives as a literal line feed
	// and the agent's input box simply holds it.
	Enter bool `json:"enter,omitempty"`
}

// ConsoleResizeRequest is a terminal's dimensions, in character cells.
type ConsoleResizeRequest struct {
	Rows int `json:"rows"`
	Cols int `json:"cols"`
}

// SessionSummary is one resumable conversation.
type SessionSummary struct {
	ID       string    `json:"id"`
	Title    string    `json:"title,omitempty"`
	Turns    int       `json:"turns"`
	Modified time.Time `json:"modified"`
	// Partial marks a session listed from its file alone, because there is no
	// verified reader for this agent's transcript format. Its id and dates are
	// real; the title and turn count are unknown.
	Partial bool `json:"partial,omitempty"`
}

type SessionListResponse struct {
	Sessions []SessionSummary `json:"sessions"`
}

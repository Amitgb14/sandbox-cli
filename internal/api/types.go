// Package api is the Sandbox API v1: its wire types, a Go client, and — in
// api/conformance — the suite that defines what every endpoint must do.
//
// The contract is docs/api/v1.md. These types are that document in Go; when the
// two disagree, the document is fixed first and the types follow.
package api

import "time"

// Version is the API version this package speaks.
const Version = "v1"

// Sandbox states. Terminated is final.
const (
	StatePending    = "pending"
	StateRunning    = "running"
	StateSuspended  = "suspended"
	StateTerminated = "terminated"
)

// Network modes, loosest last. The order is the "tighten, never loosen" rule's
// whole vocabulary: a request may move left of the server's ceiling, never right.
const (
	NetworkNone      = "none"
	NetworkAllowlist = "allowlist"
	NetworkOpen      = "open"
)

// NetworkRank orders modes from strictest (0) to loosest; -1 for an unknown one.
func NetworkRank(mode string) int {
	switch mode {
	case NetworkNone:
		return 0
	case NetworkAllowlist:
		return 1
	case NetworkOpen:
		return 2
	}
	return -1
}

// NetworkPolicy is a sandbox's egress policy.
//
// Allow has no omitempty, on purpose: in a request, an absent (null) list means
// "the server's default" while an empty one means "nothing" — which is refused
// for allowlist mode. omitempty would collapse the second into the first, turning
// the strictest request into the default one without a word.
type NetworkPolicy struct {
	Mode  string   `json:"mode"`
	Allow []string `json:"allow"`
	Deny  []string `json:"deny,omitempty"`
}

// Capabilities is what an endpoint can do. A client asks rather than assumes.
type Capabilities struct {
	APIVersion   string          `json:"api_version"`
	Backend      string          `json:"backend"`
	Capabilities map[string]bool `json:"capabilities"`
	Limits       Limits          `json:"limits"`
	Network      NetworkCeiling  `json:"network"`
}

// Has reports whether the endpoint has the named capability.
func (c Capabilities) Has(name string) bool { return c.Capabilities[name] }

// Capability names.
const (
	CapNetworkPolicyUpdate = "network_policy_update"
	CapSuspend             = "suspend"
	CapMemorySnapshot      = "memory_snapshot"
	CapBindWorkspace       = "bind_workspace"
	// CapEgressAllowlist: network mode allowlist is enforced. Without it, the
	// endpoint offers only none — a sandbox there has no network at all.
	CapEgressAllowlist = "egress_allowlist"
	// CapEgressOpen: network mode open is available.
	CapEgressOpen = "egress_open"
	// CapWorkspaceBundle: a git bundle can be cloned in and brought back out.
	CapWorkspaceBundle = "workspace_bundle"
	// CapTunnel: a TCP port on the guest's loopback can be reached through the API.
	CapTunnel = "tunnel"
	// CapAudit: the server keeps an event log, served per sandbox at
	// GET /v1/sandboxes/{ref}/events. A property of the server, not the backend.
	CapAudit = "audit"
)

// Limits are the largest resources a sandbox may ask for.
type Limits struct {
	MaxCPUs     float64 `json:"max_cpus"`
	MaxMemoryMB int     `json:"max_memory_mb"`
	MaxDiskMB   int     `json:"max_disk_mb"`
	// MaxIdleTimeoutSecs bounds idle_timeout_secs; 0 means no bound.
	MaxIdleTimeoutSecs int `json:"max_idle_timeout_secs"`
}

// NetworkCeiling is the server's network policy floor and ceiling, as a client
// sees it.
type NetworkCeiling struct {
	Default  NetworkPolicy `json:"default"`
	Ceiling  string        `json:"ceiling"`
	MayAllow []string      `json:"may_allow"`
}

// CreateSandboxRequest creates a sandbox. Every field is optional.
type CreateSandboxRequest struct {
	Name     string            `json:"name,omitempty"`
	Image    string            `json:"image,omitempty"`
	CPUs     float64           `json:"cpus,omitempty"`
	MemoryMB int               `json:"memory_mb,omitempty"`
	DiskMB   int               `json:"disk_mb,omitempty"`
	Env      map[string]string `json:"env,omitempty"`
	Network  *NetworkPolicy    `json:"network,omitempty"`
	// IdleTimeoutSecs terminates the sandbox after this long with no request
	// touching it and no process running. 0 takes the server's default.
	IdleTimeoutSecs int `json:"idle_timeout_secs,omitempty"`
	// SnapshotID starts the sandbox from a snapshot (capability
	// memory_snapshot): memory, processes and disk as they were captured. Its
	// image and resources are the snapshot's.
	SnapshotID string `json:"snapshot_id,omitempty"`
	// Bind mounts a host directory at /workspace instead of starting empty.
	// Local endpoints only (capability bind_workspace, and the operator's
	// allow_bind); the host path is refused if it is /, the home directory or
	// an ancestor of it.
	Bind *Bind `json:"bind,omitempty"`
	// Labels are the client's own metadata: up to 32 keys of lowercase letters,
	// digits and . _ / -, values up to 256 printable bytes. They decide nothing
	// about the sandbox; they are returned with it, filter the listing
	// (?label=k=v), and are recorded in its audit events, so a client can say
	// why a sandbox exists — which agent, which fleet task, which retry.
	Labels map[string]string `json:"labels,omitempty"`
}

// Bind is a host directory mounted at /workspace.
type Bind struct {
	HostPath string `json:"host_path"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

// UpdateSandboxRequest changes a running sandbox.
type UpdateSandboxRequest struct {
	Network *NetworkPolicy `json:"network,omitempty"`
}

// Sandbox is a sandbox as the API reports it. Environment values are never
// included — only their names — because this object is what dashboards and logs
// print.
type Sandbox struct {
	ID        string        `json:"id"`
	Name      string        `json:"name,omitempty"`
	State     string        `json:"state"`
	Image     string        `json:"image"`
	CPUs      float64       `json:"cpus"`
	MemoryMB  int           `json:"memory_mb"`
	DiskMB    int           `json:"disk_mb"`
	EnvNames  []string      `json:"env_names,omitempty"`
	Network   NetworkPolicy `json:"network"`
	CreatedAt time.Time     `json:"created_at"`
	// IdleTimeoutSecs is in force for this sandbox; 0 means it never idles out.
	IdleTimeoutSecs int               `json:"idle_timeout_secs"`
	Bind            *Bind             `json:"bind,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
}

// Event types in the audit log.
const (
	EventSandboxCreated    = "sandbox.created"
	EventSandboxTerminated = "sandbox.terminated" // Reason: "request" or "idle"
	EventNetworkUpdated    = "sandbox.network_updated"
	EventSandboxSuspended  = "sandbox.suspended"
	EventSandboxResumed    = "sandbox.resumed"
	EventSnapshotCreated   = "snapshot.created"
	EventProcessStarted    = "process.started"
	EventProcessExited     = "process.exited"
	EventFileRead          = "file.read"
	EventFileWritten       = "file.written"
	EventFileRemoved       = "file.removed"
	EventWorkspaceIn       = "workspace.in"  // a bundle cloned into /workspace
	EventWorkspaceOut      = "workspace.out" // a bundle taken out of it
	EventTunnelOpened      = "tunnel.opened"
)

// Event is one line of the audit log: something a client asked a sandbox to
// do, or something that happened to it. Environment variables appear by name
// only — a value is never recorded. An argv is recorded as given, so a token
// typed on a command line is in the log; treat the log as sensitive.
type Event struct {
	Time    time.Time         `json:"time"`
	Type    string            `json:"type"`
	Sandbox string            `json:"sandbox"`
	Name    string            `json:"name,omitempty"`
	Image   string            `json:"image,omitempty"`
	Labels  map[string]string `json:"labels,omitempty"`
	// Network is the policy in force after the event (created, updated).
	Network  *NetworkPolicy `json:"network,omitempty"`
	EnvNames []string       `json:"env_names,omitempty"`
	Bind     string         `json:"bind,omitempty"`
	Snapshot string         `json:"snapshot,omitempty"`

	PID        int      `json:"pid,omitempty"`
	Argv       []string `json:"argv,omitempty"`
	Cwd        string   `json:"cwd,omitempty"`
	ExitCode   *int     `json:"exit_code,omitempty"`
	DurationMS int64    `json:"duration_ms,omitempty"`

	Path  string `json:"path,omitempty"`
	Bytes int64  `json:"bytes,omitempty"`
	Port  int    `json:"port,omitempty"`

	Reason string `json:"reason,omitempty"`
}

// EventList is the body of GET /v1/sandboxes/{ref}/events.
type EventList struct {
	Events []Event `json:"events"`
	// Truncated is set when older events exist than the ones returned.
	Truncated bool `json:"truncated,omitempty"`
}

// Snapshot is a capture of a sandbox's memory and disk.
type Snapshot struct {
	ID        string    `json:"id"`
	Sandbox   string    `json:"sandbox"`
	Image     string    `json:"image"`
	Bytes     int64     `json:"bytes"`
	CreatedAt time.Time `json:"created_at"`
}

// SnapshotList is the body of GET /v1/snapshots.
type SnapshotList struct {
	Snapshots []Snapshot `json:"snapshots"`
}

// SandboxList is the body of GET /v1/sandboxes.
type SandboxList struct {
	Sandboxes []Sandbox `json:"sandboxes"`
}

// RunRequest runs a command to completion (TimeoutSecs) or starts it in the
// background (TimeoutSecs ignored).
type RunRequest struct {
	Argv        []string          `json:"argv"`
	Env         map[string]string `json:"env,omitempty"`
	Cwd         string            `json:"cwd,omitempty"`
	Stdin       []byte            `json:"stdin,omitempty"`
	TimeoutSecs int               `json:"timeout_secs,omitempty"`
	// Tty runs the process on a terminal (background processes only): one
	// output stream, and a size that attach can change.
	Tty  bool   `json:"tty,omitempty"`
	Rows uint16 `json:"rows,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
}

// RunResult is the outcome of a run.
type RunResult struct {
	ExitCode  int    `json:"exit_code"`
	Stdout    []byte `json:"stdout"`
	Stderr    []byte `json:"stderr"`
	Truncated bool   `json:"truncated"`
	TimedOut  bool   `json:"timed_out"`
}

// Process states.
const (
	ProcessRunning = "running"
	ProcessExited  = "exited"
)

// Process is a background process. PID is the server's handle, not the guest
// kernel's.
type Process struct {
	PID       int       `json:"pid"`
	Tty       bool      `json:"tty,omitempty"`
	Argv      []string  `json:"argv"`
	State     string    `json:"state"`
	ExitCode  *int      `json:"exit_code"`
	StartedAt time.Time `json:"started_at"`
}

// ProcessList is the body of GET …/processes.
type ProcessList struct {
	Processes []Process `json:"processes"`
}

// OutputEvent is one line of an output stream: a chunk of output, or — last —
// the exit code.
type OutputEvent struct {
	Stream   string `json:"stream,omitempty"` // "stdout" or "stderr"
	Data     []byte `json:"data,omitempty"`
	ExitCode *int   `json:"exit_code,omitempty"`
}

// SignalRequest names a signal to deliver.
type SignalRequest struct {
	Signal string `json:"signal"`
}

// Signals a client may send. Deliberately short: these are the ones every
// backend can deliver with the same meaning.
var Signals = []string{"INT", "TERM", "KILL", "HUP"}

// DirEntry is one entry of a directory listing.
type DirEntry struct {
	Name string `json:"name"`
	Type string `json:"type"` // "file" or "dir"
	Size int64  `json:"size"`
}

// DirList is the body of GET …/dirs.
type DirList struct {
	Entries []DirEntry `json:"entries"`
}

// Error codes. See docs/api/v1.md for what each means.
const (
	CodeInvalidRequest  = "invalid_request"
	CodeUnauthorized    = "unauthorized"
	CodeRefused         = "refused"
	CodeForbiddenOrigin = "forbidden_origin"
	CodeNotFound        = "not_found"
	CodeConflict        = "conflict"
	CodeUnsupported     = "unsupported"
	CodeInternal        = "internal"
)

// ErrorBody is the body of every non-2xx response.
type ErrorBody struct {
	Error ErrorDetail `json:"error"`
}

// ErrorDetail is the error inside ErrorBody.
type ErrorDetail struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

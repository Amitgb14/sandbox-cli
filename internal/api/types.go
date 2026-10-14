// Package api is the Sandbox API v1: its wire types, a Go client, and — in
// api/conformance — the suite that defines what every endpoint must do.
//
// The contract is docs/api/v1.md. These types are that document in Go; when the
// two disagree, the document is fixed first and the types follow.
package api

import (
	"regexp"
	"strings"
	"time"
)

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
	// Groups names a gateway's allowlist groups (EgressGroup), in a request
	// for mode allowlist only. The gateway adds their hosts to Allow and
	// sends the node hosts alone, so a sandbox's policy never carries
	// Groups; a server that is not a gateway refuses them. With Allow
	// absent, the groups' hosts are the whole list, not added to the
	// server's default one.
	Groups []string `json:"groups,omitempty"`
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
	// CapDiskSnapshot: a sandbox's files can be captured, and new sandboxes
	// started from them, booting afresh: nothing that was running is kept.
	// What a backend offers when it cannot capture memory (the macOS one).
	CapDiskSnapshot = "disk_snapshot"
	// CapEgressAllowlist: network mode allowlist is enforced. Without it, the
	// endpoint offers only none — a sandbox there has no network at all.
	CapEgressAllowlist = "egress_allowlist"
	// CapEgressOpen: network mode open is available.
	CapEgressOpen = "egress_open"
	// CapTunnel: a TCP port on the guest's loopback can be reached through the API.
	CapTunnel = "tunnel"
	// CapVolumes: named volumes persist across sandboxes and are mounted at a
	// path on create.
	CapVolumes = "volumes"
	// CapMetrics: the server samples each running sandbox's CPU and memory,
	// as the host measures them, and serves the last hour at
	// GET /v1/sandboxes/{ref}/metrics. A property of the server and its
	// backend together: the backend must be able to read usage.
	CapMetrics = "metrics"
	// CapAudit: the server keeps an event log, served per sandbox at
	// GET /v1/sandboxes/{ref}/events. A property of the server, not the backend.
	CapAudit = "audit"
	// CapImages: the operator manages the images sandboxes start from —
	// GET, POST and DELETE /v1/images — rather than each being pulled only
	// when a sandbox first asks for it.
	CapImages = "images"
	// CapRegistryLogin: a create may carry a registry login
	// (CreateSandboxRequest.RegistryLogin), which the pull of its image uses.
	// A backend that pulls through something else (the macOS container
	// runtime) cannot, and a create carrying one is refused there.
	CapRegistryLogin = "registry_login"
	// CapResize: a running sandbox's CPUs, memory and disk can be changed
	// (POST /v1/sandboxes/{ref}/resize). The sandbox restarts to take the
	// new size: its files are kept, its processes are not.
	CapResize = "resize"
)

// Limits are the largest resources a sandbox may ask for.
type Limits struct {
	MaxCPUs     float64 `json:"max_cpus"`
	MaxMemoryMB int     `json:"max_memory_mb"`
	MaxDiskMB   int     `json:"max_disk_mb"`
	// MaxIdleTimeoutSecs bounds idle_timeout_secs; 0 means no bound.
	MaxIdleTimeoutSecs int `json:"max_idle_timeout_secs"`
	// MaxLifetimeSecs is how long any sandbox may exist, busy or not,
	// counted from its creation; 0 means no bound. Where it is set, every
	// sandbox has a lifetime: this one, or a shorter one it asked for.
	MaxLifetimeSecs int `json:"max_lifetime_secs,omitempty"`
	// MinSnapshotEverySecs is the shortest interval a snapshot schedule may
	// ask for, and MaxSnapshotKeep the most scheduled snapshots of one
	// sandbox it may keep: each is a copy of the sandbox, on the host's disk.
	MinSnapshotEverySecs int `json:"min_snapshot_every_secs"`
	MaxSnapshotKeep      int `json:"max_snapshot_keep"`
}

// SnapshotSchedule is a sandbox's scheduled snapshots: one every EverySecs
// while it runs, the newest Keep kept. EverySecs 0 is no schedule.
type SnapshotSchedule struct {
	EverySecs int `json:"every_secs"`
	Keep      int `json:"keep"`
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
	// LifetimeSecs terminates the sandbox this long after it is created,
	// whatever it is doing. 0 takes limits.max_lifetime_secs (none where that
	// is 0); more than that limit is refused.
	LifetimeSecs int `json:"lifetime_secs,omitempty"`
	// SnapshotID starts the sandbox from a snapshot. A memory snapshot
	// (capability memory_snapshot) brings back memory, processes and disk as
	// they were, so its image and resources are the snapshot's. A disk
	// snapshot (disk_snapshot) brings back the files only and boots afresh,
	// so only its image is fixed; resources are the request's.
	SnapshotID string `json:"snapshot_id,omitempty"`
	// SnapshotEverySecs takes a snapshot of the sandbox this often while it
	// runs, keeping the newest SnapshotKeep of them (default 1); a manual
	// snapshot is never removed by it. Needs a snapshot capability, and is
	// bounded by min_snapshot_every_secs and max_snapshot_keep.
	SnapshotEverySecs int `json:"snapshot_every_secs,omitempty"`
	SnapshotKeep      int `json:"snapshot_keep,omitempty"`
	// Labels are the client's own metadata: up to 32 keys of lowercase letters,
	// digits and . _ / -, values up to 256 printable bytes. They decide nothing
	// about the sandbox; they are returned with it, filter the listing
	// (?label=k=v), and are recorded in its audit events, so a client can say
	// why a sandbox exists — which agent, which routing attempt, which job.
	Labels map[string]string `json:"labels,omitempty"`
	// Volumes mounts named volumes (capability volumes). A volume is attached
	// to one live sandbox at a time.
	Volumes []VolumeMount `json:"volumes,omitempty"`
	// RegistryLogin is a login for the registry the image is pulled from
	// (capability registry_login), for a private image. It is used for that
	// registry only, and only for this create's pull; it is never returned,
	// stored or recorded. Through a gateway it is the gateway's to set, from
	// the organization's registry logins, and a client's is refused.
	RegistryLogin *RegistryLogin `json:"registry_login,omitempty"`
}

// RegistryLogin is a user name and a token for one registry.
type RegistryLogin struct {
	Registry string `json:"registry"` // host[:port]: ghcr.io
	Username string `json:"username"`
	Password string `json:"password"`
}

// ResizeSandboxRequest is the body of POST /v1/sandboxes/{ref}/resize. A
// field left 0 keeps what the sandbox has. The disk can only grow.
type ResizeSandboxRequest struct {
	CPUs     float64 `json:"cpus,omitempty"`
	MemoryMB int     `json:"memory_mb,omitempty"`
	DiskMB   int     `json:"disk_mb,omitempty"`
}

// ResizeResult answers a resize: the sandbox at its size after, and whether
// it was restarted to get there. A disk that only grows is grown live where
// the endpoint can, its processes running on; anything else restarts it.
type ResizeResult struct {
	Sandbox
	Restarted bool `json:"restarted"`
}

// VolumeMount attaches a volume at a path inside the sandbox.
type VolumeMount struct {
	Name     string `json:"name"`
	Path     string `json:"path"`
	ReadOnly bool   `json:"read_only,omitempty"`
}

// Volume is a named filesystem that outlives the sandboxes it is mounted in.
type Volume struct {
	Name      string    `json:"name"`
	SizeMB    int       `json:"size_mb"`
	CreatedAt time.Time `json:"created_at"`
	// AttachedTo is the live sandbox it is mounted in, if any.
	AttachedTo string `json:"attached_to,omitempty"`
}

// CreateVolumeRequest creates a volume.
type CreateVolumeRequest struct {
	Name   string `json:"name"`
	SizeMB int    `json:"size_mb,omitempty"`
}

// VolumeList is the body of GET /v1/volumes.
type VolumeList struct {
	Volumes []Volume `json:"volumes"`
}

// UpdateSandboxRequest changes a live sandbox. Each field left out is left
// as it is. Network needs a running sandbox and the network_policy_update
// capability; the rest are this server's records of the sandbox, changed on
// a running or suspended one with nothing asked of its VM.
type UpdateSandboxRequest struct {
	Network *NetworkPolicy `json:"network,omitempty"`
	// Name renames it; "" removes the name. Unique among live sandboxes, as
	// at create.
	Name *string `json:"name,omitempty"`
	// Labels replace its labels whole; {} removes them all.
	Labels *map[string]string `json:"labels,omitempty"`
	// IdleTimeoutSecs is how long it may sit idle before it is terminated,
	// counted from its last activity. Between 1 and limits.max_idle_timeout_secs;
	// 0, never, only where that limit is 0.
	IdleTimeoutSecs *int `json:"idle_timeout_secs,omitempty"`
	// LifetimeSecs is a new lifetime, counted from creation as at create:
	// longer extends it, shorter brings its end nearer, and one already
	// past ends it at once. At least 1, and at most limits.max_lifetime_secs
	// where that is set. Through a gateway only an admin may lengthen it.
	LifetimeSecs *int `json:"lifetime_secs,omitempty"`
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
	IdleTimeoutSecs int `json:"idle_timeout_secs"`
	// LifetimeSecs is how long it may exist, and ExpiresAt when it is
	// terminated for it, busy or not; both absent when it has no lifetime.
	LifetimeSecs int        `json:"lifetime_secs,omitempty"`
	ExpiresAt    *time.Time `json:"expires_at,omitempty"`
	// SnapshotEverySecs and SnapshotKeep are the sandbox's snapshot
	// schedule; 0 is none.
	SnapshotEverySecs int `json:"snapshot_every_secs,omitempty"`
	SnapshotKeep      int `json:"snapshot_keep,omitempty"`
	// Snapshotting is the snapshot being taken of it, while one is.
	Snapshotting *SnapshotProgress `json:"snapshotting,omitempty"`
	Labels       map[string]string `json:"labels,omitempty"`
	Volumes      []VolumeMount     `json:"volumes,omitempty"`
}

// Snapshot phases, in order.
const (
	SnapshotPhaseCapture = "capture" // reading the sandbox: its files, or its memory and disk
	SnapshotPhaseStore   = "store"   // keeping what was read where a fork can start from it
)

// SnapshotProgress is how far a snapshot has got. Bytes is what has been read
// of the sandbox so far; EstimatedBytes, where the backend can say, is about
// how much there is, and 0 where it cannot. It is an estimate to draw a bar
// with, and Bytes may pass it.
type SnapshotProgress struct {
	StartedAt      time.Time `json:"started_at"`
	Scheduled      bool      `json:"scheduled,omitempty"`
	Phase          string    `json:"phase"`
	Bytes          int64     `json:"bytes"`
	EstimatedBytes int64     `json:"estimated_bytes,omitempty"`
}

// Event types in the audit log.
const (
	EventSandboxCreated    = "sandbox.created"
	EventSandboxTerminated = "sandbox.terminated" // Reason: "request" or "idle"
	EventNetworkUpdated    = "sandbox.network_updated"
	EventSandboxUpdated    = "sandbox.updated" // name, labels or idle timeout changed; the event carries them after
	EventSandboxSuspended  = "sandbox.suspended"
	EventSandboxResumed    = "sandbox.resumed"
	EventSandboxResized    = "sandbox.resized" // Reason: the size before and after
	EventSnapshotCreated   = "snapshot.created"
	EventSnapshotDeleted   = "snapshot.deleted" // Reason: "request" or "retention"
	EventProcessStarted    = "process.started"
	EventProcessExited     = "process.exited"
	EventFileRead          = "file.read"
	EventFileWritten       = "file.written"
	EventFileRemoved       = "file.removed"
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
	Snapshot string         `json:"snapshot,omitempty"`
	Volumes  []VolumeMount  `json:"volumes,omitempty"`
	// IdleTimeoutSecs is the idle timeout after an update that changed it.
	IdleTimeoutSecs *int `json:"idle_timeout_secs,omitempty"`
	// LifetimeSecs is the lifetime after an update that changed it.
	LifetimeSecs *int `json:"lifetime_secs,omitempty"`

	PID int `json:"pid,omitempty"`
	// A process is recorded by its program, its argument count and a hash
	// of its arguments — never their text. An agent's arguments carry its
	// prompt, and a prompt or a command line is where a token gets pasted;
	// the log outlives the sandbox by design. The hash is SHA-256 over the
	// arguments after the program, each followed by a NUL byte (an argument
	// cannot hold one), so `printf '%s\0' ARGS… | sha256sum` matches a known
	// command without the log having kept it.
	Program    string `json:"program,omitempty"`
	ArgCount   int    `json:"arg_count,omitempty"`
	ArgsSHA256 string `json:"args_sha256,omitempty"`
	Cwd        string `json:"cwd,omitempty"`
	ExitCode   *int   `json:"exit_code,omitempty"`
	DurationMS int64  `json:"duration_ms,omitempty"`

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

// Snapshot kinds.
const (
	SnapshotMemory = "memory" // memory, processes and disk
	SnapshotDisk   = "disk"   // the files only
)

// Snapshot is a capture of a sandbox: its memory and disk, or its files only,
// as Kind says.
type Snapshot struct {
	ID      string `json:"id"`
	Sandbox string `json:"sandbox"`
	Image   string `json:"image"`
	Kind    string `json:"kind"`
	// Scheduled is set on a snapshot the sandbox's schedule took, which its
	// retention may remove; a snapshot taken by request never is.
	Scheduled bool      `json:"scheduled,omitempty"`
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
	CodeUnavailable     = "unavailable"
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

// NodeStatus is GET /v1/node: what a sandboxd reports about itself to a
// gateway in front of many nodes (docs/roadmap/task-7-fleet-gateway.md). A
// single sandboxd serves it too; it is how a gateway decides where a sandbox
// goes and whether a node is healthy.
type NodeStatus struct {
	// Node is this sandboxd's name, set with --node-id; it is the <node> in
	// the ids of the sandboxes it creates. Empty on a standalone sandboxd.
	Node         string       `json:"node"`
	Version      string       `json:"version"`
	Capabilities Capabilities `json:"capabilities"`
	// Capacity is what the machine has for sandboxes, and Free what is not
	// yet given to running or suspended ones: allocations, not live usage.
	Capacity NodeResources `json:"capacity"`
	Free     NodeResources `json:"free"`
	Running  int           `json:"running"`
	// Pooled counts sandboxes booted ahead, by image.
	Pooled map[string]int `json:"pooled,omitempty"`
	// Images are those whose root disk is already built here, so a create
	// for them skips the pull and the build.
	Images []string          `json:"images,omitempty"`
	Labels map[string]string `json:"labels,omitempty"`
	// Cordoned nodes take no new sandboxes; what runs there carries on.
	Cordoned bool `json:"cordoned"`
}

// NodeResources is a quantity of the resources sandboxes are given.
type NodeResources struct {
	CPUs     float64 `json:"cpus"`
	MemoryMB int     `json:"memory_mb"`
	DiskMB   int     `json:"disk_mb"`
}

// CordonRequest is POST /v1/node/cordon.
type CordonRequest struct {
	Cordoned bool `json:"cordoned"`
}

// nodeIDRE is a node name as it appears in a sandbox id: short, lowercase,
// no underscore, so the id splits unambiguously.
var nodeIDRE = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,30}$`)

// ValidNodeID reports whether s may name a node.
func ValidNodeID(s string) bool { return nodeIDRE.MatchString(s) }

// NodeOfID returns the node a sandbox id names — "n17" for
// "sbx_n17_0123456789abcdef" — and false for an id that names none, which is
// every id a standalone sandboxd makes. A gateway routes by it; the state
// store, not the id, remains the authority on who owns the sandbox.
func NodeOfID(id string) (string, bool) {
	rest, ok := strings.CutPrefix(id, "sbx_")
	if !ok {
		return "", false
	}
	node, hex, ok := strings.Cut(rest, "_")
	if !ok || !ValidNodeID(node) || len(hex) != 16 {
		return "", false
	}
	return node, true
}

// Gateway-only endpoints. A sandboxd answers none of these; a gateway in front
// of many nodes adds them (docs/roadmap/task-7-fleet-gateway.md). The sandbox
// endpoints above are the same on both.
//
//	GET    /v1/whoami                          -> Whoami
//	GET    /v1/ssh                             -> SSHInfo
//	POST   /v1/ssh-keys          SSHKeyRequest -> SSHKeyInfo
//	GET    /v1/ssh-keys                        -> SSHKeyList
//	DELETE /v1/ssh-keys/{id}
//	POST   /v1/sandboxes/{ref}/ssh-access  SSHAccessRequest -> SSHAccess
//	POST   /v1/admin/keys        CreateKeyRequest -> CreatedKey      (admin)
//	GET    /v1/admin/keys                      -> KeyList            (admin)
//	DELETE /v1/admin/keys/{id}                                       (admin)
//	GET    /v1/admin/ssh-keys?user=U           -> SSHKeyList         (admin)
//	DELETE /v1/admin/ssh-keys/{id}                                   (admin)
//	GET    /v1/admin/nodes                     -> NodeList           (admin)
//	POST   /v1/admin/nodes       NodeSpec      -> NodeInfo           (admin)
//	DELETE /v1/admin/nodes/{name}                                    (admin)
//	POST   /v1/admin/nodes/{name}/cordon  CordonRequest -> NodeInfo  (admin)
//	GET    /v1/orgs                            -> OrgList
//	POST   /v1/orgs              CreateOrgRequest -> Org             (org:create)
//	GET    /v1/orgs/{name}/members             -> OrgMemberList      (a member)
//	POST   /v1/orgs/{name}/members  OrgMemberRequest -> OrgMember    (an owner)
//	DELETE /v1/orgs/{name}/members/{user}[?tenant=T]                 (an owner)
//	GET    /v1/admin/orgs                      -> AdminOrgList       (admin)
//
// Any request to a gateway may carry OrgHeader to act in one of the
// caller's organisations instead of its key's own tenant.

// Whoami is the caller as the gateway sees its credential.
type Whoami struct {
	User string `json:"user"`
	// Tenant is the key's own tenant, whatever organisation the request
	// selected; "" is the default tenant.
	Tenant string   `json:"tenant"`
	KeyID  string   `json:"key_id"`
	Scopes []string `json:"scopes"`
	// Org is the organisation this request acts in, after OrgHeader: the
	// key's own tenant when the header is absent, and DefaultOrg for the
	// default tenant.
	Org string `json:"org,omitempty"`
	// Policy is the operator's policy for this user, if any: what their
	// sandboxes are held to on top of the nodes' own limits.
	Policy *UserPolicy `json:"policy,omitempty"`
	// Email is set when the key is an account's, or one of its sign-ins':
	// its user signs in with that address and a password.
	Email string `json:"email,omitempty"`
}

// OrgHeader selects the organisation a gateway request acts in. Absent, a
// request acts in its key's own tenant. Present, the gateway allows it only
// when the key's user is a member of that organisation (or the key is an
// admin's), and answers 404 otherwise, as for an organisation that does not
// exist. A plain sandboxd ignores it.
const OrgHeader = "X-Sandbox-Org"

// DefaultOrg is the default tenant's name on the wire: the tenant of keys
// issued with none. No organisation may be created with it.
const DefaultOrg = "default"

// Org is one organisation the caller may act in.
type Org struct {
	Name string `json:"name"`
	// Role is the caller's: "owner" or "member".
	Role    string    `json:"role"`
	Created time.Time `json:"created,omitzero"`
	// Current marks the organisation this request acted in.
	Current bool `json:"current"`
}

// OrgList is GET /v1/orgs: the key's own tenant first, then every
// organisation its user is a member of.
type OrgList struct {
	Orgs []Org `json:"orgs"`
}

// CreateOrgRequest is POST /v1/orgs. A name is a DNS label of 1 to 30
// lowercase letters, digits and dashes, starting with a letter, with no
// "--", and not "default" or "admin".
type CreateOrgRequest struct {
	Name string `json:"name"`
}

// Organisation roles.
const (
	RoleOwner  = "owner"
	RoleMember = "member"
)

// OrgMember is one member of an organisation. A user is named with the
// tenant of their own keys, because a user name is unique only within a
// tenant; "" is the default tenant.
type OrgMember struct {
	User   string    `json:"user"`
	Tenant string    `json:"tenant,omitempty"`
	Role   string    `json:"role"`
	Added  time.Time `json:"added,omitzero"`
}

// OrgMemberList is GET /v1/orgs/{name}/members.
type OrgMemberList struct {
	Members []OrgMember `json:"members"`
}

// OrgMemberRequest adds a member or changes one's role. Tenant is the
// member's own tenant; omitted, it is the caller's. Role defaults to member.
type OrgMemberRequest struct {
	User   string `json:"user"`
	Tenant string `json:"tenant,omitempty"`
	Role   string `json:"role,omitempty"`
}

// AdminOrg is one organisation as an admin lists it.
type AdminOrg struct {
	Name            string    `json:"name"`
	Created         time.Time `json:"created"`
	CreatedBy       string    `json:"created_by"`
	CreatedByTenant string    `json:"created_by_tenant,omitempty"`
	Members         int       `json:"members"`
	Owners          int       `json:"owners"`
	// OwnedBy names each owner, user and tenant: who holds the organisation
	// now, which CreatedBy says only of its start.
	OwnedBy []OrgMember `json:"owned_by,omitempty"`
}

// AdminOrgList is GET /v1/admin/orgs.
type AdminOrgList struct {
	Orgs []AdminOrg `json:"orgs"`
}

// SSHInfo is where the gateway's SSH server listens and the host key to pin.
type SSHInfo struct {
	Host string `json:"host"`
	Port int    `json:"port"`
	// HostKeys are the server's public host keys as known_hosts lines
	// without the host field ("ssh-ed25519 AAAA…").
	HostKeys    []string `json:"host_keys"`
	Fingerprint string   `json:"fingerprint"` // SHA256:… of the first host key
}

// SSHKeyRequest registers a public key for SSH logins.
type SSHKeyRequest struct {
	// Key is one authorized_keys line: type, base64 key, optional comment.
	// Options (command=, from=, …) are refused.
	Key string `json:"key"`
	// Sandbox, when set, limits the key to that sandbox.
	Sandbox string `json:"sandbox,omitempty"`
}

// SSHKeyInfo is a registered key, as returned to its owner.
type SSHKeyInfo struct {
	ID          string    `json:"id"`
	Fingerprint string    `json:"fingerprint"`
	Key         string    `json:"key"`
	Sandbox     string    `json:"sandbox,omitempty"`
	Created     time.Time `json:"created"`
}

// SSHKeyList is GET /v1/ssh-keys.
type SSHKeyList struct {
	Keys []SSHKeyInfo `json:"keys"`
}

// SSHAccessRequest asks for a short-lived SSH login to one sandbox.
type SSHAccessRequest struct {
	// TTLSecs is how long the token is valid; 0 takes the gateway's default,
	// and more than its maximum is refused.
	TTLSecs int `json:"ttl_secs,omitempty"`
}

// SSHAccess is a short-lived SSH login: the token is the SSH username, and
// it is the whole credential until it expires.
type SSHAccess struct {
	User      string    `json:"user"`
	Host      string    `json:"host"`
	Port      int       `json:"port"`
	ExpiresAt time.Time `json:"expires_at"`
	// Command is the ssh command line that uses it.
	Command string `json:"command"`
}

// CreateKeyRequest issues an API key (admin).
type CreateKeyRequest struct {
	User   string   `json:"user"`
	Tenant string   `json:"tenant,omitempty"`
	Scopes []string `json:"scopes"`
	// Policy, when set, is assigned to the key's user as the key is issued
	// (UserPolicyRequest); it must exist.
	Policy string `json:"policy,omitempty"`
	// ExpiresInSecs, when set, makes the key stop working that long after
	// it is issued, as if revoked then; 0 is never.
	ExpiresInSecs int `json:"expires_in_secs,omitempty"`
}

// CreatePersonalKeyRequest is POST /v1/keys: a key the caller makes for
// themselves, with some of their own scopes (never admin), ending
// ExpiresInSecs from now — an hour to 366 days, and never after the
// account or key it is made under. Name tells it apart in a listing.
type CreatePersonalKeyRequest struct {
	Name          string   `json:"name"`
	Scopes        []string `json:"scopes"`
	ExpiresInSecs int      `json:"expires_in_secs"`
}

// KeyExpiryRequest is PATCH /v1/admin/keys/{id}: the key stops working
// ExpiresInSecs from now, never when 0, and at once when negative — what
// it has open is ended as for a revocation.
type KeyExpiryRequest struct {
	ExpiresInSecs int `json:"expires_in_secs"`
}

// KeyInfo is an API key without its secret.
type KeyInfo struct {
	ID      string    `json:"id"`
	User    string    `json:"user"`
	Tenant  string    `json:"tenant"`
	Scopes  []string  `json:"scopes"`
	Created time.Time `json:"created"`
	Revoked bool      `json:"revoked,omitempty"`
	// Policy names the policy the key's user holds; "" is none.
	Policy string `json:"policy,omitempty"`
	// Expires is when the key stops working; absent is never.
	Expires *time.Time `json:"expires,omitempty"`
	// Email, when set, makes the key an account: its user signs in with
	// that address and a password, and the key's secret is never shown.
	Email     string     `json:"email,omitempty"`
	LastLogin *time.Time `json:"last_login,omitempty"`
	// Suspended, with Revoked, is a key that is refused now and may be
	// restored until RestoreUntil (POST /v1/admin/keys/{id}/restore); past
	// it the revocation is for good.
	Suspended    bool       `json:"suspended,omitempty"`
	RestoreUntil *time.Time `json:"restore_until,omitempty"`
	// Personal is a key its user made for themselves (POST /v1/keys),
	// named Name.
	Personal bool   `json:"personal,omitempty"`
	Name     string `json:"name,omitempty"`
	// Org is the organization a personal key acts in, and only there.
	Org string `json:"org,omitempty"`
}

// CreatedKey is returned once, when a key is issued: Secret is never shown
// again.
type CreatedKey struct {
	KeyInfo
	Secret string `json:"secret"`
}

// KeyList is GET /v1/admin/keys, and GET /v1/keys for the caller's
// personal keys.
type KeyList struct {
	Keys []KeyInfo `json:"keys"`
}

// UserPolicy is a gateway's named limits for the users it is assigned to
// (admin). It is held on top of every node's own limits, and the strictest
// wins: it can only narrow what a node allows.
type UserPolicy struct {
	Name string `json:"name"`
	// MaxLifetimeSecs is how long each of the user's sandboxes may exist:
	// one that asks for no lifetime gets this one, and one that asks for
	// more is refused. 0 is no bound of the policy's own.
	MaxLifetimeSecs int `json:"max_lifetime_secs"`
}

// UserPolicyList is GET /v1/admin/policies.
type UserPolicyList struct {
	Policies []UserPolicy `json:"policies"`
}

// EgressGroup is a gateway's allowlist group: a named set of hosts an admin
// keeps (PUT /v1/admin/egress-groups/{name}) and any user may name in a
// network policy's Groups. A change holds for sandboxes created after it.
type EgressGroup struct {
	Name        string   `json:"name"`
	Description string   `json:"description,omitempty"`
	Hosts       []string `json:"hosts"`
}

// EgressGroupList is GET /v1/egress-groups and GET /v1/admin/egress-groups.
type EgressGroupList struct {
	Groups []EgressGroup `json:"groups"`
}

// AssignPolicyRequest is PUT /v1/admin/user-policy: assigns Policy to User,
// named with the tenant of their keys, or clears it when Policy is "".
type AssignPolicyRequest struct {
	User   string `json:"user"`
	Tenant string `json:"tenant,omitempty"`
	Policy string `json:"policy"`
}

// NodeSpec adds a node to a gateway (admin).
type NodeSpec struct {
	Name      string `json:"name"`
	Endpoint  string `json:"endpoint"`
	TokenFile string `json:"token_file,omitempty"`
	CAFile    string `json:"ca_file,omitempty"`
	CertFile  string `json:"cert_file,omitempty"`
	KeyFile   string `json:"key_file,omitempty"`
}

// NodeInfo is a node as the gateway sees it: its configuration, whether it
// answers, and its last status.
type NodeInfo struct {
	Name     string      `json:"name"`
	Endpoint string      `json:"endpoint"`
	Healthy  bool        `json:"healthy"`
	LastSeen time.Time   `json:"last_seen,omitempty"`
	Error    string      `json:"error,omitempty"`
	Status   *NodeStatus `json:"status,omitempty"`
}

// NodeList is GET /v1/admin/nodes.
type NodeList struct {
	Nodes []NodeInfo `json:"nodes"`
}

// MetricSample is one reading of a running sandbox's usage, taken on the host:
// nothing here is the guest's own account of itself.
type MetricSample struct {
	Time time.Time `json:"time"`
	// CPUPercent is the share of the sandbox's vCPUs used since the previous
	// sample, 0 to 100.
	CPUPercent       float64 `json:"cpu_percent"`
	MemoryBytes      int64   `json:"memory_bytes"`
	MemoryLimitBytes int64   `json:"memory_limit_bytes"`
	// The counters run from the sandbox's start; a rate is the difference
	// between two samples.
	NetRxBytes     int64 `json:"net_rx_bytes"`
	NetTxBytes     int64 `json:"net_tx_bytes"`
	DiskReadBytes  int64 `json:"disk_read_bytes"`
	DiskWriteBytes int64 `json:"disk_write_bytes"`
	Processes      int   `json:"processes,omitempty"`
}

// Image is one image a server has, or is installing: GET /v1/images.
type Image struct {
	Image string `json:"image"`
	// State is installed, installing, or failed (Error says why); a failed
	// one is listed until it is installed or removed, or the server restarts.
	State       string     `json:"state"`
	Digest      string     `json:"digest,omitempty"`
	Bytes       int64      `json:"bytes,omitempty"`
	InstalledAt *time.Time `json:"installed_at,omitempty"`
	// InUse is how many sandboxes running or suspended here start from it.
	InUse int `json:"in_use"`
	// Default is the server's default image, and Pooled one it keeps a
	// pool of: neither is removed.
	Default  bool           `json:"default,omitempty"`
	Pooled   bool           `json:"pooled,omitempty"`
	Progress *ImageProgress `json:"progress,omitempty"`
	Error    string         `json:"error,omitempty"`
	// Nodes is where the image is, in a gateway's admin listing (GET
	// /v1/admin/images): each node's own state, or the error it answered.
	Nodes []ImageNode `json:"nodes,omitempty"`
}

// ImageNode is one node's part of an image a gateway lists.
type ImageNode struct {
	Node  string `json:"node"`
	State string `json:"state,omitempty"`
	Error string `json:"error,omitempty"`
}

// Image states.
const (
	ImageInstalled  = "installed"
	ImageInstalling = "installing"
	ImageFailed     = "failed"
)

// ImageProgress is how far an install has got: pulling the image's blobs,
// Done of Total bytes, then building its root disk where the backend has one.
type ImageProgress struct {
	Phase string `json:"phase"`
	Done  int64  `json:"done,omitempty"`
	Total int64  `json:"total,omitempty"`
}

// ImageList is the body of GET /v1/images.
type ImageList struct {
	Images []Image `json:"images"`
}

// InstallImageRequest is the body of POST /v1/images.
type InstallImageRequest struct {
	Image string `json:"image"`
}

// RemovedImage is the answer to DELETE /v1/images?image=REF.
type RemovedImage struct {
	Image      string `json:"image"`
	FreedBytes int64  `json:"freed_bytes"`
}

// MetricsList is the body of GET /v1/sandboxes/{ref}/metrics: the samples of
// the last hour, oldest first, one every IntervalSecs.
type MetricsList struct {
	IntervalSecs int            `json:"interval_secs"`
	Samples      []MetricSample `json:"samples"`
}

// CreateInviteRequest is POST /v1/admin/invites: a sign-up link for Email.
// Whoever opens it sets a password and becomes an account with these
// user, tenant, scopes and policy, as a key issued with them would.
type CreateInviteRequest struct {
	Email  string   `json:"email"`
	User   string   `json:"user"`
	Tenant string   `json:"tenant,omitempty"`
	Scopes []string `json:"scopes"`
	Policy string   `json:"policy,omitempty"`
	// ExpiresInSecs is how long the account lasts once made; 0 is for good.
	ExpiresInSecs int `json:"expires_in_secs,omitempty"`
	// ValidSecs is how long the link may be used; 0 is two days, and at
	// most seven.
	ValidSecs int `json:"valid_secs,omitempty"`
	// SendEmail has the gateway email the link to Email, where it was
	// started with a mail relay (InviteList.CanEmail).
	SendEmail bool `json:"send_email,omitempty"`
}

// ResetPasswordRequest is POST /v1/admin/keys/{id}/reset, and POST
// /v1/admin/invites/{id}/resend; the body is optional.
type ResetPasswordRequest struct {
	SendEmail bool `json:"send_email,omitempty"`
}

// InviteInfo is an invite without its token.
type InviteInfo struct {
	ID            string   `json:"id"`
	Email         string   `json:"email"`
	User          string   `json:"user"`
	Tenant        string   `json:"tenant"`
	Scopes        []string `json:"scopes"`
	Policy        string   `json:"policy,omitempty"`
	ExpiresInSecs int      `json:"expires_in_secs,omitempty"`
	// Reset is set on an invite that sets a new password for an account
	// that exists, rather than making one.
	Reset   bool      `json:"reset,omitempty"`
	Created time.Time `json:"created"`
	Expires time.Time `json:"expires"`
}

// CreatedInvite is returned once, when an invite is made: Token is never
// shown again. The link is Studio's address with #invite=Token.
type CreatedInvite struct {
	InviteInfo
	Token string `json:"token"`
	// Emailed says the gateway sent the link to the address; EmailError
	// why it could not, when it was asked to. The invite stands either way.
	Emailed    bool   `json:"emailed,omitempty"`
	EmailError string `json:"email_error,omitempty"`
}

// InviteList is GET /v1/admin/invites: those not used and not expired.
type InviteList struct {
	Invites []InviteInfo `json:"invites"`
	// CanEmail says the gateway can send an invite's link itself.
	CanEmail bool `json:"can_email,omitempty"`
}

// InviteTokenRequest is POST /v1/auth/invite: what an invite is for, so
// the page that opens it can say so before a password is set.
type InviteTokenRequest struct {
	Token string `json:"token"`
}

// RegisterRequest is POST /v1/auth/register: the invite's token and the
// password chosen. TTLSecs is how long the sign-in it starts lasts, as for
// LoginRequest.
type RegisterRequest struct {
	Token    string `json:"token"`
	Password string `json:"password"`
	TTLSecs  int    `json:"ttl_secs,omitempty"`
}

// LoginRequest is POST /v1/auth/login. TTLSecs is how long the key it
// returns lasts: 0 is twelve hours, at most seven days, and never past the
// account's own end.
type LoginRequest struct {
	Email    string `json:"email"`
	Password string `json:"password"`
	TTLSecs  int    `json:"ttl_secs,omitempty"`
}

// Login is a sign-in: an API key for it alone, ended by POST
// /v1/auth/logout, by its expiry, or with its account.
type Login struct {
	Secret  string    `json:"secret"`
	User    string    `json:"user"`
	Tenant  string    `json:"tenant"`
	Email   string    `json:"email"`
	Scopes  []string  `json:"scopes"`
	Expires time.Time `json:"expires"`
}

// PasswordRequest is POST /v1/auth/password: an account's own change of
// password. Every other sign-in of the account ends.
type PasswordRequest struct {
	Current string `json:"current"`
	New     string `json:"new"`
}

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
)

// Limits are the largest resources a sandbox may ask for.
type Limits struct {
	MaxCPUs     float64 `json:"max_cpus"`
	MaxMemoryMB int     `json:"max_memory_mb"`
	MaxDiskMB   int     `json:"max_disk_mb"`
	// MaxIdleTimeoutSecs bounds idle_timeout_secs; 0 means no bound.
	MaxIdleTimeoutSecs int `json:"max_idle_timeout_secs"`
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
	IdleTimeoutSecs int `json:"idle_timeout_secs"`
	// SnapshotEverySecs and SnapshotKeep are the sandbox's snapshot
	// schedule; 0 is none.
	SnapshotEverySecs int               `json:"snapshot_every_secs,omitempty"`
	SnapshotKeep      int               `json:"snapshot_keep,omitempty"`
	Labels            map[string]string `json:"labels,omitempty"`
	Volumes           []VolumeMount     `json:"volumes,omitempty"`
}

// Event types in the audit log.
const (
	EventSandboxCreated    = "sandbox.created"
	EventSandboxTerminated = "sandbox.terminated" // Reason: "request" or "idle"
	EventNetworkUpdated    = "sandbox.network_updated"
	EventSandboxSuspended  = "sandbox.suspended"
	EventSandboxResumed    = "sandbox.resumed"
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
}

// KeyInfo is an API key without its secret.
type KeyInfo struct {
	ID      string    `json:"id"`
	User    string    `json:"user"`
	Tenant  string    `json:"tenant"`
	Scopes  []string  `json:"scopes"`
	Created time.Time `json:"created"`
	Revoked bool      `json:"revoked,omitempty"`
}

// CreatedKey is returned once, when a key is issued: Secret is never shown
// again.
type CreatedKey struct {
	KeyInfo
	Secret string `json:"secret"`
}

// KeyList is GET /v1/admin/keys.
type KeyList struct {
	Keys []KeyInfo `json:"keys"`
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

// MetricsList is the body of GET /v1/sandboxes/{ref}/metrics: the samples of
// the last hour, oldest first, one every IntervalSecs.
type MetricsList struct {
	IntervalSecs int            `json:"interval_secs"`
	Samples      []MetricSample `json:"samples"`
}

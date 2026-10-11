package api

import "time"

// Services are a gateway's (docs/services.md): a sandbox spec and a
// count the gateway keeps true, with health checks, rolling updates and an
// HTTP router in front. A plain sandboxd answers every /v1/services endpoint
// 404.
//
//	POST   /v1/services                 ServiceSpec         -> Service (201)
//	GET    /v1/services                                     -> ServiceList
//	GET    /v1/services/{name}                              -> Service
//	PUT    /v1/services/{name}          ServiceSpec         -> Service
//	POST   /v1/services/{name}/scale    ScaleServiceRequest -> Service
//	DELETE /v1/services/{name}                              (204)

// ServiceSpec is what POST /v1/services and PUT /v1/services/{name} send.
type ServiceSpec struct {
	// Name is a DNS label: lowercase letters, digits and dashes, starting and
	// ending with a letter or digit, at most 63. Unique within a tenant.
	Name  string `json:"name" yaml:"name"`
	Image string `json:"image,omitempty" yaml:"image,omitempty"`
	// Command, when set, is started in each replica as a detached process.
	// A replica whose command exits is replaced.
	Command   []string         `json:"command,omitempty" yaml:"command,omitempty"`
	Replicas  int              `json:"replicas" yaml:"replicas"`
	Resources ServiceResources `json:"resources,omitempty" yaml:"resources,omitempty"`
	// Port is the port on each replica's loopback the service listens on:
	// where an HTTP health check and the router go.
	Port   int            `json:"port,omitempty" yaml:"port,omitempty"`
	Health *ServiceHealth `json:"health,omitempty" yaml:"health,omitempty"`
	// Env is set in every replica. A GET returns names only (EnvNames), as
	// for a sandbox; a value meant to stay secret belongs in Secrets.
	Env       map[string]string `json:"env,omitempty" yaml:"env,omitempty"`
	Network   *NetworkPolicy    `json:"network,omitempty" yaml:"network,omitempty"`
	Placement ServicePlacement  `json:"placement,omitempty" yaml:"placement,omitempty"`
	// Public routes the service through the gateway's HTTP router. A service
	// that is not public is reachable only by its owner, through a tunnel.
	Public bool `json:"public,omitempty" yaml:"public,omitempty"`
	// Secrets names entries in the tenant's secret store, each set in every
	// replica's environment under its name. A name the tenant has no secret
	// for is refused (400); a gateway without a secrets key refuses any (501).
	Secrets []string `json:"secrets,omitempty" yaml:"secrets,omitempty"`
}

// ServiceResources is what each replica is given; zero takes the
// gateway's defaults.
type ServiceResources struct {
	CPUs     float64 `json:"cpus,omitempty" yaml:"cpus,omitempty"`
	MemoryMB int     `json:"memory_mb,omitempty" yaml:"memory_mb,omitempty"`
	DiskMB   int     `json:"disk_mb,omitempty" yaml:"disk_mb,omitempty"`
}

// ServiceHealth is how a replica is checked: exactly one of HTTP (a path,
// GET on Port, 2xx or 3xx is healthy) or Command (run in the replica, exit 0
// is healthy). Failures consecutive failed checks replace the replica.
// Without a health check a replica is healthy while its sandbox lives and its
// command, if any, runs.
type ServiceHealth struct {
	HTTP        string   `json:"http,omitempty" yaml:"http,omitempty"`
	Command     []string `json:"command,omitempty" yaml:"command,omitempty"`
	EverySecs   int      `json:"every_secs,omitempty" yaml:"every_secs,omitempty"`     // default 10
	TimeoutSecs int      `json:"timeout_secs,omitempty" yaml:"timeout_secs,omitempty"` // default 5
	Failures    int      `json:"failures,omitempty" yaml:"failures,omitempty"`         // default 3
}

// ServicePlacement says where replicas go. Spread "node" puts each replica
// on a node holding the fewest of the service's replicas, when one fits.
type ServicePlacement struct {
	Spread string `json:"spread,omitempty" yaml:"spread,omitempty"`
}

// SpreadNode is the one placement spread there is.
const SpreadNode = "node"

// Rollout states.
const (
	RolloutInProgress = "in_progress"
	RolloutDone       = "done"
	RolloutFailed     = "failed"
)

// ServiceRollout is the last change of revision: in progress, done, or
// failed with the reason — in which case the replicas of From keep serving.
type ServiceRollout struct {
	State  string `json:"state"`
	From   int    `json:"from"`
	To     int    `json:"to"`
	Reason string `json:"reason,omitempty"`
}

// Replica states.
const (
	ReplicaStarting  = "starting"  // created, not yet found healthy
	ReplicaHealthy   = "healthy"   // its last check passed
	ReplicaUnhealthy = "unhealthy" // its last check failed; replaced after Failures in a row
	ReplicaLost      = "lost"      // its node stopped answering; being replaced elsewhere
)

// ServiceReplica is one sandbox the service runs.
type ServiceReplica struct {
	Sandbox   string     `json:"sandbox"`
	Node      string     `json:"node"`
	Revision  int        `json:"revision"`
	State     string     `json:"state"`
	Healthy   bool       `json:"healthy"`
	LastCheck *time.Time `json:"last_check,omitempty"`
	LastError string     `json:"last_error,omitempty"`
	// Restarts is how many replicas this one replaced in a line, each for
	// failing: its command exiting or its health check failing.
	Restarts  int       `json:"restarts"`
	CreatedAt time.Time `json:"created_at"`
}

// Service is a service as GET returns it. Spec.Env is left out; EnvNames
// lists its names.
type Service struct {
	Spec     ServiceSpec `json:"spec"`
	EnvNames []string    `json:"env_names,omitempty"`
	Owner    string      `json:"owner"`
	Tenant   string      `json:"tenant,omitempty"`
	// Revision is the newest spec's; Serving the one replicas are kept at
	// when no rollout is in progress.
	Revision int `json:"revision"`
	Serving  int `json:"serving"`
	Desired  int `json:"desired"`
	Ready    int `json:"ready"`
	// Restarts counts every replica replaced for failing.
	Restarts int             `json:"restarts"`
	Rollout  *ServiceRollout `json:"rollout,omitempty"`
	// Error is why the last replica could not be created, if it could not.
	Error string `json:"error,omitempty"`
	// URL is where the gateway's HTTP router serves the service, when it
	// is public and the gateway runs a router.
	URL       string           `json:"url,omitempty"`
	Replicas  []ServiceReplica `json:"replicas"`
	CreatedAt time.Time        `json:"created_at"`
	UpdatedAt time.Time        `json:"updated_at"`
}

// ServiceList is the body of GET /v1/services.
type ServiceList struct {
	Services []Service `json:"services"`
}

// ScaleServiceRequest is the body of POST /v1/services/{name}/scale.
type ScaleServiceRequest struct {
	Replicas int `json:"replicas"`
}

// Labels a gateway stamps on every replica: which service, which revision.
const (
	LabelService         = "gateway.service"
	LabelServiceRevision = "gateway.service.rev"
)

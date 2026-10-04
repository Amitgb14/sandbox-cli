package api

import "time"

// Jobs, agent runs and secrets: work a gateway runs after the request that
// started it has gone. Gateway only, like the endpoints listed near the end
// of types.go; a plain sandboxd answers each 404.
//
//	PUT    /v1/secrets/{name}     SecretRequest                  (secrets:write)
//	GET    /v1/secrets                          -> SecretList    (sandbox:read)
//	DELETE /v1/secrets/{name}                                    (secrets:write)
//	POST   /v1/jobs               JobSpec        -> Job          (sandbox:create)
//	GET    /v1/jobs                             -> JobList       (sandbox:read)
//	GET    /v1/jobs/{id}                        -> Job           (sandbox:read)
//	DELETE /v1/jobs/{id}                        -> Job           (sandbox:create; cancels)
//	GET    /v1/jobs/{id}/runs/{n}/output        -> JobOutput     (sandbox:read)
//	GET    /v1/jobs/{id}/runs/{n}/files?path=P  -> the file's bytes (sandbox:read)
//	POST   /v1/agent-runs         AgentRunRequest -> Job         (sandbox:create)

// Job states. Running covers a job some of whose runs still wait their turn.
const (
	JobRunning   = "running"
	JobSucceeded = "succeeded"
	JobFailed    = "failed"
	JobCancelled = "cancelled"
)

// Run states. A run's attempts share its index; the state is the latest
// attempt's, and a failed or timed-out attempt with retries left goes back
// to queued.
const (
	RunQueued    = "queued"
	RunRunning   = "running"
	RunSucceeded = "succeeded"
	RunFailed    = "failed"
	RunTimedOut  = "timed_out"
	RunCancelled = "cancelled"
)

// JobSpec is a job: one sandbox per run, a command or an agent in each.
type JobSpec struct {
	// Name is for people; jobs are found by id. Optional.
	Name  string `json:"name,omitempty"`
	Image string `json:"image,omitempty"`
	// Command is the argv each run starts; or Agent and Prompt (or Prompts)
	// name an agent, started in its verified headless mode. Exactly one of
	// Command and Agent.
	Command []string `json:"command,omitempty"`
	Agent   string   `json:"agent,omitempty"`
	Prompt  string   `json:"prompt,omitempty"`
	// Prompts makes a batch: one run per prompt, every one to succeed.
	Prompts []string `json:"prompts,omitempty"`
	// Parallelism is how many runs may be in flight at once (default 1).
	Parallelism int `json:"parallelism,omitempty"`
	// Completions is how many runs must succeed (default 1; a batch's is
	// its number of prompts).
	Completions int `json:"completions,omitempty"`
	// Retries is how many more attempts a run that failed or timed out gets.
	Retries int `json:"retries,omitempty"`
	// TimeoutSecs bounds each attempt from the moment its command starts
	// (default 3600).
	TimeoutSecs int               `json:"timeout_secs,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	// Secrets are names of the tenant's secrets (PUT /v1/secrets/{name}),
	// each set in the run's environment under its own name.
	Secrets      []string       `json:"secrets,omitempty"`
	Network      *NetworkPolicy `json:"network,omitempty"`
	Resources    *JobResources  `json:"resources,omitempty"`
	FromSnapshot string         `json:"from_snapshot,omitempty"`
	Keep         *JobKeep       `json:"keep,omitempty"`
	// Notify is a URL POSTed a JobNotification when a run ends and when the
	// job does: https, or http to a loopback address.
	Notify string `json:"notify,omitempty"`
}

// JobResources is what each run's sandbox is given.
type JobResources struct {
	CPUs     float64 `json:"cpus,omitempty"`
	MemoryMB int     `json:"memory_mb,omitempty"`
	DiskMB   int     `json:"disk_mb,omitempty"`
}

// JobKeep is what is kept of each run once its sandbox is gone.
type JobKeep struct {
	// Output keeps stdout and stderr (default true), capped per stream.
	Output *bool `json:"output,omitempty"`
	// Files are absolute guest paths read back when the command ends,
	// each capped.
	Files []string `json:"files,omitempty"`
}

// AgentRunRequest is one agent run: a job of one run, without the batch
// fields.
type AgentRunRequest struct {
	Agent        string            `json:"agent"`
	Prompt       string            `json:"prompt"`
	Name         string            `json:"name,omitempty"`
	Image        string            `json:"image,omitempty"`
	Retries      int               `json:"retries,omitempty"`
	TimeoutSecs  int               `json:"timeout_secs,omitempty"`
	Env          map[string]string `json:"env,omitempty"`
	Secrets      []string          `json:"secrets,omitempty"`
	Network      *NetworkPolicy    `json:"network,omitempty"`
	Resources    *JobResources     `json:"resources,omitempty"`
	FromSnapshot string            `json:"from_snapshot,omitempty"`
	Keep         *JobKeep          `json:"keep,omitempty"`
	Notify       string            `json:"notify,omitempty"`
}

// Job is a job as the gateway reports it. Spec.Env comes back without its
// values (EnvNames lists them), as a sandbox's environment does.
type Job struct {
	ID       string     `json:"id"`
	Name     string     `json:"name,omitempty"`
	State    string     `json:"state"`
	Spec     JobSpec    `json:"spec"`
	EnvNames []string   `json:"env_names,omitempty"`
	Created  time.Time  `json:"created_at"`
	Finished *time.Time `json:"finished_at,omitempty"`
	// Expires is when a finished job and what it kept are forgotten.
	Expires   *time.Time `json:"expires_at,omitempty"`
	Queued    int        `json:"queued"`
	Running   int        `json:"running"`
	Succeeded int        `json:"succeeded"`
	Failed    int        `json:"failed"`
	// Runs is every run, in order; a listing leaves it out.
	Runs  []JobRun `json:"runs,omitempty"`
	Error string   `json:"error,omitempty"`
}

// JobRun is one run of a job.
type JobRun struct {
	N     int    `json:"n"`
	State string `json:"state"`
	// Sandbox is the latest attempt's; it is terminated once the attempt ends.
	Sandbox  string     `json:"sandbox,omitempty"`
	PID      int        `json:"pid,omitempty"`
	Attempts int        `json:"attempts"`
	ExitCode *int       `json:"exit_code,omitempty"`
	Started  *time.Time `json:"started_at,omitempty"`
	Finished *time.Time `json:"finished_at,omitempty"`
	Error    string     `json:"error,omitempty"`
	// OutputTruncated says a stream outgrew what is kept of it.
	OutputTruncated bool      `json:"output_truncated,omitempty"`
	Files           []JobFile `json:"files,omitempty"`
}

// JobFile is a file kept from a run.
type JobFile struct {
	Path      string `json:"path"`
	Size      int64  `json:"size"`
	Truncated bool   `json:"truncated,omitempty"`
	// Error says why it was not kept: not there, not readable.
	Error string `json:"error,omitempty"`
}

// JobList is GET /v1/jobs, newest first, without runs.
type JobList struct {
	Jobs []Job `json:"jobs"`
}

// JobOutput is what was kept of a run's output.
type JobOutput struct {
	Stdout    []byte `json:"stdout"`
	Stderr    []byte `json:"stderr"`
	Truncated bool   `json:"truncated"`
}

// JobNotification is what a job's notify URL is sent: which job, which run,
// what state. Never output, never an environment value.
type JobNotification struct {
	// Event is "run.finished" or "job.finished".
	Event    string `json:"event"`
	Job      string `json:"job"`
	Name     string `json:"name,omitempty"`
	JobState string `json:"job_state"`
	// Run and State are the run's, on run.finished.
	Run      *int   `json:"run,omitempty"`
	State    string `json:"state,omitempty"`
	ExitCode *int   `json:"exit_code,omitempty"`
}

// SecretRequest sets a secret's value. It is never returned.
type SecretRequest struct {
	Value string `json:"value"`
}

// SecretInfo is a secret without its value.
type SecretInfo struct {
	Name    string    `json:"name"`
	Updated time.Time `json:"updated_at"`
}

// SecretList is GET /v1/secrets.
type SecretList struct {
	Secrets []SecretInfo `json:"secrets"`
}

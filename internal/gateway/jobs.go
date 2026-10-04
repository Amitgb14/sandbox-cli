package gateway

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/policy"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// Jobs are work the gateway runs after the request that started it has
// gone: a command or an agent, once or as a batch, each run in a sandbox of
// its own made through the same create path as POST /v1/sandboxes (placed by
// the scheduler, counted against the tenant's quota, owned by the job's
// creator, and labelled gateway.job=<id>). The controller (jobrun.go) starts
// each run's command, waits for it with the job's timeout, keeps its output
// and the files the job names, terminates the sandbox, and retries a run
// that failed.
//
// What a job is, and what became of each run, is in the state file, so a
// gateway that restarts carries on: a run whose command had started is
// followed again where it runs (its process does not depend on the gateway's
// connection), a run caught between its sandbox and its command has that
// sandbox terminated and counts as a failed attempt, and queued runs start as
// before. What runs kept — output, files — is in files beside the state
// (JobsDir), not in it, because the state is rewritten whole on every change.
//
// A finished job is forgotten, with what it kept, JobRetention after it
// finished (default 24h).
//
// A job's environment values are sealed with the secrets key when the
// gateway has one, as secrets are. Without one they are held in memory only,
// and a restarted gateway cannot start new attempts of a job that set any:
// those runs fail, saying so. The state file holds no value either way. It
// does hold a job's command and prompts until the job is forgotten, since a
// restarted gateway needs them; the node's audit record still keeps neither.
//
// A notify URL is the job owner's to name, and the gateway POSTs to it from
// wherever it runs. So it goes only to a public address, checked as it is
// dialled (newNotifyClient): a job cannot make the gateway connect to its own
// loopback, the nodes' network or a metadata address. An operator whose hook
// receivers are on a private network allows that with
// Config.NotifyAllowPrivate (serve --notify-allow-private), and then http to
// loopback as well. The body is never the owner's to choose and no redirect
// is followed.

// Bounds on a job.
const (
	maxJobRuns        = 10000
	maxJobParallelism = 1000
	maxJobRetries     = 100
	defaultJobTimeout = time.Hour
	maxJobTimeout     = 7 * 24 * time.Hour
	maxKeptFiles      = 32
	// maxKeptOutput is what is kept of each output stream of a run;
	// maxKeptFile of each file. Past either the rest is dropped, and the run
	// says so.
	maxKeptOutput = 1 << 20
	maxKeptFile   = 8 << 20
	maxPromptLen  = 256 << 10
	maxNotifyURL  = 2048
)

// LabelJob names the job a run's sandbox belongs to; LabelJobRun its run.
// Like the owner labels, a request cannot set them.
const (
	LabelJob    = "gateway.job"
	LabelJobRun = "gateway.job-run"
)

var jobIDRE = regexp.MustCompile(`^job_[0-9a-f]{16}$`)

// jobRecord is a job as the state file holds it. Spec.Env is always nil
// there: values are sealed in EnvSealed, or held in memory.
type jobRecord struct {
	ID        string       `json:"id"`
	User      string       `json:"user"`
	Tenant    string       `json:"tenant"`
	Spec      api.JobSpec  `json:"spec"`
	EnvNames  []string     `json:"env_names,omitempty"`
	EnvSealed []byte       `json:"env_sealed,omitempty"`
	State     string       `json:"state"`
	Error     string       `json:"error,omitempty"`
	Cancelled bool         `json:"cancelled,omitempty"` // asked; a restarted gateway finishes the cancel
	Created   time.Time    `json:"created"`
	Finished  *time.Time   `json:"finished,omitempty"`
	Runs      []api.JobRun `json:"runs"`
}

func (r *jobRecord) clone() *jobRecord {
	c := *r
	c.Runs = make([]api.JobRun, len(r.Runs))
	for i, run := range r.Runs {
		c.Runs[i] = cloneRun(run)
	}
	return &c
}

func cloneRun(r api.JobRun) api.JobRun {
	if r.ExitCode != nil {
		v := *r.ExitCode
		r.ExitCode = &v
	}
	if r.Started != nil {
		v := *r.Started
		r.Started = &v
	}
	if r.Finished != nil {
		v := *r.Finished
		r.Finished = &v
	}
	r.Files = slices.Clone(r.Files)
	return r
}

func (r *jobRecord) owner() Owner { return Owner{User: r.User, Tenant: r.Tenant} }

func runFinal(state string) bool {
	switch state {
	case api.RunSucceeded, api.RunFailed, api.RunTimedOut, api.RunCancelled:
		return true
	}
	return false
}

// --- the store ------------------------------------------------------------------

// ErrNoSuchJob is returned for a job the store does not hold.
var ErrNoSuchJob = errors.New("no such job")

// PutJob stores a new job.
func (s *FileStore) PutJob(r *jobRecord) error {
	c := r.clone()
	return s.change(func() error {
		s.st.Jobs[c.ID] = c
		return nil
	})
}

// Job returns a copy of a job.
func (s *FileStore) Job(id string) (*jobRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.st.Jobs[id]
	if !ok {
		return nil, false
	}
	return r.clone(), true
}

// AllJobs returns a copy of every job.
func (s *FileStore) AllJobs() []*jobRecord {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]*jobRecord, 0, len(s.st.Jobs))
	for _, r := range s.st.Jobs {
		out = append(out, r.clone())
	}
	return out
}

// UpdateJob changes a job in place and writes it.
func (s *FileStore) UpdateJob(id string, fn func(*jobRecord)) error {
	return s.change(func() error {
		r, ok := s.st.Jobs[id]
		if !ok {
			return ErrNoSuchJob
		}
		fn(r)
		return nil
	})
}

// UpdateRun changes one run of a job and writes it, returning the run as
// changed.
func (s *FileStore) UpdateRun(id string, n int, fn func(*api.JobRun)) (api.JobRun, error) {
	var out api.JobRun
	err := s.change(func() error {
		r, ok := s.st.Jobs[id]
		if !ok || n < 0 || n >= len(r.Runs) {
			return ErrNoSuchJob
		}
		fn(&r.Runs[n])
		out = cloneRun(r.Runs[n])
		return nil
	})
	return out, err
}

// ForgetJob drops a job.
func (s *FileStore) ForgetJob(id string) error {
	return s.change(func() error {
		delete(s.st.Jobs, id)
		return nil
	})
}

// --- validation -----------------------------------------------------------------

// errSpec is a job spec refused, with the status to answer.
type errSpec struct {
	status int
	code   string
	msg    string
}

func (e *errSpec) Error() string { return e.msg }

func badSpec(format string, a ...any) *errSpec {
	return &errSpec{status: http.StatusBadRequest, code: api.CodeInvalidRequest, msg: fmt.Sprintf(format, a...)}
}

// checkJobSpec validates a spec and fills in its defaults. It refuses what
// a node would refuse at a run's start where it can tell now — a reserved
// environment name, a secret that does not exist — so a mistake costs one
// request, not a job of failed runs.
func (g *Gateway) checkJobSpec(p Principal, s *api.JobSpec) error {
	if s.Name != "" && !spec.ValidName(s.Name) {
		return badSpec("name %q: lowercase letters, digits and dashes, starting with a letter or digit, at most 63", s.Name)
	}
	switch {
	case len(s.Command) > 0 && s.Agent != "":
		return badSpec("a job runs a command or an agent, not both")
	case len(s.Command) > 0:
		if s.Prompt != "" || len(s.Prompts) > 0 {
			return badSpec("prompt and prompts are for an agent; a command job runs completions copies of its command")
		}
		if s.Command[0] == "" {
			return badSpec("command: the program is empty")
		}
	case s.Agent != "":
		d, ok := agents.Lookup(s.Agent)
		if !ok || d.AutonomousArgs == nil {
			return badSpec("agent %q: want one of %s (an agent with a verified headless mode)", s.Agent, strings.Join(agents.Names(), ", "))
		}
		if (s.Prompt == "") == (len(s.Prompts) == 0) {
			return badSpec("an agent job needs a prompt, or prompts for a batch (one run per prompt), not both")
		}
		for i, pr := range append([]string{s.Prompt}, s.Prompts...) {
			if i > 0 && pr == "" {
				return badSpec("prompts[%d] is empty", i-1)
			}
			if len(pr) > maxPromptLen {
				return badSpec("a prompt is at most %d bytes", maxPromptLen)
			}
		}
	default:
		return badSpec("a job needs a command, or an agent and a prompt")
	}

	if len(s.Prompts) > 0 {
		if s.Completions != 0 && s.Completions != len(s.Prompts) {
			return badSpec("a batch's completions is its number of prompts (%d)", len(s.Prompts))
		}
		s.Completions = len(s.Prompts)
	}
	if s.Completions == 0 {
		s.Completions = 1
	}
	if s.Parallelism == 0 {
		s.Parallelism = 1
	}
	switch {
	case s.Completions < 0 || s.Completions > maxJobRuns:
		return badSpec("completions: 1 to %d", maxJobRuns)
	case s.Parallelism < 0 || s.Parallelism > maxJobParallelism:
		return badSpec("parallelism: 1 to %d", maxJobParallelism)
	case s.Retries < 0 || s.Retries > maxJobRetries:
		return badSpec("retries: 0 to %d", maxJobRetries)
	case s.TimeoutSecs < 0 || time.Duration(s.TimeoutSecs)*time.Second > maxJobTimeout:
		return badSpec("timeout_secs: 0 (the default, %v) to %d", defaultJobTimeout, int(maxJobTimeout.Seconds()))
	}
	if s.TimeoutSecs == 0 {
		s.TimeoutSecs = int(defaultJobTimeout.Seconds())
	}
	if s.Resources != nil && (s.Resources.CPUs < 0 || s.Resources.MemoryMB < 0 || s.Resources.DiskMB < 0) {
		return badSpec("resources cannot be negative")
	}

	for k, v := range s.Env {
		if !policy.ValidEnvName(k) {
			return badSpec("env: %q is not a valid environment variable name", k)
		}
		if policy.IsReservedEnv(k) {
			return &errSpec{status: http.StatusForbidden, code: api.CodeRefused, msg: "env: " + k + " is reserved: " + policy.ReservedEnvReason()}
		}
		if strings.ContainsRune(v, 0) {
			return badSpec("env: %s contains a NUL byte", k)
		}
	}
	seen := map[string]bool{}
	for _, name := range s.Secrets {
		if err := checkSecretName(name); err != nil {
			return badSpec("secrets: %v", err)
		}
		if seen[name] {
			return badSpec("secrets: %s is named twice", name)
		}
		seen[name] = true
		if _, ok := s.Env[name]; ok {
			return badSpec("%s is both in env and in secrets", name)
		}
	}
	if len(s.Secrets) > 0 {
		if g.sealer == nil {
			return &errSpec{status: http.StatusNotImplemented, code: api.CodeUnsupported, msg: ErrNoSecretsKey.Error()}
		}
		for _, name := range s.Secrets {
			if _, ok := g.store.SealedSecret(p.Tenant, name); !ok {
				return badSpec("secret %s does not exist (sandbox-cli secret set %s)", name, name)
			}
		}
	}

	if s.Keep != nil {
		if len(s.Keep.Files) > maxKeptFiles {
			return badSpec("keep.files: at most %d", maxKeptFiles)
		}
		for _, f := range s.Keep.Files {
			if !path.IsAbs(f) || path.Clean(f) != f || strings.ContainsRune(f, 0) {
				return badSpec("keep.files: %q is not a clean absolute path", f)
			}
		}
	}
	if s.Notify != "" {
		if err := checkNotifyURL(s.Notify, g.cfg.NotifyAllowPrivate); err != nil {
			return badSpec("notify: %v", err)
		}
	}
	return nil
}

// checkNotifyURL allows https, and http only to this machine: a
// notification in the clear across a network says which jobs a user runs to
// anyone on the path. Unless allowPrivate, a host given as an address must
// be a public one, and so http, which is loopback only, is refused; a name
// is checked when it is dialled (newNotifyClient), this only says so early.
func checkNotifyURL(s string, allowPrivate bool) error {
	if len(s) > maxNotifyURL {
		return fmt.Errorf("at most %d bytes", maxNotifyURL)
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" {
		return errors.New("not an absolute URL")
	}
	h := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	switch u.Scheme {
	case "https":
	case "http":
		if ip := net.ParseIP(h); h != "localhost" && (ip == nil || !ip.IsLoopback()) {
			return errors.New("http is allowed only to a loopback address; use https")
		}
	default:
		return errors.New("want an https URL")
	}
	if allowPrivate {
		return nil
	}
	if ip, err := netip.ParseAddr(h); h == "localhost" || strings.HasSuffix(h, ".localhost") || (err == nil && !publicAddr(ip)) {
		return errors.New(h + " is a loopback, private or link-local address, which this gateway does not post to (an operator allows it with sandbox-gateway serve --notify-allow-private)")
	}
	return nil
}

// keepOutput reports whether a job keeps its runs' output (the default).
func keepOutput(s api.JobSpec) bool {
	return s.Keep == nil || s.Keep.Output == nil || *s.Keep.Output
}

// --- the HTTP front -------------------------------------------------------------

func (g *Gateway) createJob(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeCreate) {
		return
	}
	var s api.JobSpec
	if !decode(w, r, &s) {
		return
	}
	g.submitJob(w, p, s)
}

// createAgentRun is a job of one agent run.
func (g *Gateway) createAgentRun(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeCreate) {
		return
	}
	var a api.AgentRunRequest
	if !decode(w, r, &a) {
		return
	}
	if a.Agent == "" || a.Prompt == "" {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "an agent run needs an agent and a prompt")
		return
	}
	g.submitJob(w, p, api.JobSpec{
		Name: a.Name, Image: a.Image, Agent: a.Agent, Prompt: a.Prompt, Retries: a.Retries,
		TimeoutSecs: a.TimeoutSecs, Env: a.Env, Secrets: a.Secrets, Network: a.Network,
		Resources: a.Resources, FromSnapshot: a.FromSnapshot, Keep: a.Keep, Notify: a.Notify,
	})
}

func (g *Gateway) submitJob(w http.ResponseWriter, p Principal, s api.JobSpec) {
	if err := g.checkJobSpec(p, &s); err != nil {
		var es *errSpec
		if errors.As(err, &es) {
			writeErr(w, es.status, es.code, es.msg)
		} else {
			writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, err.Error())
		}
		return
	}
	rec := &jobRecord{ID: newID("job_"), User: p.User, Tenant: p.Tenant, Spec: s,
		State: api.JobRunning, Created: g.store.now().UTC()}
	env := s.Env
	rec.Spec.Env = nil
	for k := range env {
		rec.EnvNames = append(rec.EnvNames, k)
	}
	sort.Strings(rec.EnvNames)
	if len(env) > 0 && g.sealer != nil {
		plain, err := json.Marshal(env)
		if err != nil {
			writeErr(w, http.StatusInternalServerError, api.CodeInternal, "encoding the environment failed")
			return
		}
		rec.EnvSealed = g.sealer.seal(plain, jobEnvAD(rec.ID))
	}
	n := s.Completions
	rec.Runs = make([]api.JobRun, n)
	for i := range rec.Runs {
		rec.Runs[i] = api.JobRun{N: i, State: api.RunQueued}
	}
	if !g.jobs.hold(rec.ID, env) {
		writeErr(w, http.StatusServiceUnavailable, api.CodeUnavailable, "the gateway is not running jobs")
		return
	}
	if err := g.store.PutJob(rec); err != nil {
		g.jobs.drop(rec.ID)
		writeErr(w, http.StatusInternalServerError, api.CodeInternal, "the job could not be recorded")
		return
	}
	g.logf("job %s: %d runs for %s (key %s)", rec.ID, n, p.User, p.KeyID)
	g.startJob(rec.ID)
	writeJSON(w, http.StatusCreated, g.jobView(rec, true))
}

func jobEnvAD(id string) string { return "sandbox-gateway job env v1\x00" + id }

// jobFor finds a job the caller may see; another user's is not found, as a
// sandbox is.
func (g *Gateway) jobFor(w http.ResponseWriter, r *http.Request, p Principal, scope string) (*jobRecord, bool) {
	if !need(w, p, scope) {
		return nil, false
	}
	id := r.PathValue("id")
	rec, ok := g.store.Job(id)
	if !jobIDRE.MatchString(id) || !ok || !mayAct(p, rec.owner()) {
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such job")
		return nil, false
	}
	return rec, true
}

func (g *Gateway) listJobs(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeRead) {
		return
	}
	out := []api.Job{}
	for _, rec := range g.store.AllJobs() {
		if rec.User == p.User && rec.Tenant == p.Tenant {
			out = append(out, g.jobView(rec, false))
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Created.Equal(out[j].Created) {
			return out[i].Created.After(out[j].Created)
		}
		return out[i].ID > out[j].ID
	})
	writeJSON(w, http.StatusOK, api.JobList{Jobs: out})
}

func (g *Gateway) getJob(w http.ResponseWriter, r *http.Request, p Principal) {
	if rec, ok := g.jobFor(w, r, p, ScopeRead); ok {
		writeJSON(w, http.StatusOK, g.jobView(rec, true))
	}
}

// cancelJob cancels a job and waits (a while) for its runs to stop, so the
// answer says what became of them.
func (g *Gateway) cancelJob(w http.ResponseWriter, r *http.Request, p Principal) {
	rec, ok := g.jobFor(w, r, p, ScopeCreate)
	if !ok {
		return
	}
	if rec.State == api.JobRunning {
		if err := g.store.UpdateJob(rec.ID, func(j *jobRecord) { j.Cancelled = true }); err != nil {
			writeErr(w, http.StatusInternalServerError, api.CodeInternal, "the cancel could not be recorded")
			return
		}
		g.jobs.cancel(r.Context(), rec.ID, time.Minute)
		g.logf("job %s cancelled by key %s", rec.ID, p.KeyID)
		if rec, ok = g.store.Job(rec.ID); !ok {
			writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such job")
			return
		}
	}
	writeJSON(w, http.StatusOK, g.jobView(rec, true))
}

// runOf resolves {n} of a job the caller may read.
func (g *Gateway) runOf(w http.ResponseWriter, r *http.Request, p Principal) (*jobRecord, api.JobRun, bool) {
	rec, ok := g.jobFor(w, r, p, ScopeRead)
	if !ok {
		return nil, api.JobRun{}, false
	}
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n < 0 || n >= len(rec.Runs) || strconv.Itoa(n) != r.PathValue("n") {
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such run")
		return nil, api.JobRun{}, false
	}
	return rec, rec.Runs[n], true
}

func (g *Gateway) jobOutput(w http.ResponseWriter, r *http.Request, p Principal) {
	rec, run, ok := g.runOf(w, r, p)
	if !ok {
		return
	}
	if !keepOutput(rec.Spec) {
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "this job keeps no output (keep.output is false)")
		return
	}
	dir := g.runDir(rec.ID, run.N)
	stdout, err1 := os.ReadFile(filepath.Join(dir, "stdout"))
	stderr, err2 := os.ReadFile(filepath.Join(dir, "stderr"))
	if errors.Is(err1, os.ErrNotExist) && errors.Is(err2, os.ErrNotExist) {
		writeErr(w, http.StatusNotFound, api.CodeNotFound, fmt.Sprintf("run %d has no output yet: it is kept when an attempt ends (a running one's is in its sandbox, %s)", run.N, run.Sandbox))
		return
	}
	writeJSON(w, http.StatusOK, api.JobOutput{Stdout: stdout, Stderr: stderr, Truncated: run.OutputTruncated})
}

func (g *Gateway) jobFile(w http.ResponseWriter, r *http.Request, p Principal) {
	rec, run, ok := g.runOf(w, r, p)
	if !ok {
		return
	}
	want := r.URL.Query().Get("path")
	for i, f := range run.Files {
		if f.Path != want || f.Error != "" {
			continue
		}
		data, err := os.ReadFile(filepath.Join(g.runDir(rec.ID, run.N), "file-"+strconv.Itoa(i)))
		if err != nil {
			break
		}
		w.Header().Set("Content-Type", "application/octet-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(data)
		return
	}
	writeErr(w, http.StatusNotFound, api.CodeNotFound, "run "+strconv.Itoa(run.N)+" kept no file "+strconv.Quote(want))
}

// jobView is a job as the API reports it.
func (g *Gateway) jobView(rec *jobRecord, withRuns bool) api.Job {
	j := api.Job{ID: rec.ID, Name: rec.Spec.Name, State: rec.State, Spec: rec.Spec, EnvNames: rec.EnvNames,
		Created: rec.Created, Finished: rec.Finished, Error: rec.Error}
	j.Spec.Env = nil
	if rec.Finished != nil {
		exp := rec.Finished.Add(g.cfg.JobRetention)
		j.Expires = &exp
	}
	for _, run := range rec.Runs {
		switch run.State {
		case api.RunQueued:
			j.Queued++
		case api.RunRunning:
			j.Running++
		case api.RunSucceeded:
			j.Succeeded++
		case api.RunFailed, api.RunTimedOut:
			j.Failed++
		}
	}
	if withRuns {
		j.Runs = rec.Runs
	}
	return j
}

// runDir is where run n of job id keeps its output and files. Both parts
// are the gateway's own (a checked id, an integer).
func (g *Gateway) runDir(id string, n int) string {
	return filepath.Join(g.cfg.JobsDir, id, strconv.Itoa(n))
}

// jobRoutes registers the job, agent-run and secret endpoints.
func (g *Gateway) jobRoutes(route func(pattern string, raw bool, h handlerFunc)) {
	route("PUT /v1/secrets/{name}", false, g.putSecret)
	route("GET /v1/secrets", false, g.listSecrets)
	route("DELETE /v1/secrets/{name}", false, g.deleteSecret)
	route("POST /v1/jobs", false, g.createJob)
	route("GET /v1/jobs", false, g.listJobs)
	route("GET /v1/jobs/{id}", false, g.getJob)
	route("DELETE /v1/jobs/{id}", false, g.cancelJob)
	route("GET /v1/jobs/{id}/runs/{n}/output", false, g.jobOutput)
	route("GET /v1/jobs/{id}/runs/{n}/files", false, g.jobFile)
	route("POST /v1/agent-runs", false, g.createAgentRun)
}

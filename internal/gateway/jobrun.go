package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agenthome"
	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// The job controller. One goroutine per unfinished job hands its runs out
// to at most Parallelism goroutines at a time; each run's goroutine makes
// its attempts in turn. Everything a restarted gateway needs to carry on is
// written to the store before the step it describes is taken further.

// errJobCancelled is the cause a job's context ends with when the job is
// cancelled. Any other end is the gateway stopping, after which the job is
// resumed, not finished.
var errJobCancelled = errors.New("the job was cancelled")

// reasonOwnerRevoked is what a job, and each run it stopped, says when it
// was cancelled because its owner holds no active API key any more;
// errOwnerRevoked is the cause its context ends with.
const reasonOwnerRevoked = "cancelled: the owner's access was revoked"

var errOwnerRevoked = fmt.Errorf("%w: %s", errJobCancelled, reasonOwnerRevoked)

// jobManager holds what runs jobs: their contexts, and environments that
// are not sealed (a gateway without a secrets key).
type jobManager struct {
	mu     sync.Mutex
	ctx    context.Context // the gateway's, from Start
	closed bool
	ctls   map[string]*jobCtl
	env    map[string]map[string]string
	wg     sync.WaitGroup
}

type jobCtl struct {
	cancel context.CancelCauseFunc
	done   chan struct{}
}

func newJobManager() *jobManager {
	return &jobManager{ctls: map[string]*jobCtl{}, env: map[string]map[string]string{}}
}

// hold keeps a new job's environment, if it is not sealed, and reports
// whether jobs are being run at all.
func (jm *jobManager) hold(id string, env map[string]string) bool {
	jm.mu.Lock()
	defer jm.mu.Unlock()
	if jm.ctx == nil || jm.closed {
		return false
	}
	if len(env) > 0 {
		jm.env[id] = env
	}
	return true
}

func (jm *jobManager) drop(id string) {
	jm.mu.Lock()
	delete(jm.env, id)
	jm.mu.Unlock()
}

// goJob runs fn in the background unless the manager is closing.
func (jm *jobManager) goJob(fn func()) bool {
	jm.mu.Lock()
	defer jm.mu.Unlock()
	if jm.closed {
		return false
	}
	jm.wg.Add(1)
	go func() { defer jm.wg.Done(); fn() }()
	return true
}

// cancel cancels a running job and waits up to wait for its runs to stop.
func (jm *jobManager) cancel(ctx context.Context, id string, wait time.Duration) {
	jm.mu.Lock()
	ctl := jm.ctls[id]
	jm.mu.Unlock()
	if ctl == nil {
		return
	}
	ctl.cancel(errJobCancelled)
	t := time.NewTimer(wait)
	defer t.Stop()
	select {
	case <-ctl.done:
	case <-t.C:
	case <-ctx.Done():
	}
}

// cancelNow cancels a running job with cause and does not wait: it is
// called from a run's own goroutine as well, which waiting would deadlock.
func (jm *jobManager) cancelNow(id string, cause error) {
	jm.mu.Lock()
	ctl := jm.ctls[id]
	jm.mu.Unlock()
	if ctl != nil {
		ctl.cancel(cause)
	}
}

// close stops starting anything and waits for what runs to stop. The
// gateway's context has been cancelled by then, so a run's goroutine returns
// leaving its run as it was recorded, to be resumed.
func (jm *jobManager) close() {
	jm.mu.Lock()
	jm.closed = true
	jm.mu.Unlock()
	jm.wg.Wait()
}

// startJobs resumes every unfinished job and starts forgetting finished
// ones once kept long enough.
func (g *Gateway) startJobs(ctx context.Context) {
	g.jobs.mu.Lock()
	g.jobs.ctx = ctx
	g.jobs.mu.Unlock()
	for _, rec := range g.store.AllJobs() {
		if rec.State == api.JobRunning {
			g.logf("job %s: resuming", rec.ID)
			g.startJob(rec.ID)
		}
	}
	tick := min(max(g.cfg.JobRetention/4, 10*time.Millisecond), time.Minute)
	g.jobs.goJob(func() {
		t := time.NewTicker(tick)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				g.forgetOldJobs()
			}
		}
	})
}

// forgetOldJobs drops finished jobs kept for JobRetention, and what they kept.
func (g *Gateway) forgetOldJobs() {
	now := time.Now()
	for _, rec := range g.store.AllJobs() {
		if rec.Finished == nil || now.Sub(*rec.Finished) < g.cfg.JobRetention {
			continue
		}
		if err := os.RemoveAll(filepath.Join(g.cfg.JobsDir, rec.ID)); err != nil {
			g.logf("job %s: removing what it kept: %v", rec.ID, err)
			continue
		}
		if err := g.store.ForgetJob(rec.ID); err != nil {
			g.logf("job %s: %v", rec.ID, err)
			continue
		}
		g.jobs.drop(rec.ID)
	}
}

// startJob starts a job's controller, once.
func (g *Gateway) startJob(id string) {
	jm := g.jobs
	jm.mu.Lock()
	defer jm.mu.Unlock()
	if jm.ctx == nil || jm.closed || jm.ctls[id] != nil {
		return
	}
	ctx, cancel := context.WithCancelCause(jm.ctx)
	ctl := &jobCtl{cancel: cancel, done: make(chan struct{})}
	jm.ctls[id] = ctl
	if rec, ok := g.store.Job(id); ok && rec.Cancelled {
		// Asked before a restart: the cancel is finished now.
		cause := errJobCancelled
		if rec.Error == reasonOwnerRevoked {
			cause = errOwnerRevoked
		}
		cancel(cause)
	}
	jm.wg.Add(1)
	go func() {
		defer jm.wg.Done()
		defer func() {
			jm.mu.Lock()
			delete(jm.ctls, id)
			jm.mu.Unlock()
			cancel(nil)
			close(ctl.done)
		}()
		g.runJob(ctx, id)
	}()
}

// revokeOrphanedJobs cancels every running job whose owner holds no active
// API key in its tenant any more (userActive). A job runs in its owner's
// name long after the request that made it, and before this a job outlived
// the revocation of its owner's keys: its running sandboxes kept running
// and its queued runs went on making new ones, for up to a week each.
//
// The cancel is the one DELETE /v1/jobs/{id} makes: recorded first, so a
// restarted gateway finishes it, and then each running run's sandbox
// terminated and the queued runs never started. Called on every
// revocation (accessChanged), at Start, and every AccessRecheckInterval.
func (g *Gateway) revokeOrphanedJobs() {
	active := map[Owner]bool{}
	for id, o := range g.store.runningJobs() {
		ok, seen := active[o]
		if !seen {
			ok = userActive(g.store, o.User, o.Tenant)
			active[o] = ok
		}
		if !ok {
			g.revokeJob(id, o)
		}
	}
}

// revokeJob cancels job id because its owner o lost their access.
func (g *Gateway) revokeJob(id string, o Owner) {
	first := false
	if err := g.store.UpdateJob(id, func(j *jobRecord) {
		if j.State != api.JobRunning || j.Cancelled {
			return
		}
		first = true
		j.Cancelled = true
		if j.Error == "" {
			j.Error = reasonOwnerRevoked
		}
	}); err != nil {
		g.logf("job %s: recording its cancel: %v", id, err)
		// Cancelled all the same: the access is gone whether or not the
		// record says so, and a restarted gateway rechecks at Start.
	}
	g.jobs.cancelNow(id, errOwnerRevoked)
	if !first {
		return
	}
	g.logf("job %s: cancelled: user %q of tenant %q holds no active API key", id, o.User, o.Tenant)
	g.audit.write(api.AuditEntry{Kind: "job", Action: "job.revoked", User: o.User, Tenant: o.Tenant,
		Target: id, Result: "cancelled"})
}

// cancelledOutcome is a run stopped by its job's cancel, saying why when the
// cause was the owner's revocation.
func cancelledOutcome(ctx context.Context) outcome {
	o := outcome{state: api.RunCancelled}
	if errors.Is(context.Cause(ctx), errOwnerRevoked) {
		o.err = reasonOwnerRevoked
	}
	return o
}

// stopping reports how a run's context ended: cancelled (the job was), or
// interrupted (the gateway is stopping, and the run is left to resume).
func stopping(ctx context.Context) (cancelled, interrupted bool) {
	if ctx.Err() == nil {
		return false, false
	}
	if errors.Is(context.Cause(ctx), errJobCancelled) {
		return true, false
	}
	return false, true
}

func (g *Gateway) runJob(ctx context.Context, id string) {
	rec, ok := g.store.Job(id)
	if !ok || rec.State != api.JobRunning {
		return
	}
	env, envErr := g.jobEnv(rec)

	// Runs already under way first: their sandboxes hold room and quota.
	var pending []int
	for _, run := range rec.Runs {
		if run.State == api.RunRunning {
			pending = append(pending, run.N)
		}
	}
	for _, run := range rec.Runs {
		if run.State == api.RunQueued {
			pending = append(pending, run.N)
		}
	}
	sem := make(chan struct{}, rec.Spec.Parallelism)
	var wg sync.WaitGroup
loop:
	for _, n := range pending {
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			break loop
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			g.runOne(ctx, rec, env, envErr, n)
		}()
	}
	wg.Wait()
	if _, interrupted := stopping(ctx); interrupted {
		return
	}
	g.finishJob(id)
}

// jobEnv opens a job's environment: sealed in the store, or held in memory.
func (g *Gateway) jobEnv(rec *jobRecord) (map[string]string, error) {
	if len(rec.EnvNames) == 0 {
		return nil, nil
	}
	if len(rec.EnvSealed) > 0 {
		if g.sealer == nil {
			return nil, errors.New("the job's environment is sealed, and this gateway has no secrets key to open it")
		}
		plain, err := g.sealer.open(rec.EnvSealed, jobEnvAD(rec.ID))
		if err != nil {
			return nil, errors.New("the job's environment does not open with this gateway's secrets key")
		}
		var env map[string]string
		if err := json.Unmarshal(plain, &env); err != nil {
			return nil, errors.New("the job's environment is corrupt")
		}
		return env, nil
	}
	g.jobs.mu.Lock()
	env, ok := g.jobs.env[rec.ID]
	g.jobs.mu.Unlock()
	if !ok {
		return nil, errors.New("the job's environment was held in memory (the gateway has no secrets key) and was lost when the gateway restarted")
	}
	return env, nil
}

// finishJob decides the job's state once no run is in flight.
func (g *Gateway) finishJob(id string) {
	var final *jobRecord
	now := time.Now().UTC()
	err := g.store.UpdateJob(id, func(j *jobRecord) {
		failed := false
		for i := range j.Runs {
			r := &j.Runs[i]
			if !runFinal(r.State) {
				// Never started, or stopped by the cancel.
				r.State = api.RunCancelled
				if r.Finished == nil {
					r.Finished = &now
				}
				if r.Error == "" && j.Error == reasonOwnerRevoked {
					r.Error = j.Error
				}
			}
			if r.State != api.RunSucceeded {
				failed = true
			}
		}
		// A cancel that came after the last run had succeeded cancelled
		// nothing.
		switch {
		case j.Cancelled && failed:
			j.State = api.JobCancelled
		case failed:
			j.State = api.JobFailed
		default:
			j.State = api.JobSucceeded
		}
		j.Finished = &now
		final = j.clone()
	})
	if err != nil {
		g.logf("job %s: recording its end: %v", id, err)
		return
	}
	g.jobs.drop(id)
	g.logf("job %s: %s", id, final.State)
	g.notify(final, api.JobNotification{Event: "job.finished"})
}

// runOne makes run n's attempts until one succeeds, the retries are spent,
// the job is cancelled or the gateway stops.
func (g *Gateway) runOne(ctx context.Context, rec *jobRecord, env map[string]string, envErr error, n int) {
	for {
		run, ok := g.currentRun(rec.ID, n)
		if !ok {
			return
		}
		out := g.attempt(ctx, rec, env, envErr, run)
		if out.interrupted {
			return
		}
		now := time.Now().UTC()
		updated, err := g.store.UpdateRun(rec.ID, n, func(r *api.JobRun) {
			if out.counted {
				r.Attempts++
			}
			retry := (out.state == api.RunFailed || out.state == api.RunTimedOut) && !out.final &&
				r.Attempts <= rec.Spec.Retries && ctx.Err() == nil
			r.ExitCode, r.Error, r.OutputTruncated, r.Files = out.exit, out.err, out.truncated, out.files
			r.PID = 0
			if retry {
				r.State, r.Finished = api.RunQueued, nil
			} else {
				r.State, r.Finished = out.state, &now
			}
		})
		if err != nil {
			g.logf("job %s run %d: recording its attempt: %v", rec.ID, n, err)
			return
		}
		if updated.State != api.RunQueued {
			if j, ok := g.store.Job(rec.ID); ok {
				g.notify(j, api.JobNotification{Event: "run.finished", Run: &n, State: updated.State, ExitCode: updated.ExitCode})
			}
			return
		}
		g.logf("job %s run %d: attempt %d %s; retrying", rec.ID, n, updated.Attempts, out.state)
	}
}

func (g *Gateway) currentRun(id string, n int) (api.JobRun, bool) {
	j, ok := g.store.Job(id)
	if !ok || n >= len(j.Runs) {
		return api.JobRun{}, false
	}
	return j.Runs[n], true
}

// outcome is what one attempt came to.
type outcome struct {
	state     string
	exit      *int
	err       string
	truncated bool
	files     []api.JobFile
	// counted: the attempt was not yet counted when it was recorded running
	// (a create that failed for good).
	counted bool
	// final: retrying cannot help (the job's environment is lost, a secret
	// is gone).
	final bool
	// interrupted: the gateway is stopping; the run is left as recorded.
	interrupted bool
}

func failed(msg string) outcome { return outcome{state: api.RunFailed, err: msg} }

// attempt makes one attempt at a run, or carries on with the one a stopped
// gateway left under way.
func (g *Gateway) attempt(ctx context.Context, rec *jobRecord, env map[string]string, envErr error, run api.JobRun) outcome {
	n := run.N
	if run.State == api.RunRunning && run.Sandbox != "" {
		if run.PID == 0 || run.Started == nil {
			// The sandbox was made and the command never recorded as started.
			g.terminateRunSandbox(run.Sandbox)
			return failed("the gateway stopped between making the run's sandbox and starting its command")
		}
		nd, ok := g.nodeOfSandbox(run.Sandbox)
		if !ok {
			return failed("the run's sandbox ended while the gateway was not watching it")
		}
		return g.watch(ctx, rec, n, nd, run.Sandbox, run.PID, *run.Started)
	}
	if c, i := stopping(ctx); c {
		return cancelledOutcome(ctx)
	} else if i {
		return outcome{interrupted: true}
	}
	// Every new sandbox is made in the owner's name, so the owner is checked
	// before each, not only when a revocation is noticed: a run that reaches
	// this between the revocation and the recheck makes nothing.
	if !userActive(g.store, rec.User, rec.Tenant) {
		g.revokeJob(rec.ID, rec.owner())
		return outcome{state: api.RunCancelled, err: reasonOwnerRevoked}
	}
	if envErr != nil {
		return outcome{state: api.RunFailed, err: envErr.Error(), final: true, counted: true}
	}
	secrets, err := g.secretValues(rec.Tenant, rec.Spec.Secrets)
	if err != nil {
		return outcome{state: api.RunFailed, err: err.Error(), final: true, counted: true}
	}
	req, argv, cwd := g.runRequest(rec, n, env, secrets)
	extra := map[string]string{LabelJob: rec.ID, LabelJobRun: strconv.Itoa(n)}
	p := Principal{User: rec.User, Tenant: rec.Tenant, Scopes: []string{ScopeCreate}}

	// A create refused for want of room — the tenant's quota, the fleet's
	// capacity — waits, holding the run's turn, rather than spending an
	// attempt: a job wider than its quota is a job that queues.
	var sb api.Sandbox
	wait := 200 * time.Millisecond
	for {
		var f *createFail
		sb, f = g.create(ctx, p, req, extra)
		if f.status == http.StatusCreated {
			break
		}
		if c, i := stopping(ctx); c {
			return cancelledOutcome(ctx)
		} else if i {
			return outcome{interrupted: true}
		}
		if !f.transient {
			o := failed("creating the sandbox: " + f.err().Error())
			o.counted = true
			return o
		}
		select {
		case <-ctx.Done():
		case <-time.After(wait):
		}
		wait = min(wait*2, 30*time.Second)
	}
	// From here the run has a sandbox to take down whatever happens.
	if _, err := g.store.UpdateRun(rec.ID, n, func(r *api.JobRun) {
		r.Attempts++
		r.State, r.Sandbox, r.PID = api.RunRunning, sb.ID, 0
		r.Started, r.Finished, r.ExitCode, r.Error = nil, nil, nil, ""
	}); err != nil {
		g.terminateRunSandbox(sb.ID)
		return failed("recording the run failed")
	}
	nd, ok := g.nodeOfSandbox(sb.ID)
	if !ok {
		return failed("the run's node is not answering")
	}
	proc, err := nd.client.StartProcess(ctx, sb.ID, api.RunRequest{Argv: argv, Cwd: cwd})
	if err != nil {
		if c, i := stopping(ctx); i {
			// Left with a sandbox and no recorded command: resumed as such.
			return outcome{interrupted: true}
		} else if c {
			g.terminateRunSandbox(sb.ID)
			return cancelledOutcome(ctx)
		}
		g.terminateRunSandbox(sb.ID)
		return failed("starting the command: " + err.Error())
	}
	started := time.Now().UTC()
	if _, err := g.store.UpdateRun(rec.ID, n, func(r *api.JobRun) {
		r.PID, r.Started = proc.PID, &started
	}); err != nil {
		g.terminateRunSandbox(sb.ID)
		return failed("recording the run failed")
	}
	return g.watch(ctx, rec, n, nd, sb.ID, proc.PID, started)
}

// runRequest is the create request, argv and working directory of run n.
func (g *Gateway) runRequest(rec *jobRecord, n int, env, secrets map[string]string) (api.CreateSandboxRequest, []string, string) {
	s := rec.Spec
	req := api.CreateSandboxRequest{Image: s.Image, SnapshotID: s.FromSnapshot, Network: s.Network}
	if s.Resources != nil {
		req.CPUs, req.MemoryMB, req.DiskMB = s.Resources.CPUs, s.Resources.MemoryMB, s.Resources.DiskMB
	}
	req.Env = make(map[string]string, len(env)+len(secrets)+len(gitIdentity))
	// git refuses a commit without an identity and the image sets none; a
	// neutral one, as the CLI's run gives, unless the job set its own.
	for k, v := range gitIdentity {
		req.Env[k] = v
	}
	for k, v := range env {
		req.Env[k] = v
	}
	for k, v := range secrets {
		req.Env[k] = v
	}
	if s.Agent == "" {
		return req, slices.Clone(s.Command), ""
	}
	d, _ := agents.Lookup(s.Agent)
	prompt := s.Prompt
	if len(s.Prompts) > 0 {
		prompt = s.Prompts[n]
	}
	// The agent's API, added to an allowlist that lacks it, as the CLI does
	// for a run: an agent that cannot reach its provider fails every run.
	if d.ProviderHost != "" {
		net := req.Network
		if net == nil {
			if caps, ok := g.combinedCapabilities(); ok && caps.Network.Default.Mode == api.NetworkAllowlist {
				def := caps.Network.Default
				net = &api.NetworkPolicy{Mode: def.Mode, Allow: slices.Clone(def.Allow), Deny: slices.Clone(def.Deny)}
			}
		}
		if net != nil && net.Mode == api.NetworkAllowlist && !slices.Contains(net.Allow, d.ProviderHost) {
			c := *net
			c.Allow = append(slices.Clone(net.Allow), d.ProviderHost)
			net = &c
		}
		req.Network = net
	}
	return req, d.Autonomous(prompt, nil), agenthome.GuestHome
}

var gitIdentity = map[string]string{
	"GIT_AUTHOR_NAME": "sandbox", "GIT_AUTHOR_EMAIL": "sandbox@localhost",
	"GIT_COMMITTER_NAME": "sandbox", "GIT_COMMITTER_EMAIL": "sandbox@localhost",
}

func (g *Gateway) nodeOfSandbox(id string) (*node, bool) {
	o, ok := g.store.OwnerOf(id)
	if !ok {
		return nil, false
	}
	n := g.nodes.get(o.Node)
	return n, n != nil
}

// lostAfter is how long a run's node may go unanswering before the attempt
// is given up as failed.
var lostAfter = 2 * time.Minute

// watch follows a run's process to its end or its deadline, then keeps
// what the job asked for and terminates the sandbox.
func (g *Gateway) watch(ctx context.Context, rec *jobRecord, n int, nd *node, sandbox string, pid int, started time.Time) outcome {
	deadline := started.Add(time.Duration(rec.Spec.TimeoutSecs) * time.Second)
	fctx, cancel := context.WithDeadline(ctx, deadline)
	defer cancel()
	var out outcome
	var stdout, stderr capped
	var code *int
	var lostSince time.Time
	for {
		stdout, stderr, code = capped{max: maxKeptOutput}, capped{max: maxKeptOutput}, nil
		err := nd.client.FollowOutput(fctx, sandbox, pid, func(ev api.OutputEvent) error {
			switch {
			case ev.ExitCode != nil:
				c := *ev.ExitCode
				code = &c
			case ev.Stream == "stdout":
				stdout.Write(ev.Data)
			case ev.Stream == "stderr":
				stderr.Write(ev.Data)
			}
			return nil
		})
		if code != nil {
			break
		}
		if c, i := stopping(ctx); i {
			return outcome{interrupted: true}
		} else if c {
			g.terminateRunSandbox(sandbox)
			return cancelledOutcome(ctx)
		}
		if fctx.Err() != nil {
			// The deadline: the process is killed, and what it wrote kept.
			kctx, kc := context.WithTimeout(context.Background(), 10*time.Second)
			_ = nd.client.Signal(kctx, sandbox, pid, "KILL")
			kc()
			out.state = api.RunTimedOut
			out.err = fmt.Sprintf("timed out after %ds", rec.Spec.TimeoutSecs)
			break
		}
		if api.IsCode(err, api.CodeNotFound) {
			g.terminateRunSandbox(sandbox)
			return failed("the run's sandbox or its process is gone")
		}
		if lostSince.IsZero() {
			lostSince = time.Now()
		} else if time.Since(lostSince) > lostAfter {
			g.terminateRunSandbox(sandbox)
			return failed("lost the run's node")
		}
		select {
		case <-fctx.Done():
		case <-time.After(time.Second):
		}
	}
	if code != nil {
		out.exit = code
		out.state = api.RunSucceeded
		if *code != 0 {
			out.state = api.RunFailed
			out.err = "exited " + strconv.Itoa(*code)
		}
	}
	out.truncated = stdout.cut || stderr.cut
	dir := g.runDir(rec.ID, n)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		g.logf("job %s: %v", rec.ID, err)
	}
	if keepOutput(rec.Spec) {
		for name, b := range map[string][]byte{"stdout": stdout.buf.Bytes(), "stderr": stderr.buf.Bytes()} {
			if err := writeAtomic(filepath.Join(dir, name), b); err != nil {
				g.logf("job %s: keeping %s: %v", rec.ID, name, err)
			}
		}
	}
	if rec.Spec.Keep != nil {
		out.files = g.keepFiles(nd, sandbox, dir, rec.Spec.Keep.Files)
	}
	g.terminateRunSandbox(sandbox)
	return out
}

// keepFiles reads the files a job keeps out of a run's sandbox, each capped.
func (g *Gateway) keepFiles(nd *node, sandbox, dir string, paths []string) []api.JobFile {
	var out []api.JobFile
	for i, p := range paths {
		f := api.JobFile{Path: p}
		ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
		resp, err := nd.do(ctx, http.MethodGet, "/v1/sandboxes/"+url.PathEscape(sandbox)+"/files", url.Values{"path": {p}}, nil, "")
		switch {
		case err != nil:
			f.Error = "the node did not answer"
		case resp.StatusCode != http.StatusOK:
			f.Error = readAPIError(resp).Error()
		default:
			data, rerr := io.ReadAll(io.LimitReader(resp.Body, maxKeptFile+1))
			resp.Body.Close()
			if rerr != nil {
				f.Error = "reading it failed"
				break
			}
			if len(data) > maxKeptFile {
				data, f.Truncated = data[:maxKeptFile], true
			}
			f.Size = int64(len(data))
			if werr := writeAtomic(filepath.Join(dir, "file-"+strconv.Itoa(i)), data); werr != nil {
				g.logf("keeping %s: %v", p, werr)
				f.Error = "the gateway could not keep it"
			}
		}
		cancel()
		out = append(out, f)
	}
	return out
}

// terminateRunSandbox takes a run's sandbox down and forgets it, as a
// DELETE through the gateway does.
func (g *Gateway) terminateRunSandbox(id string) {
	o, ok := g.store.OwnerOf(id)
	if !ok {
		return
	}
	n := g.nodes.get(o.Node)
	if n == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	resp, err := n.do(ctx, http.MethodDelete, "/v1/sandboxes/"+url.PathEscape(id), nil, nil, "")
	if err != nil {
		g.logf("terminating %s: %v", id, err)
		return
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusNoContent || resp.StatusCode == http.StatusNotFound {
		g.tombs.add(id, o)
		if err := g.store.ForgetSandbox(id); err != nil {
			g.logf("forgetting %s: %v", id, err)
		}
	}
}

// capped is a buffer that keeps its first max bytes and notes the rest.
type capped struct {
	buf bytes.Buffer
	max int
	cut bool
}

func (c *capped) Write(p []byte) {
	room := c.max - c.buf.Len()
	if len(p) > room {
		p, c.cut = p[:max(room, 0)], true
	}
	c.buf.Write(p)
}

// --- notifications ----------------------------------------------------------------

// newNotifyClient is what posts notifications: bounded in time, and never
// following a redirect, which would let the receiver point the gateway
// somewhere the job's owner never named.
//
// Unless allowPrivate, it connects to public addresses only. A notify URL
// is the job owner's text and the gateway sits on the operator's network,
// so without this any user could make it open connections to the gateway's
// own loopback, the nodes' network or a cloud metadata address — a port
// scan by timing at the least. The address is checked as it is dialled,
// after the name is resolved, so a name that resolves to a private address
// (or is made to, between the check at submit time and the post) is refused
// too. No proxy is used: one would be dialled instead, and the check would
// be of the proxy.
func newNotifyClient(allowPrivate bool) *http.Client {
	d := &net.Dialer{Timeout: 10 * time.Second}
	if !allowPrivate {
		d.Control = func(_, address string, _ syscall.RawConn) error {
			host, _, err := net.SplitHostPort(address)
			if err != nil {
				return err
			}
			if ip, err := netip.ParseAddr(host); err != nil || !publicAddr(ip) {
				return fmt.Errorf("notify: %s is not a public address (sandbox-gateway serve --notify-allow-private allows it)", host)
			}
			return nil
		}
	}
	return &http.Client{
		Timeout:       10 * time.Second,
		Transport:     &http.Transport{DialContext: d.DialContext, TLSHandshakeTimeout: 10 * time.Second, MaxIdleConns: 4, IdleConnTimeout: 30 * time.Second},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// cgnat is the shared address space (RFC 6598), private in all but name.
var cgnat = netip.MustParsePrefix("100.64.0.0/10")

// publicAddr reports whether ip is an address on the public internet: not
// loopback, private, link-local (169.254.169.254 among them), shared,
// unspecified or multicast.
func publicAddr(ip netip.Addr) bool {
	ip = ip.Unmap()
	return ip.IsValid() && ip.IsGlobalUnicast() && !ip.IsPrivate() && !cgnat.Contains(ip)
}

// notify posts n to the job's notify URL: three tries at most, a failure
// logged and dropped. What is sent is the job's id and name and states —
// never output, never an environment value or a secret.
func (g *Gateway) notify(j *jobRecord, n api.JobNotification) {
	if j.Spec.Notify == "" {
		return
	}
	n.Job, n.Name, n.JobState = j.ID, j.Spec.Name, j.State
	body, err := json.Marshal(n)
	if err != nil {
		return
	}
	target := j.Spec.Notify
	g.jobs.goJob(func() {
		g.jobs.mu.Lock()
		base := g.jobs.ctx
		g.jobs.mu.Unlock()
		for try := 0; try < 3; try++ {
			if try > 0 {
				select {
				case <-base.Done():
					return
				case <-time.After(time.Duration(try) * time.Second):
				}
			}
			req, err := http.NewRequestWithContext(base, http.MethodPost, target, bytes.NewReader(body))
			if err != nil {
				return
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("User-Agent", "sandbox-gateway")
			resp, err := g.notifyClient.Do(req)
			if err == nil {
				_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
				resp.Body.Close()
				if resp.StatusCode < 300 {
					return
				}
				err = fmt.Errorf("answered %d", resp.StatusCode)
			}
			// Without the URL, which a client error quotes whole: a hook's
			// URL commonly carries its credential in the path or the query,
			// and it is the job owner's, not the operator's log's.
			var ue *url.Error
			if errors.As(err, &ue) {
				err = ue.Err
			}
			g.logf("job %s: notify (%s): %v", j.ID, n.Event, err)
		}
	})
}

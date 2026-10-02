package fleet

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/workspace"
)

// A fleet is one agent per branch, each in a sandbox of its own, run over the
// API: every task is a sandbox created, cloned into, run headless with its
// verify wrapped round the agent, and brought back into
// refs/sandbox/fleet/<branch>. Nothing here is a second isolation policy — a
// fleet task is the same create request a `run` makes, so the boundary is the
// server's either way.
//
// The beta.15 fleet ran agents in worktrees of the host repository; that model
// is gone with the bind mount. Work now lands from refs, and `land` merges a
// ref, never a directory the agent could still be writing.

// Task states.
const (
	TaskRunning  = "running"
	TaskVerified = "verified" // exited 0
	TaskFailed   = "failed"   // exited non-zero, not the verify code
	TaskRejected = "rejected" // its verify said the work is not done
	TaskLost     = "lost"     // the sandbox or the connection to it went away
)

// TaskState is what a fleet run recorded about one task.
type TaskState struct {
	Branch   string `json:"branch"`
	Agent    string `json:"agent"`
	Sandbox  string `json:"sandbox"`
	State    string `json:"state"`
	ExitCode int    `json:"exit_code"`     // -1: the agent never ran, or its exit was not seen
	Ref      string `json:"ref,omitempty"` // empty: nothing new came back
	Log      string `json:"log"`
	Error    string `json:"error,omitempty"`
}

// State is a fleet run's record, kept outside the repository.
type State struct {
	Repo       string                `json:"repo"`
	BaseBranch string                `json:"base_branch"`
	BaseCommit string                `json:"base_commit"`
	StartedAt  time.Time             `json:"started_at"`
	Tasks      map[string]*TaskState `json:"tasks"`
}

// Unfinished names the tasks that did not verify, in no particular order. A
// fleet run that leaves any is a failure, whatever else landed.
func (s *State) Unfinished() []string {
	var out []string
	for b, t := range s.Tasks {
		if t.State != TaskVerified {
			out = append(out, b)
		}
	}
	return out
}

// StatePath is where a repository's latest fleet run is recorded.
func StatePath(repo string) string {
	h := sha256.Sum256([]byte(repo))
	return filepath.Join(workspace.ConfigDir(), "fleet", hex.EncodeToString(h[:8]), "state.json")
}

// LoadState reads a repository's latest fleet run.
func LoadState(repo string) (*State, error) {
	b, err := os.ReadFile(StatePath(repo))
	if err != nil {
		return nil, err
	}
	var s State
	return &s, json.Unmarshal(b, &s)
}

func (s *State) save(mu *sync.Mutex) error {
	mu.Lock()
	b, _ := json.MarshalIndent(s, "", "  ")
	mu.Unlock()
	p := StatePath(s.Repo)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	tmp := p + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Runner runs fleets against one endpoint.
type Runner struct {
	Client *api.Client
	Repo   string
	// PersistLogins restores and saves each agent's login, as a wrapper does.
	// Off under the prod profile.
	PersistLogins bool
	// Keep leaves each task's sandbox running when it finishes.
	Keep bool
	Out  io.Writer // progress lines
}

var nameUnsafe = regexp.MustCompile(`[^a-z0-9-]+`)

// sandboxName turns a branch into a sandbox name: a fleet's sandboxes are
// findable in `list` by the branch they work on.
func sandboxName(branch string) string {
	n := strings.Trim(nameUnsafe.ReplaceAllString(strings.ToLower(branch), "-"), "-")
	if len(n) > 40 {
		n = n[:40]
	}
	return "fleet-" + n
}

// Run runs every task, at most spec.Concurrency() at a time, and returns once
// all have finished. The state file is written as each task changes.
func (r *Runner) Run(ctx context.Context, spec Spec) (*State, error) {
	if err := spec.Validate(); err != nil {
		return nil, err
	}
	base, err := workspace.Git(r.Repo, "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("the repository has no commit to start from: %w", err)
	}
	branch, _ := workspace.Git(r.Repo, "symbolic-ref", "--quiet", "--short", "HEAD")
	st := &State{Repo: r.Repo, BaseBranch: branch, BaseCommit: base, StartedAt: time.Now().UTC(), Tasks: map[string]*TaskState{}}
	var mu sync.Mutex
	logDir := filepath.Join(filepath.Dir(StatePath(r.Repo)), "logs")
	if err := os.MkdirAll(logDir, 0o700); err != nil {
		return nil, err
	}
	for _, t := range spec.Tasks {
		st.Tasks[t.Branch] = &TaskState{Branch: t.Branch, Agent: spec.AgentFor(t), State: TaskRunning, ExitCode: -1,
			Log: filepath.Join(logDir, sandboxName(t.Branch)+".log")}
	}
	if err := st.save(&mu); err != nil {
		return nil, err
	}

	caps, err := r.Client.Capabilities(ctx)
	if err != nil {
		return nil, err
	}
	slots := make(chan struct{}, spec.Concurrency())
	var wg sync.WaitGroup
	for _, t := range spec.Tasks {
		wg.Add(1)
		go func(t Task) {
			defer wg.Done()
			slots <- struct{}{}
			defer func() { <-slots }()
			ts := st.Tasks[t.Branch]
			r.runTask(ctx, spec, t, caps, ts, &mu)
			mu.Lock()
			line := fmt.Sprintf("%-24s %-9s exit %d", t.Branch, ts.State, ts.ExitCode)
			if ts.Ref != "" {
				line += "  " + ts.Ref
			}
			if ts.Error != "" {
				line += "  (" + ts.Error + ")"
			}
			mu.Unlock()
			fmt.Fprintln(r.Out, line)
			_ = st.save(&mu)
		}(t)
	}
	wg.Wait()
	return st, st.save(&mu)
}

func (r *Runner) runTask(ctx context.Context, spec Spec, t Task, caps api.Capabilities, ts *TaskState, mu *sync.Mutex) {
	fail := func(state string, err error) {
		mu.Lock()
		ts.State, ts.Error = state, err.Error()
		mu.Unlock()
	}
	d, ok := agents.Lookup(spec.AgentFor(t))
	if !ok {
		fail(TaskFailed, fmt.Errorf("unknown agent %q", spec.AgentFor(t)))
		return
	}
	lim := spec.LimitsFor(t)
	req := api.CreateSandboxRequest{Name: sandboxName(t.Branch), Env: agentEnv(d)}
	req.MemoryMB, req.CPUs = parseMemoryMiB(lim.Memory), parseCPUs(lim.CPUs)
	if len(lim.Allow) > 0 {
		req.Network = &api.NetworkPolicy{Mode: api.NetworkAllowlist,
			Allow: append(append([]string{}, caps.Network.Default.Allow...), lim.Allow...)}
	}
	sb, err := r.Client.CreateSandbox(ctx, req)
	if err != nil {
		fail(TaskFailed, err)
		return
	}
	mu.Lock()
	ts.Sandbox = sb.ID
	mu.Unlock()
	if !r.Keep {
		defer r.Client.TerminateSandbox(context.Background(), sb.ID)
	}
	base, err := workspace.CloneIn(ctx, r.Client, sb.ID, r.Repo)
	if err != nil {
		fail(TaskFailed, err)
		return
	}
	if r.PersistLogins {
		workspace.RestoreLogin(ctx, r.Client, sb.ID, d)
	}

	argv := withVerify(d.Autonomous(t.Prompt, t.Args), t.Verify)
	p, err := r.Client.StartProcess(ctx, sb.ID, api.RunRequest{Argv: argv, Cwd: "/workspace"})
	if err != nil {
		fail(TaskFailed, err)
		return
	}
	logf, err := os.OpenFile(ts.Log, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o600)
	if err != nil {
		fail(TaskFailed, err)
		return
	}
	code := -1
	err = r.Client.FollowOutput(ctx, sb.ID, p.PID, func(ev api.OutputEvent) error {
		if ev.ExitCode != nil {
			code = *ev.ExitCode
		}
		_, _ = logf.Write(ev.Data)
		return nil
	})
	logf.Close()
	if err != nil {
		fail(TaskLost, err)
		return
	}
	if r.PersistLogins {
		workspace.SaveLogin(context.Background(), r.Client, sb.ID, d)
	}

	sess := workspace.Session{Sandbox: sb.ID, Repo: r.Repo, Base: base, Branch: workspace.SandboxBranch}
	ref, err := workspace.BringBack(context.Background(), r.Client, sess, "fleet/"+t.Branch)
	mu.Lock()
	defer mu.Unlock()
	ts.ExitCode, ts.Ref = code, ref
	switch {
	case err != nil:
		ts.State, ts.Error = TaskFailed, "bringing work back: "+err.Error()
	case code == VerifyFailedExit:
		ts.State = TaskRejected
	case code == 0:
		ts.State = TaskVerified
	default:
		ts.State = TaskFailed
	}
}

// agentEnv is an agent's constant settings and the host values of its
// allowlist that are set.
func agentEnv(d agents.Descriptor) map[string]string {
	env := map[string]string{}
	for _, kv := range d.Env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			env[k] = v
		}
	}
	for _, name := range d.EnvAllow {
		if v, ok := os.LookupEnv(name); ok {
			env[name] = v
		}
	}
	return env
}

// parseMemoryMiB reads a fleet memory cap ("4g", "512m", "2048") as MiB; 0
// means the server's default.
func parseMemoryMiB(s string) int {
	s = strings.ToLower(strings.TrimSpace(s))
	mult := 1
	switch {
	case strings.HasSuffix(s, "g"):
		mult, s = 1024, strings.TrimSuffix(s, "g")
	case strings.HasSuffix(s, "m"):
		s = strings.TrimSuffix(s, "m")
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 {
		return 0
	}
	return n * mult
}

func parseCPUs(s string) float64 {
	f, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
	if err != nil || f < 0 {
		return 0
	}
	return f
}

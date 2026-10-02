package cli

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/policy"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
	"github.com/Amitgb14/sandbox-cli/internal/workspace"
)

// runFlags are the sandbox options shared by `run` and every agent wrapper.
type runFlags struct {
	context       string
	image         string
	cpus          float64
	memory, disk  int
	idle          int
	network       string
	allow, deny   []string
	env           []string
	bind          string
	bindReadOnly  bool
	noWorkspace   bool
	detach, keep  bool
	name          string
	noPersistAuth bool
	project       string
	noBringBack   bool
	fromSnapshot  string
	profile       string
	configPath    string
	fallback      []string
	checkpoint    time.Duration
	labels        []string
}

func (rf *runFlags) register(cmd *cobra.Command) {
	f := cmd.Flags()
	f.StringVar(&rf.context, "context", "", "which sandboxd to use (sandbox-cli context ls)")
	f.StringVar(&rf.image, "image", "", "image to run (default: the server's)")
	f.Float64Var(&rf.cpus, "cpus", 0, "vCPUs")
	f.IntVar(&rf.memory, "memory", 0, "memory in MiB")
	f.IntVar(&rf.disk, "disk", 0, "writable disk in MiB")
	f.IntVar(&rf.idle, "idle", 0, "terminate after this many idle seconds (default: the server's)")
	f.StringVar(&rf.network, "network", "", "none, allowlist or open (default: the server's)")
	f.StringArrayVar(&rf.allow, "allow", nil, "also allow egress to this host (repeatable; implies allowlist)")
	f.StringArrayVar(&rf.deny, "deny", nil, "refuse egress to this host even if allowed (repeatable)")
	f.StringArrayVarP(&rf.env, "env", "e", nil, "KEY=VALUE, or KEY to forward the host's value (repeatable)")
	f.StringVar(&rf.bind, "bind", "", "mount this directory at /workspace instead of cloning (local endpoints)")
	f.BoolVar(&rf.bindReadOnly, "bind-read-only", false, "mount --bind read-only")
	f.BoolVar(&rf.noWorkspace, "no-workspace", false, "start with an empty /workspace")
	f.BoolVarP(&rf.detach, "detach", "d", false, "start, print how to attach, and return")
	f.BoolVar(&rf.keep, "keep", false, "keep the sandbox when the command ends")
	f.StringVar(&rf.name, "name", "", "name the sandbox")
	f.BoolVar(&rf.noPersistAuth, "no-persist-auth", false, "do not restore or save the agent's login")
	f.StringVar(&rf.project, "project", "", "repository to work on (default: the current directory)")
	f.BoolVar(&rf.noBringBack, "no-bring-back", false, "do not fetch the sandbox's commits when the command ends")
	f.StringVar(&rf.profile, "profile", "", "dev or prod (prod: no persisted logins)")
	f.StringVar(&rf.fromSnapshot, "from-snapshot", "", "start from a snapshot (sandbox-cli snapshot) instead of the image")
	f.StringVar(&rf.configPath, "config", "", "an explicit config file, trusted like your own")
	f.DurationVar(&rf.checkpoint, "checkpoint-every", 5*time.Minute, "fetch the sandbox's working tree to refs/sandbox/checkpoints/<id> this often while attached, so a dead VM loses minutes rather than the run (0: never)")
	f.StringArrayVar(&rf.labels, "label", nil, "label the sandbox, key=value (repeatable); shown by list and recorded in its audit events")
	f.StringArrayVar(&rf.fallback, "fallback", nil, "an agent to try next if this one's provider is down or it fails having changed nothing (repeatable; agent wrappers only)")
}

// sandboxFlagNames lists the long flags a wrapper consumes before handing the
// rest to the agent, and whether each takes a value.
func sandboxFlagNames() map[string]bool {
	rf := &runFlags{}
	cmd := &cobra.Command{}
	rf.register(cmd)
	out := map[string]bool{}
	cmd.Flags().VisitAll(func(fl *pflag.Flag) {
		out[fl.Name] = fl.Value.Type() != "bool"
	})
	return out
}

// runSpec is one run: a command, and when it is an agent's, the agent.
type runSpec struct {
	argv  []string
	agent *agents.Descriptor
	// before runs once the workspace and login are in place, before the
	// command starts; after runs once it has exited, before the sandbox goes.
	before, after func(ctx context.Context, c *api.Client, sandbox string)
	// result, when set, is filled in with what the run did.
	result *runResult
	// labels are added to the sandbox's, over any --label of the same key:
	// they are what sandbox-cli itself knows about the run (the agent, the
	// routing attempt), and a user label should not be able to disguise one
	// routing attempt as another.
	labels map[string]string
}

// runResult is what routing needs to know about a run that has ended.
type runResult struct {
	// changed reports whether the workspace ended differently from how it
	// began; nil when that could not be determined.
	changed *bool
}

// execute is a var so routing's tests can script what each attempt did; a
// real attempt needs a guest that runs git.
var execute = runSandbox

// runSandbox is the whole of a run: create, set up the workspace, restore the
// login, attach, and on the way out save the login, bring work back and clean
// up. It returns the command's exit code.
func runSandbox(ctx context.Context, rf *runFlags, rs runSpec) (int, error) {
	c, ctxName, err := newClient(rf.context)
	if err != nil {
		return 1, err
	}
	caps, err := c.Capabilities(ctx)
	if err != nil {
		return 1, fmt.Errorf("cannot reach sandboxd (%s): %w", ctxName, err)
	}

	req := api.CreateSandboxRequest{
		Name: rf.name, Image: rf.image, CPUs: rf.cpus, MemoryMB: rf.memory, DiskMB: rf.disk,
		IdleTimeoutSecs: rf.idle, SnapshotID: rf.fromSnapshot,
	}
	if req.Env, err = buildEnv(rf.env, rs.agent); err != nil {
		return 1, err
	}
	if req.Labels, err = buildLabels(rf.labels, rs); err != nil {
		return 1, err
	}
	req.Network = buildNetwork(rf, caps)
	project := rf.project
	if project == "" {
		project, _ = os.Getwd()
	}
	if err := applyConfig(rf, project, &req, caps); err != nil {
		return 1, err
	}
	if rf.bind != "" {
		abs, err := filepath.Abs(policy.ExpandTilde(rf.bind))
		if err != nil {
			return 1, err
		}
		req.Bind = &api.Bind{HostPath: abs, ReadOnly: rf.bindReadOnly}
	}

	sb, err := c.CreateSandbox(ctx, req)
	if err != nil {
		return 1, err
	}
	keep := rf.keep || rf.detach
	defer func() {
		if !keep {
			_ = c.TerminateSandbox(context.Background(), sb.ID)
		}
	}()

	var sess *workspace.Session
	if rf.bind == "" && !rf.noWorkspace && rf.fromSnapshot == "" {
		if repo := workspace.RepoRoot(project); repo != "" {
			base, err := workspace.CloneIn(ctx, c, sb.ID, repo)
			if err != nil {
				return 1, err
			}
			sess = &workspace.Session{Sandbox: sb.ID, Context: ctxName, Repo: repo, Base: base, Branch: workspace.SandboxBranch, Started: time.Now().UTC()}
			_ = sess.Save()
		} else {
			fmt.Fprintln(os.Stderr, "sandbox-cli: not in a git repository; /workspace starts empty (use --bind on a local endpoint to mount a directory)")
		}
	}

	persist := rs.agent != nil && !rf.noPersistAuth && len(rs.agent.AuthPaths) > 0
	if persist {
		workspace.RestoreLogin(ctx, c, sb.ID, *rs.agent)
	}

	if rs.before != nil {
		rs.before(ctx, c, sb.ID)
	}

	tty := !rf.detach && isTerminal(os.Stdin) && isTerminal(os.Stdout)
	rows, cols := termSize(os.Stdout)
	p, err := c.StartProcess(ctx, sb.ID, api.RunRequest{Argv: rs.argv, Cwd: "/workspace", Tty: tty, Rows: rows, Cols: cols})
	if err != nil {
		return 1, err
	}
	if rf.detach {
		fmt.Printf("%s\n", sb.ID)
		fmt.Fprintf(os.Stderr, "sandbox-cli: started %s (process %d). Attach: sandbox-cli attach %s · Stop: sandbox-cli kill %s\n",
			sb.ID, p.PID, sb.ID, sb.ID)
		return 0, nil
	}

	stopCheckpoints := func() error { return nil }
	if sess != nil && rf.checkpoint > 0 {
		stopCheckpoints = startCheckpoints(ctx, c, sess, rf.checkpoint)
	}
	code, err := attach(ctx, c, sb.ID, p.PID, tty)
	// Reported only now: while attached, the agent owns the terminal, and a
	// line printed over its UI is a line nobody can read.
	if cerr := stopCheckpoints(); cerr != nil {
		fmt.Fprintf(os.Stderr, "sandbox-cli: checkpoints failed during the run: %v\n", cerr)
	}
	if err != nil {
		return 1, err
	}

	if rs.after != nil {
		rs.after(context.Background(), c, sb.ID)
	}
	if persist {
		workspace.SaveLogin(context.Background(), c, sb.ID, *rs.agent)
	}
	if sess != nil && !rf.noBringBack {
		name := sb.ID
		if rf.name != "" {
			name = rf.name
		}
		ref, err := workspace.BringBack(context.Background(), c, *sess, name)
		switch {
		case err != nil:
			keep = true
			fmt.Fprintf(os.Stderr, "sandbox-cli: bringing work back failed: %v\n  the sandbox is kept: sandbox-cli bring-back %s\n", err, sb.ID)
		case ref == "":
			sess.MarkBroughtBack("")
			fmt.Fprintln(os.Stderr, "sandbox-cli: no new commits to bring back")
			if rs.result != nil {
				rs.result.changed = new(bool)
			}
		default:
			sess.MarkBroughtBack(ref)
			if rs.result != nil {
				changed := true
				rs.result.changed = &changed
			}
			fmt.Fprintf(os.Stderr, "sandbox-cli: work brought back to %s\n  review: git log -p HEAD..%s · merge: git merge %s\n", ref, ref, ref)
		}
	}
	if keep && !rf.detach {
		fmt.Fprintf(os.Stderr, "sandbox-cli: kept %s (sandbox-cli kill %s)\n", sb.ID, sb.ID)
	}
	return code, nil
}

// startCheckpoints takes a checkpoint every interval until the returned
// function is called, which stops it and reports the last failure, if the
// failures were not followed by a success. The session record is updated after
// each one, so a CLI that is itself killed leaves the latest checkpoint
// findable by `recover`.
func startCheckpoints(ctx context.Context, c *api.Client, sess *workspace.Session, every time.Duration) func() error {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() {
		var last error
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				done <- last
				return
			case <-t.C:
			}
			ref, fetched, err := workspace.Checkpoint(ctx, c, *sess)
			switch {
			case err != nil:
				if ctx.Err() == nil {
					last = err
				}
			case fetched:
				last = nil
				sess.Checkpoint, sess.CheckpointAt = ref, time.Now().UTC()
				_ = sess.Save()
			default:
				last = nil
			}
		}
	}()
	return func() error {
		cancel()
		return <-done
	}
}

// attach connects the terminal (or stdin/stdout) to a process until it exits.
func attach(ctx context.Context, c *api.Client, sandbox string, pid int, tty bool) (int, error) {
	st, err := c.Attach(ctx, sandbox, pid)
	if err != nil {
		return 1, err
	}
	defer st.Close()
	if tty {
		restore, err := makeRaw(os.Stdin)
		if err == nil {
			defer restore()
		}
		stop := onResize(func() { r, c := termSize(os.Stdout); _ = st.Resize(r, c) })
		defer stop()
	} else {
		sig := make(chan os.Signal, 1)
		signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
		defer signal.Stop(sig)
		go func() {
			for s := range sig {
				if s == os.Interrupt {
					_ = st.Signal("INT")
				} else {
					_ = st.Signal("TERM")
				}
			}
		}()
	}
	go func() {
		_, _ = io.Copy(st, os.Stdin)
		if !tty {
			_ = st.CloseStdin()
		}
	}()
	return st.Copy(os.Stdout, os.Stderr)
}

// buildEnv is the agent's constant settings, the host values of its allowlist
// that are set, and --env.
func buildEnv(flags []string, agent *agents.Descriptor) (map[string]string, error) {
	env := map[string]string{}
	if agent != nil {
		for _, kv := range agent.Env {
			if k, v, ok := strings.Cut(kv, "="); ok {
				env[k] = v
			}
		}
		for _, name := range agent.EnvAllow {
			if v, ok := os.LookupEnv(name); ok {
				env[name] = v
			}
		}
	}
	for _, e := range flags {
		k, v, hasValue := strings.Cut(e, "=")
		if !hasValue {
			hv, ok := os.LookupEnv(k)
			if !ok {
				continue
			}
			v = hv
		}
		if policy.IsReservedEnv(k) {
			return nil, fmt.Errorf("--env %s: %s", k, policy.ReservedEnvReason())
		}
		env[k] = v
	}
	return env, nil
}

// buildLabels is the sandbox's labels: --label, then what sandbox-cli knows
// about the run. The server validates them.
func buildLabels(flags []string, rs runSpec) (map[string]string, error) {
	out := map[string]string{}
	for _, l := range flags {
		k, v, ok := strings.Cut(l, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("--label %q: want key=value", l)
		}
		out[k] = v
	}
	if rs.agent != nil {
		out["agent"] = rs.agent.Name
	}
	for k, v := range rs.labels {
		out[k] = v
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// formatLabels renders labels for a table cell, sorted, with their text made
// safe to print.
func formatLabels(l map[string]string) string {
	if len(l) == 0 {
		return "-"
	}
	keys := make([]string, 0, len(l))
	for k := range l {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, len(keys))
	for i, k := range keys {
		parts[i] = k + "=" + termsafe.Clean(l[k])
	}
	return strings.Join(parts, ",")
}

// buildNetwork turns the flags into a policy request, or nil for the server's
// default. --allow adds to the server's default list rather than replacing it,
// which is what someone typing one more host means.
func buildNetwork(rf *runFlags, caps api.Capabilities) *api.NetworkPolicy {
	if rf.network == "" && len(rf.allow) == 0 && len(rf.deny) == 0 {
		return nil
	}
	mode := rf.network
	if mode == "" {
		mode = caps.Network.Default.Mode
		if len(rf.allow) > 0 {
			mode = api.NetworkAllowlist
		}
	}
	p := &api.NetworkPolicy{Mode: mode, Deny: rf.deny}
	if mode == api.NetworkAllowlist {
		p.Allow = append(append([]string{}, caps.Network.Default.Allow...), rf.allow...)
	}
	return p
}

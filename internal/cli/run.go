package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sort"
	"strings"
	"sync/atomic"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Amitgb14/sandbox-cli/internal/agenthome"
	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/policy"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
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
	detach, keep  bool
	name          string
	noPersistAuth bool
	fromSnapshot  string
	profile       string
	configPath    string
	fallback      []string
	labels        []string
	volumes       []string

	// project is the directory whose .sandbox.yaml applies; empty is the
	// current directory. Not a flag: Studio sets it, to a directory with no
	// project config, so a run it starts does not pick up whatever
	// .sandbox.yaml sits where Studio happened to be started.
	project string

	// flags is the set these were parsed from, to tell a flag that was given
	// from one left at its default.
	flags *pflag.FlagSet
}

func (rf *runFlags) register(cmd *cobra.Command) {
	f := cmd.Flags()
	rf.flags = f
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
	f.BoolVarP(&rf.detach, "detach", "d", false, "start, print how to attach, and return")
	f.BoolVar(&rf.keep, "keep", false, "keep the sandbox when the command ends")
	f.StringVar(&rf.name, "name", "", "name the sandbox")
	f.BoolVar(&rf.noPersistAuth, "no-persist-auth", false, "do not restore or save the agent's login")
	f.StringVar(&rf.profile, "profile", "", "dev or prod (prod: no persisted logins)")
	f.StringVar(&rf.fromSnapshot, "from-snapshot", "", "start from a snapshot (sandbox-cli snapshot) instead of the image")
	f.StringVar(&rf.configPath, "config", "", "an explicit config file, trusted like your own")
	f.StringArrayVar(&rf.volumes, "volume", nil, "mount a named volume, NAME:/path or NAME:/path:ro (repeatable; sandbox-cli volume)")
	f.StringArrayVar(&rf.labels, "label", nil, "label the sandbox, key=value (repeatable); shown by list and recorded in its audit events")
	f.StringArrayVar(&rf.fallback, "fallback", nil, "an agent to try next if this one's provider is down (repeatable; agent wrappers only)")
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
	// before runs once the login is in place, before the command starts; after runs once it has exited, before the sandbox goes.
	before, after func(ctx context.Context, c *api.Client, sandbox string)
	// result, when set, is filled in with what the run did.
	result *runResult
	// console, for a detached run, gives the process a terminal of rows x cols
	// although none is attached now: Studio's browser terminal attaches later,
	// and an interactive agent started without one draws nothing.
	console    bool
	rows, cols uint16
	// labels are added to the sandbox's, over any --label of the same key:
	// they are what sandbox-cli itself knows about the run (the agent, the
	// routing attempt), and a user label should not be able to disguise one
	// routing attempt as another.
	labels map[string]string
}

// runResult is what a caller needs to know about a run: Studio, which sandbox
// it started.
type runResult struct {
	sandbox string
	pid     int
}

// execute is a var so routing's tests can script what each attempt did.
var execute = runSandbox

// runSandbox is the whole of a run: create, restore the login, start the
// command in the sandbox user's home, attach, and on the way out save the
// login and clean up. It returns the command's exit code.
//
// A sandbox needs no repository. It starts in its own home directory, and
// code gets in the way it gets into any machine: the agent or the command
// clones it, or the files API writes it. Nothing on the host is mounted in and
// nothing comes back to the host but the agent's login.
func runSandbox(ctx context.Context, rf *runFlags, rs runSpec) (int, error) {
	project := rf.project
	if project == "" {
		project, _ = os.Getwd()
	}
	// The configuration first: a mistake in your own files is yours to fix
	// whether or not a sandboxd is answering, and should not wait behind one.
	ov, err := rf.overrides()
	if err != nil {
		return 1, err
	}
	if _, err := loadConfig(project, rf.configPath, rf.profile, ov); err != nil {
		return 1, err
	}
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
	for _, v := range rf.volumes {
		m, err := parseVolumeFlag(v)
		if err != nil {
			return 1, err
		}
		req.Volumes = append(req.Volumes, m)
	}
	// An agent the image does not carry runs from its tools volume, installed
	// on this endpoint once. Not for a sandbox from a snapshot, which brings
	// its own disk, nor where the user mounted something of their own.
	if rs.agent != nil && rf.fromSnapshot == "" && !mountsNear(req.Volumes, agents.ToolsDir) {
		if m := agenthome.AgentTools(ctx, c, caps, *rs.agent, stderrf); m != nil {
			req.Volumes = append(req.Volumes, *m)
		}
	}
	if err := applyConfig(rf, project, &req, caps); err != nil {
		return 1, err
	}
	// git in the guest refuses every commit without an identity, and the image
	// sets none; a neutral one, unless the user set their own (--env, or env:
	// in their config). Theirs is never read from the host: whose name goes on
	// the work is not something to copy into a guest running somebody else's
	// instructions without being asked.
	for k, v := range guestGitIdentity {
		if _, set := req.Env[k]; !set {
			req.Env[k] = v
		}
	}

	sb, err := c.CreateSandbox(ctx, req)
	if err != nil {
		return 1, err
	}
	if rs.result != nil {
		rs.result.sandbox = sb.ID
	}
	keep := rf.keep || rf.detach
	defer func() {
		if !keep {
			_ = c.TerminateSandbox(context.Background(), sb.ID)
		}
	}()

	persist := rs.agent != nil && !rf.noPersistAuth && len(rs.agent.AuthPaths) > 0
	if persist {
		agenthome.RestoreLogin(ctx, c, sb.ID, *rs.agent)
	}

	if rs.before != nil {
		rs.before(ctx, c, sb.ID)
	}

	tty := !rf.detach && isTerminal(os.Stdin) && isTerminal(os.Stdout)
	rows, cols := termSize(os.Stdout)
	if rs.console {
		tty, rows, cols = true, rs.rows, rs.cols
	}
	p, err := c.StartProcess(ctx, sb.ID, api.RunRequest{Argv: rs.argv, Cwd: agenthome.GuestHome, Tty: tty, Rows: rows, Cols: cols})
	if err != nil {
		return 1, err
	}
	if rs.result != nil {
		rs.result.pid = p.PID
	}
	if rf.detach {
		fmt.Printf("%s\n", sb.ID)
		fmt.Fprintf(os.Stderr, "sandbox-cli: started %s (process %d). Attach: sandbox-cli attach %s · Stop: sandbox-cli kill %s\n",
			sb.ID, p.PID, sb.ID, sb.ID)
		return 0, nil
	}

	code, err := attach(ctx, c, sb.ID, p.PID, tty, true)
	if err != nil {
		return 1, err
	}

	if rs.after != nil {
		rs.after(context.Background(), c, sb.ID)
	}
	if persist {
		agenthome.SaveLogin(context.Background(), c, sb.ID, *rs.agent)
	}
	if keep && !rf.detach {
		fmt.Fprintf(os.Stderr, "sandbox-cli: kept %s (sandbox-cli kill %s)\n", sb.ID, sb.ID)
	}
	return code, nil
}

// attach connects the terminal (or stdin/stdout) to a process until it exits.
//
// Without a terminal, what an interrupt means depends on whose process it is.
// A run you started in the foreground is yours to stop, so Ctrl-C is
// forwarded to it (forward). Attaching to a run that was started detached is
// watching it, and Ctrl-C there stops the watching: it detaches, and the run
// carries on (errDetached). beta.15 kept the same split with docker's
// --sig-proxy=false, because an unattended agent killed by a keystroke meant
// for the viewer is the failure detaching exists to avoid. With a terminal,
// Ctrl-C is a byte for the process's own terminal either way, as it is for
// any interactive program.
func attach(ctx context.Context, c *api.Client, sandbox string, pid int, tty, forward bool) (int, error) {
	st, err := c.Attach(ctx, sandbox, pid)
	if err != nil {
		return 1, err
	}
	defer st.Close()
	var detached atomic.Bool
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
				switch {
				case !forward:
					detached.Store(true)
					_ = st.Close()
					return
				case s == os.Interrupt:
					_ = st.Signal("INT")
				default:
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
	code, err := st.Copy(os.Stdout, os.Stderr)
	if detached.Load() {
		return 0, errDetached
	}
	return code, err
}

// guestGitIdentity is the identity commits made in a sandbox carry by
// default, as the environment variables git reads.
var guestGitIdentity = map[string]string{
	"GIT_AUTHOR_NAME": "sandbox", "GIT_AUTHOR_EMAIL": "sandbox@localhost",
	"GIT_COMMITTER_NAME": "sandbox", "GIT_COMMITTER_EMAIL": "sandbox@localhost",
}

// errDetached is attach ending because the viewer left, not the process.
var errDetached = errors.New("detached; the process is still running")

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

// mountsNear reports whether any mount is at path, inside it, or around it.
func mountsNear(mounts []api.VolumeMount, path string) bool {
	for _, m := range mounts {
		if m.Path == path || strings.HasPrefix(m.Path, path+"/") || strings.HasPrefix(path, m.Path+"/") {
			return true
		}
	}
	return false
}

func stderrf(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "sandbox-cli: "+format+"\n", args...)
}

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/policy"
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
	profile       string
	configPath    string
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
	f.StringVar(&rf.configPath, "config", "", "an explicit config file, trusted like your own")
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
}

// execute is the whole of a run: create, set up the workspace, restore the
// login, attach, and on the way out save the login, bring work back and clean
// up. It returns the command's exit code.
func execute(ctx context.Context, rf *runFlags, rs runSpec) (int, error) {
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
		IdleTimeoutSecs: rf.idle,
	}
	if req.Env, err = buildEnv(rf.env, rs.agent); err != nil {
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

	var sess *session
	if rf.bind == "" && !rf.noWorkspace {
		if repo := repoRoot(project); repo != "" {
			base, err := cloneIn(ctx, c, sb.ID, repo)
			if err != nil {
				return 1, err
			}
			sess = &session{Sandbox: sb.ID, Context: ctxName, Repo: repo, Base: base, Branch: sandboxBranch}
			_ = sess.save()
		} else {
			fmt.Fprintln(os.Stderr, "sandbox-cli: not in a git repository; /workspace starts empty (use --bind on a local endpoint to mount a directory)")
		}
	}

	persist := rs.agent != nil && !rf.noPersistAuth && len(rs.agent.AuthPaths) > 0
	if persist {
		restoreAuth(ctx, c, sb.ID, *rs.agent)
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

	code, err := attach(ctx, c, sb.ID, p.PID, tty)
	if err != nil {
		return 1, err
	}

	if persist {
		saveAuth(context.Background(), c, sb.ID, *rs.agent)
	}
	if sess != nil && !rf.noBringBack {
		name := sb.ID
		if rf.name != "" {
			name = rf.name
		}
		ref, err := bringBack(context.Background(), c, *sess, name)
		switch {
		case err != nil:
			keep = true
			fmt.Fprintf(os.Stderr, "sandbox-cli: bringing work back failed: %v\n  the sandbox is kept: sandbox-cli bring-back %s\n", err, sb.ID)
		case ref == "":
			fmt.Fprintln(os.Stderr, "sandbox-cli: no new commits to bring back")
		default:
			fmt.Fprintf(os.Stderr, "sandbox-cli: work brought back to %s\n  review: git log -p HEAD..%s · merge: git merge %s\n", ref, ref, ref)
		}
	}
	if keep && !rf.detach {
		fmt.Fprintf(os.Stderr, "sandbox-cli: kept %s (sandbox-cli kill %s)\n", sb.ID, sb.ID)
	}
	return code, nil
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

// The agent's login lives in ~/.config/sandbox/agents/<agent>/ on the host,
// one file per AuthPath, 0600. It is copied in when a run starts and back out
// when it ends — the host never mounts anything into the guest for it.

func authDir(d agents.Descriptor) string { return filepath.Join(configDir(), "agents", d.PersistDir) }

const guestHome = "/sandbox/home"

func restoreAuth(ctx context.Context, c *api.Client, sandbox string, d agents.Descriptor) {
	for _, rel := range d.AuthPaths {
		data, err := os.ReadFile(filepath.Join(authDir(d), filepath.FromSlash(rel)))
		if err != nil {
			continue
		}
		_ = c.WriteFile(ctx, sandbox, guestHome+"/"+rel, data)
	}
}

func saveAuth(ctx context.Context, c *api.Client, sandbox string, d agents.Descriptor) {
	for _, rel := range d.AuthPaths {
		data, err := c.ReadFile(ctx, sandbox, guestHome+"/"+rel)
		if err != nil {
			continue
		}
		if err := writePrivate(authDir(d), rel, data); err != nil {
			fmt.Fprintf(os.Stderr, "sandbox-cli: saving the %s login: %v\n", d.Name, err)
		}
	}
}

// writePrivate writes rel under dir, which sandbox-cli owns: owner-only, and
// never through a symlink — the content came from a guest, and a link planted
// in this directory must not redirect the write.
func writePrivate(dir, rel string, data []byte) error {
	path := filepath.Join(dir, filepath.FromSlash(rel))
	if !strings.HasPrefix(path, dir+string(filepath.Separator)) {
		return errors.New("path escapes the login directory")
	}
	for d := filepath.Dir(path); ; d = filepath.Dir(d) {
		if fi, err := os.Lstat(d); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
			return fmt.Errorf("%s is a symlink; not writing through it", d)
		}
		if d == dir || len(d) <= len(dir) {
			break
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	if fi, err := os.Lstat(path); err == nil && fi.Mode()&fs.ModeSymlink != 0 {
		return fmt.Errorf("%s is a symlink; not writing through it", path)
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

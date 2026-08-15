// Package cli wires the cobra command tree for the `sandbox-cli` binary.
package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/image"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// runFlags holds the persistent flag values shared by run and the agent wrappers.
type runFlags struct {
	project     string
	image       string
	workdir     string
	user        string
	mounts      []string
	env         []string
	envAllow    []string
	tty         bool
	noTTY       bool
	config      string
	profile     string
	network     string
	engine      string
	build       bool
	dryRun      bool
	noMetrics   bool
	memory      string
	cpus        string
	noHardening bool
	allow       []string
	cache       bool
	secrets     []string
	worktree    string
	publish     []string
	addHosts    []string
	hostGateway bool
	git         bool
	runtime     string
	share       bool
	// shareName namespaces --share: empty means the shared root at /shared
	// (today's behaviour), a name means <shared>/NAME at /shared/NAME so two
	// concurrent sandboxes cannot silently overwrite the same filename.
	shareName string
	paste     bool
	detach    bool

	// Crash safety net (internal/rescue). noSnapshot opts out entirely;
	// snapshotInterval overrides the configured cadence for this run.
	noSnapshot       bool
	snapshotInterval time.Duration

	// fallback is --fallback: the agents to try, in order, when the one asked for
	// is unavailable. Wrapper-only, because it re-targets *an agent* — a plain
	// `run` has an argv rather than an adapter, and there is nothing to route it
	// to.
	fallback []string

	// routedFrom/routeReason are set by the routing loop on a fallback attempt,
	// and travel only as far as the audit line — the container is identical
	// either way, so this is a fact *about* the run rather than an input to it.
	routedFrom   string
	routeReason  string
	routeID      string
	routeAttempt int

	// Auth persistence (agent wrappers only). persistName is the sandbox-owned
	// host state dir name (e.g. "claude") mounted as the agent's HOME.
	// noPersistAuth opts out.
	persistName   string
	noPersistAuth bool

	// noStatusline disables the sandbox mem/cpu status line for the claude wrapper.
	noStatusline bool

	// noSync (claude wrapper) opts out of read-write mounting the host's Claude
	// project history for this repo into the sandbox. Sharing it is the default,
	// so host sessions can be --resume'd from inside the container.
	noSync bool
}

// newSession loads config (with the flag override for the config path) and
// returns a wired Session plus the base Options derived from the flags. The
// guest command is filled in by each subcommand.
func newSession(rf *runFlags) (*sandbox.Session, sandbox.Options, error) {
	startDir, _ := os.Getwd()
	// An explicit --network outranks the profile's default. It is the way to
	// decline the allowlist for one run, which matters now that the allowlist is
	// the default: without it, --user root and --no-hardening would collide with
	// a posture the user never asked for and there would be no way to say so.
	//
	// Checked here and applied by the loader, because the profile is validated
	// as part of loading. Applying it afterwards was too late — a prod run whose
	// config said `mode: default` was refused before the flag that fixes it was
	// ever read, so the documented escape hatch could not be used.
	if rf.network != "" {
		switch rf.network {
		case "default", "none", "allowlist":
		default:
			return nil, sandbox.Options{}, fmt.Errorf("--network %q: want allowlist, default or none", rf.network)
		}
	}
	cfg, err := config.LoadProfileWith(startDir, rf.config, rf.profile,
		config.Overrides{NetworkMode: rf.network, Allow: rf.allow})
	if err != nil {
		return nil, sandbox.Options{}, err
	}
	// --user root and --no-hardening contradict the allowlist: the firewall needs
	// the privilege drop to survive, and both undo it. BuildSpec refuses the
	// combination, and should.
	//
	// But since the allowlist became the *default*, that refusal would fire on a
	// posture the user never asked for — `sandbox-cli run --user root` would stop
	// working with an error about an allowlist nobody requested. So an explicit
	// flag beats an implicit default: the allowlist yields, loudly. An allowlist
	// that was actually asked for (--allow, --network allowlist, or prod) still
	// refuses, because then the user has stated both intentions and only one can
	// hold.
	if cfg.Network.Mode == "allowlist" && rf.network == "" && len(rf.allow) == 0 &&
		cfg.Profile != config.ProfileProd && (rf.noHardening || config.IsRootUser(rf.user)) {
		which := "--user root"
		if rf.noHardening {
			which = "--no-hardening"
		}
		fmt.Fprintf(os.Stderr, "sandbox-cli: %s disables what the egress allowlist depends on, so this run "+
			"has no allowlist\n  pass --allow or --network allowlist to make the conflict an error instead\n", which)
		cfg.Network.Mode = "default"
	}
	if rf.engine != "" {
		cfg.Engine = rf.engine
	}
	if err := cfg.Validate(); err != nil {
		return nil, sandbox.Options{}, err
	}

	sess := sandbox.New(cfg)
	// Wire the lazy image builder into the docker backend.
	if d, ok := sess.Runtime.(*runtime.DockerCLI); ok {
		image.Register(d)
	}

	opts := sandbox.Options{
		Project:     rf.project,
		Image:       rf.image,
		Workdir:     rf.workdir,
		User:        rf.user,
		ExtraMounts: rf.mounts,
		Env:         rf.env,
		EnvAllow:    rf.envAllow,
		TTY:         ttyOverride(rf),
		NoMetrics:   rf.noMetrics,
		Memory:      rf.memory,
		CPUs:        rf.cpus,
		NoHardening: rf.noHardening,
		Allow:       rf.allow,
		Cache:       rf.cache,
		Secrets:     rf.secrets,
		Publish:     rf.publish,
		AddHosts:    rf.addHosts,
		HostGateway: rf.hostGateway,
		GitIdentity: rf.git,
		Runtime:     rf.runtime,
	}

	// The repository this run belongs to, resolved before --worktree redirects the
	// project directory: identity is a property of the repo, not of the checkout
	// a particular agent happens to be given.
	repoDir := rf.project
	if repoDir == "" {
		repoDir, _ = os.Getwd()
	}

	// --worktree BRANCH: resolve (creating if needed) a git worktree for the
	// branch and run the sandbox in it, so parallel agents each get their own
	// branch/container without colliding. This overrides the project directory.
	if rf.worktree != "" {
		info, werr := worktree.Resolve(repoDir, rf.worktree)
		if werr != nil {
			return nil, sandbox.Options{}, werr
		}
		verb := "using"
		if info.Created {
			verb = "created"
		}
		fmt.Fprintf(os.Stderr, "sandbox-cli: %s worktree %q at %s\n", verb, info.Branch, info.Path)
		opts.Project = info.Path
	}

	// A git worktree's .git is a pointer file holding an absolute host path into
	// the parent repo, which lives outside the workspace. Without the parent .git
	// mounted at that same path, every git command inside the container fails with
	// "not a git repository" — the agent could edit files but never commit them.
	// Applies both to --worktree and to running from a worktree directly.
	projectDir := opts.Project
	if projectDir == "" {
		projectDir, _ = os.Getwd()
	}
	// The three binds a linked worktree needs — the parent .git, the worktree at
	// its own host path, and the parent's hooks read-only over it — together with
	// the refusals that guard them. Shared with the fleet runner, which builds its
	// own Options and would otherwise have to repeat both the mounts and the
	// safety check. The workspace's *own* .git/hooks is handled in BuildSpec,
	// which knows where the workspace lands inside the container; this is the
	// parent repository's, which a linked worktree also runs.
	opts.ExtraMounts = append(opts.ExtraMounts, sandbox.LinkedWorktreeMounts(projectDir)...)

	// The branch drives the live gauge and the post-run summary, and — with
	// --detach — the container's name and its sandbox.branch label. It matters
	// most with --worktree, where several containers are running different
	// branches of the same repo at once.
	opts.Branch = worktree.Branch(config.ExpandTilde(projectDir))

	// Identity for the container name and the sandbox.* labels. RepoID is shared
	// by every branch of one repository, so a later command can ask for all of
	// them at once; Base is the branch the main checkout is sitting on now, which
	// is the branch this work is expected to land on. Both are best-effort: a
	// non-repository project simply has no identity to stamp, which is not an
	// error — it only means the run cannot be addressed by branch afterwards.
	if id, ierr := worktree.RepoID(config.ExpandTilde(repoDir)); ierr == nil {
		opts.RepoID = id
		opts.Base = worktree.Branch(config.ExpandTilde(repoDir))
	}
	opts.Agent = rf.persistName
	opts.RoutedFrom, opts.RouteReason = rf.routedFrom, rf.routeReason
	opts.RouteID, opts.RouteAttempt = rf.routeID, rf.routeAttempt
	opts.Detach = rf.detach

	// --share: mount one sandbox-owned host directory at /shared. This is the
	// only mount that is intentionally the *same* for every project, which is
	// what makes it a channel: two agents that share nothing else — different
	// repos, different worktrees, different containers — can hand a file over
	// through it. Opt-in, because a cross-project channel is exactly the kind of
	// reach the sandbox otherwise refuses by default. --share-name NAME
	// namespaces it to <shared>/NAME at /shared/NAME, so two concurrent runs
	// that both want their own handoff spot don't clobber the same filename in
	// the shared root.
	//
	// --share-name requires --share rather than implying it. Implying would mean
	// one flag silently switching on the cross-project channel, and would leave
	// `--share=false --share-name work` as a contradiction to resolve by
	// guessing — with the wrong guess enabling sharing for someone who wrote
	// "false". Requiring both makes the opt-in one deliberate act with no
	// ambiguous spelling, at the cost of one extra word.
	if rf.shareName != "" && !rf.share {
		return nil, sandbox.Options{}, fmt.Errorf("--share-name %q needs --share as well: a namespace is a way of sharing, not an alternative to it", rf.shareName)
	}
	if rf.share {
		mount, err := shareMount(rf.shareName)
		if err != nil {
			return nil, sandbox.Options{}, err
		}
		opts.ExtraMounts = append(opts.ExtraMounts, mount)
	}

	// --paste: make an image path pasted into the agent resolve, by mounting the
	// directories such paths point into read-only at their own host paths. Opt-in
	// and announced, because it is the widest reach the sandbox will grant short
	// of an explicit --mount: everything in those directories becomes readable,
	// not only the file that was pasted.
	if rf.paste {
		home, herr := os.UserHomeDir()
		if herr != nil {
			return nil, sandbox.Options{}, fmt.Errorf("--paste: cannot determine your home directory: %w", herr)
		}
		mounts, dirs := pasteMounts(home)
		if len(mounts) == 0 {
			fmt.Fprintf(os.Stderr, "sandbox-cli: --paste: none of %s exist under %s; mount the directory you paste from with --mount DIR:DIR:ro\n",
				strings.Join(pasteDirNames, ", "), home)
		} else {
			opts.ExtraMounts = append(opts.ExtraMounts, mounts...)
			fmt.Fprintf(os.Stderr, "sandbox-cli: --paste: %s mounted read-only at their host paths\n", strings.Join(dirs, " "))
		}
	}

	// Persist agent login in a dedicated, sandbox-owned host dir mounted as the
	// agent's whole HOME, so login survives the ephemeral container.
	if rf.persistName != "" && !rf.noPersistAuth && cfg.PersistAuthEnabled() {
		dir := config.AgentStateDir(rf.persistName)
		if dir != "" {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return nil, sandbox.Options{}, fmt.Errorf("creating auth persist dir %s: %w", dir, err)
			}
			opts.AuthPersistDir = dir
		}
	}

	return sess, opts, nil
}

func ttyOverride(rf *runFlags) *bool {
	switch {
	case rf.noTTY:
		v := false
		return &v
	case rf.tty:
		v := true
		return &v
	default:
		return nil // auto-detect
	}
}

// addRunFlags registers the shared persistent flags on a command.
func addRunFlags(cmd *cobra.Command, rf *runFlags) {
	f := cmd.Flags()
	f.StringVarP(&rf.project, "project", "p", "", "host dir to mount at /workspace (default: cwd)")
	f.StringVarP(&rf.image, "image", "i", "", "override the container image")
	f.StringVarP(&rf.workdir, "workdir", "w", "", "working dir inside the container (default: /workspace)")
	f.StringVar(&rf.user, "user", "", "user inside the container (root|sandbox|uid:gid)")
	f.StringArrayVarP(&rf.mounts, "mount", "m", nil, "extra mount host:container[:ro|rw] (repeatable)")
	f.StringArrayVarP(&rf.env, "env", "e", nil, "KEY=VALUE, or bare KEY to forward host value (repeatable)")
	f.StringArrayVar(&rf.envAllow, "env-allow", nil, "host env var name to forward if present (repeatable)")
	f.BoolVar(&rf.tty, "tty", false, "force an interactive TTY")
	f.BoolVar(&rf.noTTY, "no-tty", false, "disable TTY allocation")
	f.StringVarP(&rf.config, "config", "c", "", "explicit config file path")
	f.StringVar(&rf.profile, "profile", "", "security profile: dev (warns) or prod (refuses); default dev")
	f.StringVar(&rf.network, "network", "", "network mode: allowlist (default), default (unrestricted), or none")
	f.StringVar(&rf.engine, "engine", "", "container engine: docker (default) or podman")
	f.BoolVar(&rf.build, "build", false, "force rebuild of the base image")
	f.BoolVar(&rf.dryRun, "dry-run", false, "print the docker command and exit")
	f.BoolVar(&rf.noMetrics, "no-metrics", false, "disable the live resource gauge (non-interactive runs)")
	f.StringVar(&rf.memory, "memory", "", "container memory limit, e.g. 2g (default: unlimited)")
	f.StringVar(&rf.cpus, "cpus", "", "container CPU limit, e.g. 1.5 (default: unlimited)")
	f.BoolVar(&rf.noHardening, "no-hardening", false, "disable default cap-drop/no-new-privileges/pids-limit (debug)")
	f.StringArrayVar(&rf.allow, "allow", nil, "enable the egress allowlist and permit DOMAIN (repeatable; baseline registries always allowed)")
	f.BoolVar(&rf.cache, "cache", false, "persist package-manager caches (npm/pip/cargo/go) in named volumes across runs")
	f.StringArrayVar(&rf.secrets, "secret", nil, "brokered credential NAME=file:PATH|cmd:COMMAND|env:VAR, resolved at run time and kept off the argv (repeatable)")
	f.StringVar(&rf.worktree, "worktree", "", "run in a git worktree for BRANCH (created if absent), for parallel per-branch agents")
	f.StringArrayVarP(&rf.publish, "publish", "P", nil, "publish a container port to the host: PORT|HOST:CONTAINER|IP:HOST:CONTAINER (repeatable; binds 127.0.0.1 unless an address is given)")
	f.StringArrayVar(&rf.addHosts, "add-host", nil, "extra HOST:IP mapping passed to docker (repeatable)")
	f.BoolVar(&rf.hostGateway, "host-gateway", false, "map host.docker.internal to the host so the agent can reach host MCP servers (Linux)")
	f.BoolVar(&rf.git, "git", false, "forward host git identity and trust the workspace so git commits just work in-container")
	f.StringVar(&rf.runtime, "runtime", "", "OCI runtime for stronger isolation, e.g. kata-fc (microVM) or runsc (gVisor); must be registered with docker")
	f.BoolVar(&rf.share, "share", false, "mount the shared dir (~/.config/sandbox/shared) at /shared so agents in different projects can exchange files")
	// A separate string flag rather than an optional value on --share. An
	// optional-value flag needs NoOptDefVal, and NoOptDefVal is also what tells
	// pflag (and splitWrapperArgs) that the flag does NOT consume the next token
	// — so `--share NAME` silently left --share bare, mounted the whole shared
	// root rather than the leaf, and forwarded NAME and every sandbox flag after
	// it to the guest as arguments. The failure was toward more access, from a
	// spelling that looks correct. A StringVar consumes its value in both forms,
	// so there is no spelling of this that fails open.
	f.StringVar(&rf.shareName, "share-name", "", "with --share, mount ~/.config/sandbox/shared/NAME at /shared/NAME instead of the root, so concurrent runs don't clobber each other's files")
	f.BoolVar(&rf.paste, "paste", false, "mount ~/Desktop, ~/Downloads and ~/Pictures read-only at their host paths so an image path pasted into the agent resolves")
	f.BoolVar(&rf.detach, "detach", false, "run in the background and print the container name; the guest gets no terminal, so it must be a command (or an agent in its non-interactive mode) that exits on its own")
	f.BoolVar(&rf.noSnapshot, "no-snapshot", false, "disable the crash safety net (periodic snapshots of the workspace under refs/sandbox)")
	f.DurationVar(&rf.snapshotInterval, "snapshot-interval", 0, "how often to snapshot the workspace, e.g. 30s (default: the configured 2m)")

	// Flags before -- are ours; everything after -- is the guest command verbatim.
	f.SetInterspersed(false)
}

// NewRootCmd builds the top-level command tree.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "sandbox-cli",
		Short: "Run AI coding agents in a disposable, isolated container",
		Long: "sandbox-cli runs a command (or an AI coding agent) inside a throwaway Docker\n" +
			"container where only the chosen project is mounted at /workspace and HOME is\n" +
			"a fake, ephemeral directory. A mistaken `rm -rf ~` or an injected command\n" +
			"cannot touch the rest of your machine.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		newRunCmd(),
		newInitCmd(),
		newConfigCmd(),
		newStatsCmd(),
		newListCmd(),
		newLogsCmd(),
		newAttachCmd(),
		newKillCmd(),
		newDoctorCmd(),
		newCleanCmd(),
		newUsageCmd(),
		newFleetCmd(),
		newWorktreeCmd(),
		newRecoverCmd(),
		newContextCmd(),
		newVersionCmd(),
	)
	// One command per agent adapter, from the single list in agents.go.
	root.AddCommand(agentCmds()...)
	return root
}

// Execute runs the CLI and returns a process exit code.
func Execute() int {
	if err := NewRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "sandbox-cli: "+err.Error())
		return 1
	}
	return exitCode
}

// exitCode carries the guest process exit code out of a subcommand's RunE so the
// process can mirror it. Defaults to 0.
var exitCode int

// isDirPath reports whether p is an existing directory. Only existing hook
// directories are mounted: creating one would be sandbox-cli writing into the
// user's repository, which it does not do.
func isDirPath(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

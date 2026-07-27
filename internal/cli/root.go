// Package cli wires the cobra command tree for the `sandbox-cli` binary.
package cli

import (
	"fmt"
	"os"
	"path/filepath"
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
	paste       bool
	detach      bool

	// Crash safety net (internal/rescue). noSnapshot opts out entirely;
	// snapshotInterval overrides the configured cadence for this run.
	noSnapshot       bool
	snapshotInterval time.Duration

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
	cfg, err := config.LoadProfile(startDir, rf.config, rf.profile)
	if err != nil {
		return nil, sandbox.Options{}, err
	}
	// An explicit --network outranks the profile's default. It is the way to
	// decline the allowlist for one run, which matters now that the allowlist is
	// the default: without it, --user root and --no-hardening would collide with
	// a posture the user never asked for and there would be no way to say so.
	if rf.network != "" {
		switch rf.network {
		case "default", "none", "allowlist":
			cfg.Network.Mode = rf.network
		default:
			return nil, sandbox.Options{}, fmt.Errorf("--network %q: want allowlist, default or none", rf.network)
		}
		if err := config.ValidateProfile(cfg.Profile, cfg); err != nil {
			return nil, sandbox.Options{}, err
		}
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
		cfg.Profile != config.ProfileProd && (rf.noHardening || isRootRequest(rf.user)) {
		which := "--user root"
		if rf.noHardening {
			which = "--no-hardening"
		}
		fmt.Fprintf(os.Stderr, "sandbox-cli: %s disables what the egress allowlist depends on, so this run "+
			"has no allowlist\n  pass --allow or --network allowlist to make the conflict an error instead\n", which)
		cfg.Network.Mode = "default"
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
	// The path comes from a `.git` pointer file inside the workspace, which the
	// agent can rewrite, and it is about to be mounted read-write at its own host
	// location — so it goes through the same non-overridable refusals as the
	// workspace itself. worktree.GitCommonDir already requires the target to look
	// like a real git directory; this is the second layer, and the one that would
	// still hold if that check were ever loosened.
	if gitDir, ok := worktree.GitCommonDir(config.ExpandTilde(projectDir)); ok && sandbox.RefuseUnsafeHostPath(gitDir) == nil {
		opts.ExtraMounts = append(opts.ExtraMounts, gitDir+":"+gitDir+":rw")

		// The worktree is mounted a second time at its own host path, not only at
		// /workspace. The parent repo records each linked worktree by absolute path
		// and treats a record whose path has vanished as a deleted worktree, so
		// inside the container every one of them reads as "prunable: gitdir file
		// points to non-existent location" — the host paths simply aren't there.
		// The parent .git above is mounted read-write so the agent can commit, which
		// means a `git worktree prune` (or the one `git gc` runs for itself) reaches
		// out of the container and deletes the user's entire worktree registry,
		// orphaning every worktree including the one it is running in. Making the
		// path resolve is one extra bind of a directory that is already mounted, so
		// it grants no reach the container did not have a moment ago.
		if wt, err := filepath.Abs(config.ExpandTilde(projectDir)); err == nil {
			opts.ExtraMounts = append(opts.ExtraMounts, wt+":"+wt+":rw")
		}
	}

	// .git/hooks read-only, over the read-write workspace.
	//
	// The agent has to be able to edit project files, but hooks are not project
	// source: they are programs the *user's* git runs, on the host, as them. An
	// agent that writes .git/hooks/pre-commit is not editing the project, it is
	// waiting for the user's next commit. Confirmed as a live escape.
	//
	// hooks specifically, not .git as a whole: agents legitimately run `git config`
	// and git itself writes indexes, refs and logs constantly. Changes to
	// .git/config are watched instead (see watchGitConfig) — detected rather than
	// prevented, because preventing them breaks ordinary work.
	// The workspace's own .git/hooks is handled in BuildSpec, which knows where the
	// workspace lands inside the container. This is the other one: a linked
	// worktree runs the hooks from the parent repository's common directory, and
	// that directory is bind-mounted read-write at its host path so git works at
	// all — so its hooks need covering at that same host path.
	if common, ok := worktree.GitCommonDir(config.ExpandTilde(projectDir)); ok {
		if h := filepath.Join(common, "hooks"); isDirPath(h) {
			opts.ExtraMounts = append(opts.ExtraMounts, h+":"+h+":ro")
		}
	}

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
	opts.Detach = rf.detach

	// --share: mount one sandbox-owned host directory at /shared. This is the
	// only mount that is intentionally the *same* for every project, which is
	// what makes it a channel: two agents that share nothing else — different
	// repos, different worktrees, different containers — can hand a file over
	// through it. Opt-in, because a cross-project channel is exactly the kind of
	// reach the sandbox otherwise refuses by default.
	if rf.share {
		dir := config.SharedDir()
		if dir == "" {
			return nil, sandbox.Options{}, fmt.Errorf("--share: cannot determine the config directory (no HOME?)")
		}
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return nil, sandbox.Options{}, fmt.Errorf("creating shared dir %s: %w", dir, err)
		}
		seedSharedReadme(dir)
		opts.ExtraMounts = append(opts.ExtraMounts, dir+":"+sharedTarget+":rw")
		fmt.Fprintf(os.Stderr, "sandbox-cli: sharing %s at %s\n", dir, sharedTarget)
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
	f.StringVar(&rf.runtime, "runtime", "", "OCI runtime for stronger isolation, e.g. kata-runtime (microVM) or runsc (gVisor); must be registered with docker")
	f.BoolVar(&rf.share, "share", false, "mount the shared dir (~/.config/sandbox/shared) at /shared so agents in different projects can exchange files")
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
		newPsCmd(),
		newCleanCmd(),
		newUsageCmd(),
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

// isRootRequest reports whether a --user value asks for uid 0. Mirrors
// sandbox.isRootUser, which is unexported and applies one layer down.
func isRootRequest(user string) bool {
	switch strings.TrimSpace(user) {
	case "root", "0", "0:0", "root:root":
		return true
	}
	return false
}

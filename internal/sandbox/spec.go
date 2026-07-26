package sandbox

import (
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/creds"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// gitIdentityEnvNames are forwarded (by name) into the container when --git is
// set, so commits made in the sandbox are attributed to the host git identity.
var gitIdentityEnvNames = []string{
	"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL",
	"GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL",
}

// Options are the per-invocation flag values collected by the CLI. Zero values
// mean "not set" and fall back to config.
type Options struct {
	Project     string   // --project: host dir for /workspace (default cwd)
	Image       string   // --image override
	Workdir     string   // --workdir override
	User        string   // --user override
	Runtime     string   // --runtime: OCI runtime (e.g. kata-runtime, runsc); "" => config/default
	ExtraMounts []string // --mount host:container[:ro|rw]
	Env         []string // --env KEY=VALUE or bare KEY (forward host value)
	EnvAllow    []string // --env-allow NAME (forward host value if present)
	TTY         *bool    // --tty/--no-tty; nil => auto-detect
	NoMetrics   bool     // disable the live resource gauge
	Memory      string   // --memory: container memory limit (e.g. "2g"); "" => config/unlimited
	CPUs        string   // --cpus: container CPU limit (e.g. "1.5"); "" => config/unlimited
	NoHardening bool     // --no-hardening: drop cap-drop/no-new-privileges/pids-limit (debug escape hatch)
	Allow       []string // --allow DOMAIN: enable the egress allowlist and permit these domains (repeatable)
	Cache       bool     // --cache: persist package-manager caches in named volumes across runs
	Secrets     []string // --secret NAME=file:PATH|cmd:COMMAND|env:VAR (brokered credential, repeatable)
	Publish     []string // --publish/-P PORT|HOST:CONTAINER|IP:HOST:CONTAINER (repeatable); adds to config `ports`
	AddHosts    []string // --add-host HOST:IP (repeatable)
	HostGateway bool     // --host-gateway: add host.docker.internal -> host gateway (reach host MCP servers)
	GitIdentity bool     // --git: forward host git user.name/email and trust the workspace
	Branch      string   // workspace's git branch: display in the gauge/summary, and the sandbox.branch label
	Command     []string // guest argv

	// Detach runs the container in the background instead of waiting on it, so one
	// terminal can launch several agents. It is not merely a docker flag: it
	// decides three things about the resolved spec that are wrong by default for
	// an unattended run — no pty, no live gauge, and no --rm (the exit code and
	// logs are the whole point of launching it).
	Detach bool

	// Identity stamped on the container as sandbox.* labels, and — for detached
	// runs — folded into its name. Docker is the state store: a fact not recorded
	// here is one no later command can recover.
	RepoID string // stable repo identity (worktree.RepoID), shared by every branch of one repo
	Agent  string // agent adapter name ("claude", "codex"), empty for a plain run
	Base   string // the branch this work is expected to land on

	// AuthPersistDir, when non-empty, is a host directory bind-mounted read-write
	// as the agent's whole HOME so its login/config survives the ephemeral
	// container (log in once). Set by the claude/codex wrappers.
	AuthPersistDir string
}

// isReservedEnv reports whether name is one of the control variables consumed by
// the container's root-phase startup. The canonical list and the reasoning live
// in config.IsReservedEnv; this is the local spelling.
//
// Two behaviors hang off it, and the difference is deliberate. A name arriving
// through `env_allow` is dropped **silently**: that list is a best-effort
// "forward it if it happens to be set", so a host that exports one of these
// should not fail the run. A name set **deliberately** — `--env
// SANDBOX_RUN_AS=root`, or a config `env:` key — is refused with an error,
// because there the user is asking rather than inheriting and a silent drop
// would mislead them about what the container received.
func isReservedEnv(name string) bool { return config.IsReservedEnv(name) }

// BuildSpec turns a merged config plus per-invocation options into a fully
// resolved runtime.RunSpec. It resolves and safety-checks the workspace, folds
// in config and flag mounts/env, and decides TTY allocation.
func BuildSpec(cfg config.Config, opts Options) (runtime.RunSpec, error) {
	ws, err := ResolveWorkspace(opts.Project)
	if err != nil {
		return runtime.RunSpec{}, err
	}

	image := cfg.Image
	if opts.Image != "" {
		image = opts.Image
	}
	workdir := cfg.Workdir
	if opts.Workdir != "" {
		workdir = opts.Workdir
	}
	user := cfg.User
	if opts.User != "" {
		user = opts.User
	}
	// OCI runtime (docker --runtime): "" => docker default (runc). Named
	// runtimeName to avoid shadowing the imported runtime package.
	runtimeName := cfg.Runtime
	if opts.Runtime != "" {
		runtimeName = opts.Runtime
	}

	// The workspace target is caller-influenced (config `workdir:`), so it is
	// checked like any other: a target that shadows the container's own binaries
	// hands the repository control of what the entrypoint runs.
	wsTarget := workdirTargetOrDefault(cfg.Workdir)
	if err := ValidateMountTarget(wsTarget); err != nil {
		return runtime.RunSpec{}, fmt.Errorf("workdir %q: %w", wsTarget, err)
	}
	mounts := []runtime.Mount{WorkspaceMount(ws, wsTarget)}

	// Config-declared mounts (host paths already resolved to absolute at load time).
	for _, m := range cfg.Mounts {
		if err := ValidateMountTarget(m.Container); err != nil {
			return runtime.RunSpec{}, err
		}
		mounts = append(mounts, runtime.Mount{
			Source: m.Host,
			Target: m.Container,
			RO:     m.Mode != "rw",
		})
	}
	// Flag mounts.
	for _, raw := range opts.ExtraMounts {
		m, err := parseMount(raw)
		if err != nil {
			return runtime.RunSpec{}, err
		}
		if err := ValidateMountTarget(m.Target); err != nil {
			return runtime.RunSpec{}, err
		}
		mounts = append(mounts, m)
	}

	// Auth-persistence mount: a sandbox-owned host dir mounted as the whole
	// agent HOME, so everything the agent writes there — ~/.claude.json (the
	// "onboarding done" flag + account), ~/.claude/.credentials.json, ~/.codex —
	// survives the ephemeral --rm container and you log in once. Mounting the
	// whole home (not just ~/.claude) is required because config files are
	// written via atomic rename, which a single-file bind mount cannot persist.
	if opts.AuthPersistDir != "" {
		home := cfg.Home
		if home == "" {
			home = "/sandbox/home"
		}
		mounts = append(mounts, runtime.Mount{
			Source: opts.AuthPersistDir,
			Target: home,
			RO:     false,
		})
	}

	// Persistent package-manager caches. When enabled, mount a docker-managed
	// named volume at each cache dir so npm/pip/cargo/go downloads survive --rm.
	// These overlay the corresponding subdirs of HOME (including the auth-persist
	// bind above, when present) and are host-independent (docker volumes), so
	// they don't touch the workspace/HOME isolation invariants.
	if cfg.Cache.IsEnabled() || opts.Cache {
		for _, p := range cfg.Cache.CachePaths() {
			mounts = append(mounts, runtime.Mount{
				Source: config.CacheVolumeName(p),
				Target: p,
				Volume: true,
			})
		}
	}

	env := map[string]string{}
	for k, v := range cfg.Env {
		if isReservedEnv(k) {
			continue
		}
		env[k] = v
	}
	var envNames []string
	seen := map[string]bool{}
	addName := func(n string) {
		n = strings.TrimSpace(n)
		if n == "" || seen[n] || isReservedEnv(n) {
			return
		}
		seen[n] = true
		envNames = append(envNames, n)
	}

	// Config env_allow: forward host value only if present.
	for _, n := range cfg.EnvAllow {
		if _, ok := os.LookupEnv(n); ok {
			addName(n)
		}
	}
	for _, n := range opts.EnvAllow {
		if _, ok := os.LookupEnv(n); ok {
			addName(n)
		}
	}
	// --env: KEY=VALUE sets explicitly; bare KEY forwards host value.
	for _, e := range opts.Env {
		if k, v, ok := strings.Cut(e, "="); ok {
			if isReservedEnv(k) {
				return runtime.RunSpec{}, fmt.Errorf("--env %s: %s", k, config.ReservedEnvReason())
			}
			env[k] = v
		} else {
			if _, ok := os.LookupEnv(e); ok {
				addName(e)
			}
		}
	}

	// The host's timezone, so timestamps written inside the container carry the
	// offset the user works in rather than the UTC the base image was built with
	// (timezone.go). A default, which is why it yields to anything the user said
	// about TZ themselves — `--env TZ=UTC` opts out, and forwarding TZ by name
	// keeps the host's own value.
	if _, set := env["TZ"]; !set && !seen["TZ"] {
		if tz := hostTimezone(); tz != "" {
			env["TZ"] = tz
		}
	}

	// Brokered secrets are forwarded by name only. Their values are resolved on
	// the real run path (Session.Run -> injectSecrets), never here, so BuildSpec
	// stays pure and --dry-run neither reads a secret file nor runs a secret
	// command. Sorted for deterministic argv order.
	sources, err := secretSources(cfg, opts)
	if err != nil {
		return runtime.RunSpec{}, err
	}
	secretNames := make([]string, 0, len(sources))
	for name := range sources {
		secretNames = append(secretNames, name)
	}
	sort.Strings(secretNames)
	for _, name := range secretNames {
		addName(name)
	}

	// --git: make git "just work" inside the container. Trust the bind-mounted
	// workspace regardless of its owner uid (avoids git's "dubious ownership"
	// refusal when the host uid != the container user) via the GIT_CONFIG_*
	// env-config mechanism, and forward the host git identity by name (values
	// resolved on the real run path in injectGitIdentity, never in --dry-run).
	if opts.GitIdentity {
		env["GIT_CONFIG_COUNT"] = "1"
		env["GIT_CONFIG_KEY_0"] = "safe.directory"
		env["GIT_CONFIG_VALUE_0"] = "*"
		for _, n := range gitIdentityEnvNames {
			addName(n)
		}
	}

	// Resolve the hardening policy from config, then apply flag overrides.
	// --no-hardening is a debug escape hatch that reverts to the historical
	// "no cap-drop / no-new-privileges / no pids cap" behavior; it deliberately
	// does not touch the opt-in Memory/CPU limits.
	sec := cfg.Security
	noNewPriv := sec.NoNewPriv()
	capDrop := sec.CapDrop
	capAdd := sec.CapAdd
	pids := sec.Pids()
	if opts.NoHardening {
		noNewPriv = false
		capDrop = nil
		capAdd = nil
		pids = 0
	}
	memory := sec.Memory
	if opts.Memory != "" {
		memory = opts.Memory
	}
	cpus := sec.CPUs
	if opts.CPUs != "" {
		cpus = opts.CPUs
	}

	// Egress allowlist. Config `network.mode: allowlist` contributes the baseline
	// plus configured domains; `--allow DOMAIN` adds domains and, on its own,
	// switches the allowlist on. When active, the container needs a default-deny
	// egress firewall: that is programmed in-container at startup by the
	// sandbox-firewall entrypoint, which requires running as root with NET_ADMIN
	// and then drops back to the intended user (SANDBOX_RUN_AS) before the agent
	// runs. Allowlist implies bridge networking, so it overrides `none`.
	network := cfg.NetworkArg()
	egress := cfg.Network.EgressDomains()
	allowlist := cfg.Network.Mode == "allowlist" || len(opts.Allow) > 0
	if len(opts.Allow) > 0 {
		// --allow on its own switches the allowlist on, and the baseline comes with
		// it — unless the config explicitly declined it, which has to hold whether
		// or not that config also named the mode. Dedupe keeps baseline first and
		// makes this idempotent when EgressDomains already supplied it.
		if cfg.Network.BaselineEnabled() {
			egress = append(config.BaselineEgress(), egress...)
		}
		egress = config.DedupeDomains(append(egress, opts.Allow...))
	}
	// --no-hardening and the allowlist are mutually exclusive, and refusing is the
	// only honest outcome. The escape hatch is documented as reverting to the
	// historical behavior, but combined with an allowlist it lands *above* that
	// baseline rather than at it: the firewall needs the container to start as
	// root with NET_ADMIN, NET_RAW, SETUID and SETGID, and it is `cap_drop: ALL`
	// plus `no-new-privileges` that stop the guest holding on to them after the
	// drop to the sandbox user. Without those, the run has docker's full default
	// capability set *plus* those four, with setuid binaries live again — strictly
	// wider than a plain --no-hardening run, from a flag whose whole purpose is to
	// be no worse than the old default.
	//
	// Silently keeping the hardening would be the other option, and is worse: the
	// user asked for something and would not get it, with nothing said.
	if allowlist && opts.NoHardening {
		return runtime.RunSpec{}, fmt.Errorf(
			"--no-hardening cannot be combined with the egress allowlist: the firewall starts the " +
				"container as root with NET_ADMIN and drops privileges, and the hardening you are " +
				"disabling is what stops the guest regaining them — the combination is wider than " +
				"either alone. Drop one of the two")
	}

	// An allowlist that resolved to nothing must refuse, not run. The firewall is
	// wired below only when there are domains to permit, so an empty list would
	// otherwise hand back a container with no egress filtering at all — the
	// strictest request producing the weakest result. `mode: none` is how you ask
	// for a sandbox that reaches nothing.
	if allowlist && len(egress) == 0 {
		return runtime.RunSpec{}, fmt.Errorf(
			"network.mode is \"allowlist\" with baseline: false and no domains in network.allow — " +
				"that permits nothing, and would run with no egress firewall at all; " +
				"list the domains you need, or use network.mode: none")
	}
	dockerUser := user
	entrypoint := ""
	if allowlist {
		runAs := user
		if runAs == "" {
			runAs = "sandbox"
		}
		env["SANDBOX_EGRESS_ALLOW"] = strings.Join(egress, ",")
		env["SANDBOX_RUN_AS"] = runAs
		// NET_ADMIN/NET_RAW to program iptables; SETUID/SETGID so the entrypoint's
		// `setpriv` can drop root -> the intended user before the agent runs
		// (cap-drop ALL from hardening would otherwise block setresuid/setresgid).
		capAdd = append(capAdd, "NET_ADMIN", "NET_RAW", "SETUID", "SETGID")
		dockerUser = "root"
		entrypoint = "/usr/local/bin/sandbox-firewall"
		network = "" // allowlist requires bridge networking, not "none"
	}

	// --add-host passthrough and the --host-gateway convenience, which maps
	// host.docker.internal to the host so an agent can reach MCP servers running
	// on the host (automatic on Docker Desktop, but Linux needs it explicit).
	addHosts := append([]string(nil), opts.AddHosts...)
	if opts.HostGateway {
		addHosts = append(addHosts, "host.docker.internal:host-gateway")
	}

	// Published ports. Config `ports:` declares what a project normally needs and
	// --publish adds to it for one run, so a repo can check in its dev-server port
	// and a debugger port can still be opened ad hoc. Unlike every other reach in
	// here, this one points inward — so the address is resolved now (a bare spec
	// becomes 127.0.0.1) and the result is what --dry-run prints.
	ports, err := NormalizePublish(append(append([]string(nil), cfg.Ports...), opts.Publish...))
	if err != nil {
		return runtime.RunSpec{}, err
	}
	// `network: none` and a published port are a contradiction: docker would take
	// the flag and the port would never answer. Say so rather than hand back a
	// container that looks configured and isn't.
	if len(ports) > 0 && network == "none" {
		return runtime.RunSpec{}, fmt.Errorf("cannot publish ports with network mode \"none\": the container has no network to publish from")
	}
	// With an allowlist the firewall also default-denies *inbound* traffic, so the
	// published ports have to be named or they would be reachable from the host
	// and then refused inside the container — a dev server that silently stops
	// answering the moment --allow is added. This is the one deliberate ingress
	// carve-out, and it covers exactly what --dry-run already shows being
	// published. Set here rather than in the egress block above because the port
	// list is only resolved at this point.
	if allowlist {
		if in := IngressPorts(ports); len(in) > 0 {
			env["SANDBOX_INGRESS_PORTS"] = strings.Join(in, ",")
		}
	}

	tty := detectTTY()
	if opts.TTY != nil {
		tty = *opts.TTY
	}
	// A detached run has no terminal behind it, whatever the launching one looks
	// like. detectTTY() reports on the shell that typed the command, not on the
	// container — so without this an agent launched from a terminal is handed a
	// pty, starts its full-screen UI, and waits forever for a keystroke from
	// nobody. An explicit --tty does not override it: there is no terminal to give.
	if opts.Detach {
		tty = false
	}

	// Metrics require a terminal to report to. The live gauge is drawn only for
	// non-interactive runs (an interactive agent TUI owns the terminal); the
	// post-run summary is printed for all runs, including interactive ones, since
	// it only appears after the session ends. A detached run gets neither: nothing
	// is watching, and this process exits long before the container does.
	metricsOn := !opts.NoMetrics && !opts.Detach && isTerminal(os.Stderr)
	showMetrics := metricsOn && !tty
	showSummary := metricsOn

	// Labels are how a container is found again after this process is gone.
	// Empty values are omitted rather than stamped blank, so a label that is
	// present always carries a fact.
	labels := map[string]string{}
	for k, v := range map[string]string{
		"sandbox.repo":   opts.RepoID,
		"sandbox.branch": opts.Branch,
		"sandbox.agent":  opts.Agent,
		"sandbox.base":   opts.Base,
	} {
		if v != "" {
			labels[k] = v
		}
	}
	if len(labels) == 0 {
		labels = nil
	}

	// Remove is the single deliberate exception to the disposable-container rule:
	// a detached container is retained after it exits, because its exit code and
	// its logs are the only record that the run happened at all and --rm would
	// delete both at the moment they become interesting. Reaping is a later,
	// explicit step.
	return runtime.RunSpec{
		Image:    image,
		Name:     containerName(opts),
		Workdir:  workdir,
		Command:  opts.Command,
		TTY:      tty,
		Detach:   opts.Detach,
		Labels:   labels,
		Remove:   !opts.Detach,
		Hostname: cfg.Hostname,
		Home:     cfg.Home,
		User:     dockerUser,
		Network:  network,
		Runtime:  runtimeName,
		Env:      env,
		EnvNames: envNames,
		Mounts:   mounts,
		AddHosts: addHosts,
		Ports:    ports,

		Entrypoint: entrypoint,

		NoNewPrivileges: noNewPriv,
		Seccomp:         sec.Seccomp,
		CapDrop:         capDrop,
		CapAdd:          capAdd,
		PidsLimit:       pids,
		Memory:          memory,
		CPUs:            cpus,

		ShowMetrics: showMetrics,
		ShowSummary: showSummary,
		Branch:      opts.Branch,
	}, nil
}

// containerName returns a docker-valid container name. Foreground runs get a
// timestamp, which keeps repeated runs of one project from colliding.
//
// Detached runs get a deterministic sandbox-<repo>-<branch> instead, and that is
// load-bearing rather than cosmetic: docker refuses a duplicate container name
// atomically, so the name itself enforces one-agent-per-branch. The alternative —
// listing containers and checking before launching — has a window between the
// check and the launch in which a second launch passes the same check, and two
// agents in one checkout is silent data loss.
//
// Without a repo and branch to build from there is nothing to be deterministic
// about, so it falls back to the timestamp.
func containerName(opts Options) string {
	if opts.Detach {
		repo := worktree.SanitizeName(opts.RepoID)
		branch := worktree.SanitizeName(opts.Branch)
		if repo != "" && branch != "" {
			return "sandbox-" + repo + "-" + branch
		}
	}
	return "sandbox-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

func workdirTargetOrDefault(workdir string) string {
	if workdir == "" {
		return "/workspace"
	}
	return workdir
}

// parseMount parses "host:container[:ro|rw]". Host ~ is expanded. Missing mode
// defaults to read-only (the conservative default).
func parseMount(raw string) (runtime.Mount, error) {
	parts := strings.Split(raw, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return runtime.Mount{}, fmt.Errorf("invalid --mount %q: want host:container[:ro|rw]", raw)
	}
	host := config.ExpandTilde(parts[0])
	container := parts[1]
	ro := true
	if len(parts) == 3 {
		switch parts[2] {
		case "ro":
			ro = true
		case "rw":
			ro = false
		default:
			return runtime.Mount{}, fmt.Errorf("invalid --mount mode %q in %q: want ro or rw", parts[2], raw)
		}
	}
	if host == "" || container == "" {
		return runtime.Mount{}, fmt.Errorf("invalid --mount %q: host and container must be non-empty", raw)
	}
	return runtime.Mount{Source: host, Target: container, RO: ro}, nil
}

// secretSources merges config-declared secrets with --secret flags into a single
// name -> source map (flags win on a name clash). It is pure (parsing + tilde
// expansion only, no I/O), so both BuildSpec (to forward names) and Session.Run
// (to resolve values) can call it without side effects.
func secretSources(cfg config.Config, opts Options) (map[string]creds.Source, error) {
	out := map[string]creds.Source{}
	for name, s := range cfg.Secrets {
		file := s.File
		if file != "" {
			file = config.ExpandTilde(file)
		}
		out[name] = creds.Source{File: file, Command: s.Command, Env: s.Env}
	}
	for _, raw := range opts.Secrets {
		name, src, err := parseSecretFlag(raw)
		if err != nil {
			return nil, err
		}
		out[name] = src
	}
	return out, nil
}

// parseSecretFlag parses "NAME=file:PATH", "NAME=cmd:COMMAND", or "NAME=env:VAR".
func parseSecretFlag(raw string) (string, creds.Source, error) {
	name, spec, ok := strings.Cut(raw, "=")
	if !ok || name == "" || spec == "" {
		return "", creds.Source{}, fmt.Errorf("invalid --secret %q: want NAME=file:PATH | cmd:COMMAND | env:VAR", raw)
	}
	scheme, val, ok := strings.Cut(spec, ":")
	if !ok || val == "" {
		return "", creds.Source{}, fmt.Errorf("invalid --secret %q: source must be file:PATH, cmd:COMMAND, or env:VAR", raw)
	}
	switch scheme {
	case "file":
		return name, creds.Source{File: config.ExpandTilde(val)}, nil
	case "cmd":
		return name, creds.Source{Command: val}, nil
	case "env":
		return name, creds.Source{Env: val}, nil
	default:
		return "", creds.Source{}, fmt.Errorf("invalid --secret %q: unknown source %q (use file, cmd, or env)", raw, scheme)
	}
}

// detectTTY reports whether stdin and stdout are both terminals.
func detectTTY() bool {
	return isTerminal(os.Stdin) && isTerminal(os.Stdout)
}

func isTerminal(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

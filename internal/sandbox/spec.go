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
	"path"
	"path/filepath"
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

	// Fleet marks this container as launched by `fleet run` rather than by an
	// interactive command, so the fleet's own stop/clean/slot-counting reach only
	// what the fleet started.
	Fleet bool

	// Verify is the task's definition of done — a shell command the container runs
	// after the agent. This field is the *record* of it, stamped as a label so a
	// later command can tell a run that had no check from one that passed its
	// check; the command itself travels in Command, wrapped around the agent's
	// argv (internal/fleet.withVerify).
	Verify string

	// Prompt is what the agent was asked to do, for the record only — exactly
	// like Verify. The prompt that actually runs travels inside Command, built
	// by the agent descriptor; this is the same text handed over separately so
	// it can be stamped as a label and read back without parsing an
	// agent-specific argv to find which position holds it.
	//
	// Callers that build Command themselves (a plain `run -- cmd`) leave it
	// empty, and the label is then omitted rather than stamped blank: a label
	// that is present always carries a fact.
	Prompt string

	// Baseline is a crash-snapshot commit taken just before this run starts, so
	// its changes can later be told apart from whatever was already uncommitted
	// in the workspace. Empty when no snapshot could be taken — not a git
	// repository, snapshots switched off — and the label is then omitted, which
	// is what makes "we cannot attribute this precisely" a state a client can
	// see rather than one it has to infer.
	Baseline string

	// AuthPersistDir, when non-empty, is a host directory bind-mounted read-write
	// as the agent's whole HOME so its login/config survives the ephemeral
	// container (log in once). Set by the claude/codex wrappers.
	AuthPersistDir string
}

// boolLabel renders a flag as a label value, or "" so the omit-when-empty rule
// above drops it: a container that is not a fleet container carries no
// sandbox.fleet label at all, rather than one saying "false" that a filter would
// have to know to exclude.
func boolLabel(b bool) string {
	if b {
		return "1"
	}
	return ""
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

	// Where the workspace is mounted, and where the guest starts, are two
	// questions that `workdir` answers at once — and they are not always the same
	// answer:
	//
	//   --workdir /app             mount the project at /app and start there
	//   --workdir /workspace/sub   keep the mount, start in a subdirectory
	//
	// Deciding by whether the path is inside the mount covers both. Previously the
	// flag moved only `-w`, so `--workdir /app` started the guest in a directory
	// that did not exist while the project sat at /workspace; and config `workdir:`
	// moved both, so the two spellings of one setting disagreed.
	wsTarget := workdirTargetOrDefault(cfg.Workdir)
	if opts.Workdir != "" && !isPathAncestor(wsTarget, opts.Workdir) && opts.Workdir != wsTarget {
		wsTarget = opts.Workdir
	}
	// The workspace target is caller-influenced, so it is checked like any other:
	// a target that shadows the container's own binaries hands the repository
	// control of what the entrypoint runs.
	if err := ValidateMountTarget(wsTarget); err != nil {
		return runtime.RunSpec{}, fmt.Errorf("workdir %q: %w", wsTarget, err)
	}
	if err := ValidateMountPath("workspace path", ws); err != nil {
		return runtime.RunSpec{}, err
	}
	mounts := []runtime.Mount{WorkspaceMount(ws, wsTarget)}

	// .git/hooks read-only, layered over the read-write workspace.
	//
	// The agent has to be able to edit project files, but hooks are not project
	// source: they are programs the *user's* git runs later, on the host, as them.
	// An agent writing .git/hooks/pre-commit is not editing the project, it is
	// waiting for the user's next commit — confirmed as a live escape.
	//
	// hooks specifically, and not .git as a whole: agents legitimately run
	// `git config`, and git writes indexes, refs and logs constantly. Changes to
	// .git/config are reported at exit instead — detected rather than prevented,
	// because preventing them would break ordinary work for a smaller gain.
	if hooks := filepath.Join(ws, ".git", "hooks"); isExistingDir(hooks) {
		mounts = append(mounts, runtime.Mount{
			Source: hooks,
			Target: path.Join(wsTarget, ".git", "hooks"),
			RO:     true,
		})
	}

	// Config-declared mounts (host paths already resolved to absolute at load time).
	for _, m := range cfg.Mounts {
		if err := ValidateMountTarget(m.Container); err != nil {
			return runtime.RunSpec{}, err
		}
		if err := ValidateMountPath("host path", m.Host); err != nil {
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
		if err := ValidateMountPath("host path", m.Source); err != nil {
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
	// Every sandbox joins one shared network with inter-container communication
	// disabled, rather than the default bridge where each can reach all the others.
	// `network: none` still means none.
	network := cfg.NetworkArg()
	if network != "none" {
		network = runtime.SandboxNetwork
	}
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
	// `--user root` and the allowlist are the same contradiction as --no-hardening,
	// and were not refused. The firewall needs the container to *start* as root,
	// then drop — and the drop is skipped when the requested user resolves to uid
	// 0, so the agent keeps NET_ADMIN in its effective set and can simply
	// `iptables -F OUTPUT` and reach anything. Confirmed: flushed, then connected
	// to a non-allowlisted address. An allowlist the guest can switch off is worse
	// than no allowlist, because sandbox-cli reports it is enforcing one.
	if allowlist && isRootUser(user) {
		return runtime.RunSpec{}, fmt.Errorf(
			"--user root cannot be combined with the egress allowlist: the firewall drops privileges "+
				"after programming itself, and a guest left as root keeps NET_ADMIN and can flush the "+
				"rules. Drop one of the two, or pass --network default to run this one without the "+
				"allowlist (which already runs the container as root for setup, then drops to %q)",
			defaultRunAsUser)
	}
	if allowlist && opts.NoHardening {
		return runtime.RunSpec{}, fmt.Errorf(
			"--no-hardening cannot be combined with the egress allowlist: the firewall starts the " +
				"container as root with NET_ADMIN and drops privileges, and the hardening you are " +
				"disabling is what stops the guest regaining them — the combination is wider than " +
				"either alone. Drop one of the two, or pass --network default to run this one without the allowlist")
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
		// Turns on name-based enforcement: the entrypoint starts the proxy on this
		// port and redirects the guest's 80/443 into it, so the allowlist is decided
		// on the hostname the client asked for rather than on addresses resolved
		// once at startup. A container-local port, never published.
		env["SANDBOX_PROXY_PORT"] = defaultProxyPort
		// NET_ADMIN/NET_RAW to program iptables; SETUID/SETGID so the entrypoint's
		// `setpriv` can drop root -> the intended user before the agent runs
		// (cap-drop ALL from hardening would otherwise block setresuid/setresgid).
		// Copied first: append onto a slice with spare capacity would write
		// through into cfg.Security.CapAdd's backing array, mutating the caller's
		// config. BuildArgs is documented as pure and BuildSpec is held to the
		// same standard.
		capAdd = append(append([]string(nil), capAdd...), "NET_ADMIN", "NET_RAW", "SETUID", "SETGID")
		dockerUser = "root"
		entrypoint = "/usr/local/bin/sandbox-firewall"
		network = runtime.SandboxNetwork // allowlist needs networking, not "none"
	}

	// --add-host passthrough and the --host-gateway convenience, which maps
	// host.docker.internal to the host so an agent can reach MCP servers running
	// on the host (automatic on Docker Desktop, but Linux needs it explicit).
	addHosts := append([]string(nil), opts.AddHosts...)
	if opts.HostGateway {
		addHosts = append(addHosts, "host.docker.internal:host-gateway")
	} else {
		// Without --host-gateway, point the host names at the container's own
		// loopback so they resolve to nothing useful.
		//
		// spec.go used to treat host.docker.internal as something --host-gateway
		// switched on. On Docker Desktop it resolves unconditionally, so it was
		// never off: a sandbox with no flags at all read a file from a service the
		// user had bound to 127.0.0.1 precisely so nothing else could reach it.
		// The absence of a flag was not a defence.
		//
		// This blocks the *name*, which is the documented and discoverable path.
		// In allowlist mode the gateway address is refused as well, since it is on
		// no allowlist; in default mode there is no firewall and a raw address is
		// still reachable — that is the residue, and it is tracked with the rest of
		// the default-mode isolation work.
		for _, name := range []string{"host.docker.internal", "gateway.docker.internal"} {
			if !namesHost(opts.AddHosts, name) {
				addHosts = append(addHosts, name+":127.0.0.1")
			}
		}
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
	// sandbox.cli is stamped unconditionally, and that is the point: every other
	// label describes the *work* (which repo, branch, agent) and is omitted when
	// there is nothing true to say — so a run outside a git repository carried no
	// labels at all and could not be found again by `sandbox-cli ps`. A container
	// nobody can list is one nobody can stop, and a killed sandbox-cli leaves the
	// container running with the workspace still mounted.
	labels := map[string]string{LabelCLI: "1"}
	for k, v := range map[string]string{
		LabelRepo:     opts.RepoID,
		LabelBranch:   opts.Branch,
		LabelAgent:    opts.Agent,
		LabelBase:     opts.Base,
		LabelVerify:   opts.Verify,
		LabelFleet:    boolLabel(opts.Fleet),
		LabelProfile:  cfg.Profile,
		LabelPrompt:   truncatePrompt(opts.Prompt),
		LabelBaseline: opts.Baseline,
	} {
		if v != "" {
			labels[k] = v
		}
	}

	// Remove is the single deliberate exception to the disposable-container rule:
	// a detached container is retained after it exits, because its exit code and
	// its logs are the only record that the run happened at all and --rm would
	// delete both at the moment they become interesting. Reaping is a later,
	// explicit step.
	return runtime.RunSpec{
		Image:           image,
		Name:            containerName(opts),
		Workdir:         workdir,
		Command:         opts.Command,
		TTY:             tty,
		Detach:          opts.Detach,
		Labels:          labels,
		Remove:          !opts.Detach,
		Hostname:        cfg.Hostname,
		Home:            cfg.Home,
		User:            hostMappedUser(cfg.Engine, dockerUser),
		Network:         network,
		HostUserMapping: hostUserMapping(cfg.Engine),
		Runtime:         runtimeName,
		Env:             env,
		EnvNames:        envNames,
		Mounts:          mounts,
		AddHosts:        addHosts,
		Ports:           ports,

		Entrypoint: entrypoint,

		NoNewPrivileges: noNewPriv,
		Seccomp:         seccompArg(sec.Seccomp),
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
	if !config.ValidEnvName(name) {
		return "", creds.Source{}, fmt.Errorf("invalid --secret %q: %q is not a valid environment variable name", raw, name)
	}
	if config.IsReservedEnv(name) {
		return "", creds.Source{}, fmt.Errorf("invalid --secret %q: %s", raw, config.ReservedEnvReason())
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

// defaultProxyPort is where the in-container egress proxy listens. Fixed rather
// than chosen per run: it is bound to loopback inside the container's own network
// namespace, so it collides with nothing outside and needs no coordination.
const defaultProxyPort = "3128"

// defaultRunAsUser is the unprivileged user the firewall entrypoint drops to.
const defaultRunAsUser = "sandbox"

// isRootUser reports whether a --user/config value asks to run as uid 0, by name
// or by number. The entrypoint makes the same judgement on the resolved uid, so
// checking only the spelling "root" would miss `--user 0` and `--user 0:0`.
func isRootUser(user string) bool { return config.IsRootUser(user) }

// isExistingDir reports whether p is a directory that is already there.
func isExistingDir(p string) bool {
	fi, err := os.Stat(p)
	return err == nil && fi.IsDir()
}

// namesHost reports whether the caller already mapped this hostname themselves.
// An explicit --add-host is a deliberate act and must win: /etc/hosts resolution
// takes the first match, so sandbox-cli adding its own entry for the same name
// would silently override what the user asked for.
func namesHost(addHosts []string, name string) bool {
	for _, h := range addHosts {
		if n, _, ok := strings.Cut(h, ":"); ok && strings.EqualFold(strings.TrimSpace(n), name) {
			return true
		}
	}
	return false
}

// seccompArg turns the Seccomp config value into what docker should be handed.
//
// SeccompRequired is a *policy* — "refuse unless the daemon applies a filter" —
// not a profile reference, and Session.enforceSeccomp is what acts on it. Docker
// treats any seccomp= value other than "unconfined" as a path to a profile file
// and reads it client-side, so letting the sentinel through rendered
// `--security-opt seccomp=required` and every prod run died with
// "opening seccomp profile (required) failed". The policy has been checked by
// the time a container is started; what docker wants here is the daemon default,
// which is spelled by saying nothing.
func seccompArg(v string) string {
	if v == config.SeccompRequired {
		return ""
	}
	return v
}

// sandboxUID/sandboxGID are the image's unprivileged user, numerically. The
// engines need the number rather than the name in one specific place; see
// hostMappedUser.
const (
	sandboxUID = "1001"
	sandboxGID = "1001"
)

// hostUserMapping is the --userns value for the engine, or "" when it needs
// none.
//
// Only podman does. Rootless podman maps the host user to container uid 0, so a
// bind-mounted workspace appears root-owned and the sandbox user cannot write to
// it — measured on native Linux (Fedora, SELinux enforcing): today's flags fail
// both read and write. keep-id remaps so container 1001 *is* the host user.
//
// Docker gets "" and its argv is unchanged, which is what keeps the golden
// --dry-run test honest rather than merely passing.
func hostUserMapping(engine string) string {
	if engine != "podman" {
		return ""
	}
	return "keep-id:uid=" + sandboxUID + ",gid=" + sandboxGID
}

// hostMappedUser renders the container user numerically under podman.
//
// `--user sandbox` sets the uid but leaves the group at 0, so a file written
// into the workspace came back owned by host-uid:subgid (501:100000) — the right
// user, a group the host does not have. `--user 1001:1001` maps both, and the
// file lands as the host's own uid:gid (501:1000). The name is kept for docker,
// where it is more legible and the mapping question does not arise.
//
// An explicitly requested user is left exactly as the caller wrote it: they have
// said what they want, and second-guessing it here would be the surprise.
func hostMappedUser(engine, user string) string {
	if engine != "podman" || user != defaultRunAsUser {
		return user
	}
	return sandboxUID + ":" + sandboxGID
}

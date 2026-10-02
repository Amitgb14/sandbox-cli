package studioapi

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

// listRuns returns every container this tool started, newest first, optionally
// narrowed by extra labels. Without all, containers that have finished are
// dropped — the same rule `sandbox-cli list` uses (created/restarting/paused
// still show; only exited/dead are hidden), so the two stay consistent for
// someone using both.
func (s *Server) listRuns(ctx context.Context, all bool, filter map[string]string) ([]runtime.ContainerInfo, error) {
	labels := map[string]string{sandbox.LabelCLI: "1"}
	for k, v := range filter {
		labels[k] = v
	}
	infos, err := s.RT.Containers(ctx, labels)
	if err != nil {
		return nil, fmt.Errorf("listing containers: %w", err)
	}
	if all {
		return infos, nil
	}
	live := make([]runtime.ContainerInfo, 0, len(infos))
	for _, c := range infos {
		if c.State == "exited" || c.State == "dead" {
			continue
		}
		live = append(live, c)
	}
	return live, nil
}

// resolveRun finds the one run a reference names: an id, a container name, or a
// branch — the same three sandbox-cli list shows and kill/logs/attach accept.
//
// The rule this exists to uphold is the one internal/cli's session commands are
// built on: a reference is matched against a listing filtered to this tool's own
// containers and is never handed to the engine to resolve. Ambiguity refuses and
// lists the candidates rather than guessing.
func (s *Server) resolveRun(ctx context.Context, ref string) (runtime.ContainerInfo, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return runtime.ContainerInfo{}, fmt.Errorf("empty run id")
	}
	infos, err := s.listRuns(ctx, true, nil)
	if err != nil {
		return runtime.ContainerInfo{}, err
	}
	var exact, prefix []runtime.ContainerInfo
	for _, c := range infos {
		switch {
		case c.ID == ref || shortID(c.ID) == ref || c.Name == ref || c.Labels[sandbox.LabelBranch] == ref:
			exact = append(exact, c)
		case strings.HasPrefix(c.ID, ref) || strings.HasPrefix(c.Name, ref):
			prefix = append(prefix, c)
		}
	}
	for _, tier := range [][]runtime.ContainerInfo{exact, prefix} {
		switch len(tier) {
		case 0:
			continue
		case 1:
			return tier[0], nil
		}
		// The single exception to refusing ambiguity: with exactly one candidate
		// still running, a reference to work in progress can only mean that one.
		if live := runningOnly(tier); len(live) == 1 {
			return live[0], nil
		}
		return runtime.ContainerInfo{}, ambiguousRunError(ref, tier)
	}
	return runtime.ContainerInfo{}, fmt.Errorf("no run matches %q", ref)
}

func runningOnly(infos []runtime.ContainerInfo) []runtime.ContainerInfo {
	var out []runtime.ContainerInfo
	for _, c := range infos {
		if c.Running() {
			out = append(out, c)
		}
	}
	return out
}

func ambiguousRunError(ref string, matches []runtime.ContainerInfo) error {
	var b strings.Builder
	fmt.Fprintf(&b, "%q matches %d runs; name one by id:", ref, len(matches))
	for _, c := range matches {
		fmt.Fprintf(&b, " %s", shortID(c.ID))
	}
	return fmt.Errorf("%s", b.String())
}

// repoNameFromID recovers the display name from a repo id.
//
// worktree.RepoID builds the id as `filepath.Base(root) + "-" + sha256(root)[:8]`,
// so this inverts that construction rather than guessing: an id that does not
// end in a dash and eight hex digits is handed back whole, because the honest
// answer for an id this does not recognise is the id.
//
// Derived rather than looked up in the projects registry on purpose — a run may
// belong to a repository nobody registered (an old container, or one launched
// from the CLI in a checkout Studio has never been pointed at), and a name that
// existed only for registered repositories would leave exactly those rows blank.
// It is a *display* name either way: matching is RepoID's job.
func repoNameFromID(id string) string {
	if len(id) < 10 {
		return id
	}
	suffix := id[len(id)-9:]
	if suffix[0] != '-' {
		return id
	}
	for _, r := range suffix[1:] {
		if !(r >= '0' && r <= '9') && !(r >= 'a' && r <= 'f') {
			return id
		}
	}
	return id[:len(id)-9]
}

// shortID is the 12-character form docker itself displays, and the one this API
// accepts back as Run.ID — mirrors internal/cli's shortID so ids agree between
// `sandbox-cli list` and GET /runs.
func shortID(id string) string {
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

// toRun converts a docker-reported container into the wire Run type. Every
// field comes from runtime.ContainerInfo or its sandbox.* labels — never
// re-derived, since docker is the state store.
// workspaceDir is where every sandbox mounts the project. It is not exported by
// internal/sandbox, so it is stated once here rather than spelled inline at the
// one place that has to recognise which of several mounts is the workspace.
const workspaceDir = "/workspace"

func toRun(c runtime.ContainerInfo, engine string) Run {
	r := Run{
		ID:          shortID(c.ID),
		ContainerID: c.ID,
		Name:        c.Name,
		Kind:        RunKindInteractive,
		State:       toRunState(c.State),
		Detached:    !c.OpenStdin && !c.TTY,
		RepoID:      c.Labels[sandbox.LabelRepo],
		RepoName:    repoNameFromID(c.Labels[sandbox.LabelRepo]),
		RoutedFrom:  c.Labels[sandbox.LabelRoutedFrom],
		RouteReason: c.Labels[sandbox.LabelRouteReason],
		// The episode, and where in it. Read from the container rather than from
		// the audit log because a detached run's audit line is written when it
		// ends, and these are the fields a listing of *running* containers needs
		// to show a failover as one thing rather than two.
		RouteID:      c.Labels[sandbox.LabelRouteID],
		RouteAttempt: atoiOrZero(c.Labels[sandbox.LabelRouteAttempt]),
		Branch:       c.Labels[sandbox.LabelBranch],
		Base:         c.Labels[sandbox.LabelBase],
		Agent:        c.Labels[sandbox.LabelAgent],
		Verify:       c.Labels[sandbox.LabelVerify],
		Profile:      c.Labels[sandbox.LabelProfile],
		Prompt:       c.Labels[sandbox.LabelPrompt],
		CreatedAt:    c.CreatedAt,
		OpenStdin:    c.OpenStdin,
		TTY:          c.TTY,

		Image:    c.Image,
		Command:  c.Command,
		Workdir:  c.Workdir,
		Engine:   engine,
		EnvNames: c.EnvNames(),
		Network:  runNetwork(c),
		Security: runSecurity(c),
	}

	r.Mounts = make([]RunMount, 0, len(c.Mounts))
	for _, m := range c.Mounts {
		mode := "ro"
		if m.ReadWrite {
			mode = "rw"
		}
		r.Mounts = append(r.Mounts, RunMount{
			Host:      m.Source,
			Container: m.Destination,
			Mode:      mode,
			Origin:    mountOrigin(m.Destination),
		})
		// The workspace is named by where it lands, not by its host path: the
		// host path is whatever project this was, and /workspace is the one
		// destination every sandbox has.
		if m.Destination == workspaceDir {
			r.Workspace = m.Source
		}
	}
	if c.Labels[sandbox.LabelFleet] != "" {
		r.Kind = RunKindFleet
	}
	if c.State == "exited" {
		ec := c.ExitCode
		r.ExitCode = &ec
	}
	if !c.StartedAt.IsZero() {
		t := c.StartedAt
		r.StartedAt = &t
	}
	if !c.FinishedAt.IsZero() {
		t := c.FinishedAt
		r.FinishedAt = &t
	}
	// Only for a run that has both ends. A running container has no duration
	// yet, and reporting "now minus start" would be a number that changes on
	// every poll for a field a client will read as final.
	if !c.StartedAt.IsZero() && !c.FinishedAt.IsZero() && c.FinishedAt.After(c.StartedAt) {
		ms := c.FinishedAt.Sub(c.StartedAt).Milliseconds()
		r.DurationMS = &ms
	}
	return r
}

func toRunState(s string) RunState {
	switch RunState(s) {
	case RunStateCreated, RunStateRunning, RunStatePaused, RunStateRestarting, RunStateExited, RunStateDead:
		return RunState(s)
	default:
		return RunStateUnknown
	}
}

// runNetwork reads a run's egress posture back off the container.
//
// Mode is this tool's vocabulary, not docker's: the container reports a network
// name, which says nothing about whether an allowlist was programmed. The
// control variable the entrypoint acts on does, and it carries the resolved
// domain list — baseline ∪ configured — which is what the firewall and the
// proxy actually enforce. That makes the list authoritative in a way a config
// file is not: it is what the container was handed, not what someone asked for.
func runNetwork(c runtime.ContainerInfo) RunNetwork {
	n := RunNetwork{Mode: "default", NetworkName: c.NetworkMode, Allow: []string{}}
	if c.NetworkMode == "none" {
		n.Mode = "none"
	}

	allow := envValue(c, "SANDBOX_EGRESS_ALLOW")
	if allow == "" {
		return n
	}
	n.Mode = "allowlist"
	n.Allow = strings.Split(allow, ",")

	// The baseline is a set, so membership of any one of its domains is the
	// question — a list built with `baseline: false` contains none of them.
	for _, d := range config.BaselineEgress() {
		if slices.Contains(n.Allow, d) {
			n.Baseline = true
			break
		}
	}

	// Always "name" while the in-container proxy exists: the firewall matches
	// resolved addresses, and the proxy is what closes the gap between an address
	// and a hostname. Reported rather than assumed by the client, so the day a
	// run is enforced by address alone it says so.
	enforcement := "name"
	n.Enforcement = &enforcement

	for _, p := range strings.Split(envValue(c, "SANDBOX_INGRESS_PORTS"), ",") {
		if v, err := strconv.Atoi(strings.TrimSpace(p)); err == nil {
			n.IngressPorts = append(n.IngressPorts, v)
		}
	}
	return n
}

// runSecurity reads a run's confinement back off the container.
func runSecurity(c runtime.ContainerInfo) RunSecurity {
	s := RunSecurity{
		CapDrop:   c.Security.CapDrop,
		CapAdd:    c.Security.CapAdd,
		PidsLimit: c.Security.PidsLimit,
		User:      c.User,
		Seccomp:   "default",
		// The OCI runtime belongs here rather than beside the image: it is the one
		// setting that changes the *kind* of boundary rather than its degree, and
		// a security block that lists capabilities and seccomp while staying silent
		// about the kernel the container is on describes the smaller half. The CLI
		// listing grew the same fact at the same time; two front ends disagreeing
		// about what is knowable about one container is the drift internal/doctor
		// was extracted to prevent for seccomp.
		Runtime:           c.Runtime,
		StrongerIsolation: c.StrongerIsolation(),
	}
	if s.CapDrop == nil {
		s.CapDrop = []string{}
	}
	if s.CapAdd == nil {
		s.CapAdd = []string{}
	}
	for _, opt := range c.Security.SecurityOpt {
		switch {
		case opt == "no-new-privileges" || opt == "no-new-privileges:true":
			s.NoNewPrivileges = true
		case strings.HasPrefix(opt, "seccomp="):
			s.Seccomp = strings.TrimPrefix(opt, "seccomp=")
		}
	}
	// Empty rather than "0": a limit of zero reads as "no memory allowed", and
	// what is true is that no limit was set at all.
	if c.Security.MemoryBytes > 0 {
		s.Memory = fmt.Sprintf("%dm", c.Security.MemoryBytes/(1024*1024))
	}
	if c.Security.NanoCPUs > 0 {
		s.CPUs = strconv.FormatFloat(float64(c.Security.NanoCPUs)/1e9, 'g', -1, 64)
	}
	// The hardening this tool applies by default, stated once so a client does
	// not re-derive it from four fields and get it subtly wrong.
	s.Hardening = s.NoNewPrivileges && slices.Contains(s.CapDrop, "ALL")
	return s
}

// mountOrigin names why a mount exists, from where it lands. Only the
// destinations sandbox-cli chooses itself are named; anything else came from a
// --mount or a config entry and has no origin this can honestly claim to know.
func mountOrigin(dest string) string {
	switch {
	case dest == workspaceDir:
		return "workspace"
	case strings.HasSuffix(dest, "/.git") || strings.Contains(dest, "/.git/"):
		return "worktree-git"
	case dest == "/sandbox/home":
		return "persisted-home"
	case strings.Contains(dest, "/.claude/projects"):
		return "history"
	case strings.HasPrefix(dest, "/shared"):
		return "share"
	default:
		return ""
	}
}

// envValue returns the value of one container environment variable, or "".
// Used only for sandbox-cli's own control variables, whose values are settings
// rather than secrets — nothing here reads a forwarded credential.
func envValue(c runtime.ContainerInfo, name string) string {
	prefix := name + "="
	for _, kv := range c.Env {
		if strings.HasPrefix(kv, prefix) {
			return strings.TrimPrefix(kv, prefix)
		}
	}
	return ""
}

// atoiOrZero reads a numeric label, treating anything unreadable as absent.
//
// A label is text stamped by a launcher that may be an older build of this tool,
// so a value that is not a number is a fact this daemon does not have — the same
// bargain every other optional label makes — rather than a reason to fail a
// listing.
func atoiOrZero(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// Package sandbox composes config, image building, and the runtime backend into
// a single Session that resolves a request and runs it in an isolated container.
package sandbox

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/audit"
	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/creds"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"time"
)

// Session ties a resolved config to a runtime backend and an audit sink.
type Session struct {
	Cfg     config.Config
	Runtime runtime.Runtime
	Audit   audit.Sink
}

// New returns a Session with the given config, the docker CLI backend, and a
// no-op audit sink (the audit seam is a stub in the MVP).
func New(cfg config.Config) *Session {
	return &Session{
		Cfg:     cfg,
		Runtime: runtime.NewEngine(cfg.Engine),
		Audit:   audit.NewJSONLSink(config.AuditDir()),
	}
}

// Prepare resolves options into a RunSpec without executing anything. Used by
// --dry-run and by Run.
func (s *Session) Prepare(opts Options) (runtime.RunSpec, error) {
	return BuildSpec(s.Cfg, opts)
}

// Run resolves the options and executes the container, returning the guest exit
// code. forceBuild rebuilds the base image even if it already exists locally.
func (s *Session) Run(ctx context.Context, opts Options, forceBuild bool) (int, error) {
	spec, err := s.Prepare(opts)
	if err != nil {
		return 1, err
	}
	if err := s.Runtime.Available(ctx); err != nil {
		return 1, err
	}
	// Before the image build, not after: a policy check that needs no image at
	// all should not cost minutes of `docker build` before it refuses.
	if err := s.enforceSeccomp(ctx); err != nil {
		return 1, err
	}
	if err := s.Runtime.EnsureImage(ctx, spec.Image, forceBuild); err != nil {
		return 1, fmt.Errorf("preparing image %q: %w", spec.Image, err)
	}
	if err := s.Runtime.EnsureNetwork(ctx, spec.Network); err != nil {
		return 1, err
	}
	// Said once, on the way in, while the user can still do something about it.
	// Skipped when the policy required one: enforceSeccomp has already asked the
	// daemon and refused if the answer was no, so warning here would be a second
	// `docker info` for a question whose answer cannot be bad.
	if w, ok := s.Runtime.(interface{ WarnIfSeccompDisabled(context.Context) }); ok &&
		s.Cfg.Security.Seccomp != config.SeccompRequired {
		w.WarnIfSeccompDisabled(ctx)
	}

	// Resolve brokered secrets and the host git identity, and hand the values to
	// the runtime for the docker child only — never into this process's own
	// environment, which a secret named PATH or DOCKER_HOST would otherwise use to
	// redirect the subprocesses we spawn afterwards. Done only here, on the real
	// run path and never in Prepare/--dry-run, so a secret command is not executed
	// just to print the command. BuildSpec already added the names to the spec.
	fwd, err := forwardedValues(s.Cfg, opts)
	if err != nil {
		return 1, err
	}
	spec.ForwardedEnv = fwd

	if canObserveDenials(spec) {
		spec.Denials = &runtime.EgressDenials{}
	}

	// Recorded after the run, not before it: an audit line whose whole purpose is
	// "what did this do and how did it end" is not worth much written at the
	// moment nothing has happened yet.
	started := time.Now()
	code, runErr := s.Runtime.Run(ctx, spec)
	s.Audit.RecordSession(auditMeta(s.Cfg, spec, opts, code, time.Since(started)))
	return code, runErr
}

// Start launches the container detached and returns its name, without waiting
// for the guest. It runs the identical preflight as Run and resolves the spec
// through the identical BuildSpec, so a detached run is isolated exactly as its
// foreground twin is; the only thing that differs is that nothing here waits.
func (s *Session) Start(ctx context.Context, opts Options, forceBuild bool) (string, error) {
	opts.Detach = true
	spec, err := s.Prepare(opts)
	if err != nil {
		return "", err
	}
	if err := s.Runtime.Available(ctx); err != nil {
		return "", err
	}
	// Built here, before the container starts, and deliberately not left to the
	// launch itself: a fan-out of detached runs against a cold image would
	// otherwise trigger one concurrent build per container.
	// Before the image build, not after: a policy check that needs no image at
	// all should not cost minutes of `docker build` before it refuses.
	if err := s.enforceSeccomp(ctx); err != nil {
		return "", err
	}
	if err := s.Runtime.EnsureImage(ctx, spec.Image, forceBuild); err != nil {
		return "", fmt.Errorf("preparing image %q: %w", spec.Image, err)
	}
	if err := s.Runtime.EnsureNetwork(ctx, spec.Network); err != nil {
		return "", err
	}

	// Same rule as Run: resolved only on a real launch path, never in
	// Prepare/--dry-run, and reaching the container by name rather than on the argv.
	fwd, err := forwardedValues(s.Cfg, opts)
	if err != nil {
		return "", err
	}
	spec.ForwardedEnv = fwd

	started := time.Now()
	name, startErr := s.Runtime.Start(ctx, spec)
	// A detached run has no exit code to wait for — the record says it was
	// launched, and `sandbox-cli ps` is where its fate lives.
	meta := auditMeta(s.Cfg, spec, opts, 0, time.Since(started))
	meta.Detached = true
	s.Audit.RecordSession(meta)
	return name, startErr
}

// canObserveDenials reports whether this run's egress refusals can actually be
// counted. Both conditions have to hold, and when either does not the audit line
// carries no denial field at all.
//
// That absence is load-bearing, which is why the audit field is a pointer: a run
// nobody looked at and a run that was looked at and refused nothing are different
// facts, and the second is recorded as an explicit 0.
//
//   - There must be an allowlist to be refused by. In default mode no proxy runs
//     and no denial can occur, so a counter would answer a question nobody put —
//     the same reason EgressEnforcementRequested is empty rather than "address"
//     for a run with no allowlist.
//
//   - The run must have no pty. With `-t` docker returns one merged stream on its
//     own stdout, and the only way to read it is to interpose on stdout — which
//     costs the container its terminal size (measured; see runtime.newDenyTap).
//     An interactive agent is left unobserved rather than observed at the price of
//     the thing the user is looking at.
//
// **There is a third condition this function cannot express: it is only called
// from Run.** Start — which is every `--detach` session and every fleet task —
// never wires a collector, because nothing in this process is holding that
// container's output. That is not the same as the output being lost: `docker
// logs` has it, and reading it back at `fleet status` or reap time is the
// obvious way to cover the unattended case, which is where an after-the-fact
// record is worth most. Recorded in docs/roadmap/task-4-run-provenance.md rather
// than left implied by this function's absence from Start.
//
// Between that and the pty rule, what is actually covered today is a
// non-interactive `run` under an allowlist — CI, a redirected shell, `--no-tty`.
// Say that plainly anywhere this field is described; it is much narrower than
// "runs with an allowlist". The denials still reach the screen in the
// interactive case, where the user already is; what is missing is the record.
func canObserveDenials(spec runtime.RunSpec) bool {
	return spec.Env["SANDBOX_EGRESS_ALLOW"] != "" && !spec.TTY
}

// gitIdentityValues, when --git is set, reads the host git user.name/email and
// returns them as the GIT_AUTHOR_*/GIT_COMMITTER_* vars the runtime forwards by
// name, so commits inside the sandbox are attributed to the host identity.
// Best-effort: an unset identity or missing git simply yields nothing (the
// workspace-trust env from BuildSpec still applies).
//
// It returns them rather than setting them, for the same reason secrets do: this
// process spawns docker and git afterwards, and nothing it forwards to a
// container belongs in its own environment.
func gitIdentityValues(opts Options) map[string]string {
	if !opts.GitIdentity {
		return nil
	}
	// Read the identity git would use in the project itself (its local config
	// wins over global), not sandbox-cli's ambient cwd.
	dir := opts.Project
	if dir == "" {
		dir, _ = os.Getwd()
	}
	out := map[string]string{}
	if name := gitConfigGet(dir, "user.name"); name != "" {
		out["GIT_AUTHOR_NAME"] = name
		out["GIT_COMMITTER_NAME"] = name
	}
	if email := gitConfigGet(dir, "user.email"); email != "" {
		out["GIT_AUTHOR_EMAIL"] = email
		out["GIT_COMMITTER_EMAIL"] = email
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

func gitConfigGet(dir, key string) string {
	cmd := exec.Command("git", "config", "--get", key)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// injectSecrets resolves the configured/flagged secrets and sets them in the
// current process environment, ready for the runtime to forward by name.
// forwardedValues resolves everything whose *value* must reach the container
// without passing through sandbox-cli's own environment or the docker argv:
// brokered secrets, and the host git identity. The names are already on the
// spec (BuildSpec); this supplies what they are worth.
//
// Called only from Run and Start, never from Prepare, so --dry-run neither reads
// a secret file nor executes a secret command.
func forwardedValues(cfg config.Config, opts Options) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range gitIdentityValues(opts) {
		out[k] = v
	}

	sources, err := secretSources(cfg, opts)
	if err != nil {
		return nil, err
	}
	if len(sources) > 0 {
		vars, err := creds.Resolve(sources)
		if err != nil {
			return nil, err
		}
		for _, v := range vars {
			out[v.Name] = v.Value
		}
		warnLongLivedSecrets(vars, time.Now())
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// warnLongLivedSecrets reports brokered credentials whose own shape says they
// outlive the run. It is here rather than in `creds` because this is the last
// point at which a secret's name and its value are both in hand, and the value
// must go no further than the container — and it warns rather than refuses for
// the reason `creds.Classify` gives: for some credentials the long-lived form is
// the only form there is.
//
// The message carries the name and the format, never any part of the value, for
// the same reason `audit.SessionMeta` has nowhere to put one: a warning is
// written to a stream somebody may well be logging.
//
// warnedSecret is a var so the tests can read what was printed. Everything else
// about this is deliberately dumb: it prints, and the run proceeds.
var warnedSecret = func(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format, args...)
}

func warnLongLivedSecrets(vars []creds.EnvVar, now time.Time) {
	for _, v := range vars {
		a := creds.Classify(v.Value, now)
		if a.Lifetime != creds.LongLived {
			continue
		}
		warnedSecret("sandbox-cli: secret %s looks like %s, which outlives this run — "+
			"a leaked value stays usable until you revoke it. Brokering a short-lived one "+
			"bounds what a leak is worth; see docs/security/secrets.md.\n", v.Name, a.Detail)
	}
}

// auditMeta assembles the record for one run. Everything here is already
// resolved on the spec, which is the point: the log describes what actually ran,
// not what the flags asked for.
func auditMeta(cfg config.Config, spec runtime.RunSpec, opts Options, exitCode int, took time.Duration) audit.SessionMeta {
	m := audit.SessionMeta{
		Image:    spec.Image,
		Workdir:  spec.Workdir,
		Command:  spec.Command,
		Agent:    opts.Agent,
		Branch:   spec.Branch,
		Network:  "default",
		EnvNames: spec.EnvNames, // names only — never the values
		ExitCode: exitCode,
		Duration: took,
	}
	// Set only when the run was actually observed, so a nil here and a zero mean
	// different things in the record — see audit.SessionMeta. A run that was not
	// looked at must not report "nothing was refused".
	if spec.Denials != nil {
		n := spec.Denials.Count()
		m.EgressDeniedReported = &n
		m.EgressDeniedHostsReported = spec.Denials.Hosts()
	}
	for _, mnt := range spec.Mounts {
		if mnt.Target == spec.Workdir {
			m.Workspace = mnt.Source
			break
		}
	}
	if spec.Network == "none" {
		m.Network = "none"
	}
	if allow := spec.Env["SANDBOX_EGRESS_ALLOW"]; allow != "" {
		m.Network = "allowlist"
		m.EgressAllow = strings.Split(allow, ",")
		// Which regime was asked for. The address-matching firewall permits every
		// host sharing an allowlisted address; the proxy does not. A log that
		// recorded only "allowlist" could not tell those apart afterwards.
		//
		// This is what the run requested, not what the container did — the
		// entrypoint falls back to address matching if the proxy binary or the
		// sandbox-proxy user is missing, which a user-supplied image can cause.
		// The field is named accordingly rather than asserting an outcome the host
		// cannot see.
		m.EgressEnforcementRequested = "address"
		if spec.Env["SANDBOX_PROXY_PORT"] != "" {
			m.EgressEnforcementRequested = "name"
		}
	}
	m.Engine = cfg.Engine
	if m.Engine == "" {
		m.Engine = "docker"
	}
	m.NetworkName = spec.Network

	return m
}

// enforceSeccomp turns the seccomp warning into a refusal when the configuration
// asked for one.
//
// The difference between profiles in one function: dev prints the warning and
// carries on, because a developer is watching and can act on it. prod refuses,
// because nobody is, and an unattended run that quietly proceeded with the full
// syscall table available is exactly the "degraded silently" failure the profile
// exists to prevent.
//
// A daemon that cannot be queried refuses too, under the same reasoning: prod
// does not get to assume the answer it would prefer.
func (s *Session) enforceSeccomp(ctx context.Context) error {
	if s.Cfg.Security.Seccomp != config.SeccompRequired {
		return nil
	}
	r, ok := s.Runtime.(interface {
		SeccompUnavailable(context.Context) (bool, bool)
	})
	if !ok {
		return nil // a runtime that cannot be asked; nothing to enforce against
	}
	unavailable, answered := r.SeccompUnavailable(ctx)
	if !answered {
		return fmt.Errorf("security.seccomp is %q but this docker daemon could not be asked whether it "+
			"applies a syscall filter; refusing rather than assuming it does", config.SeccompRequired)
	}
	if unavailable {
		return fmt.Errorf("security.seccomp is %q but this docker daemon applies no syscall filter, so the "+
			"container would have the full syscall table\n"+
			"  Docker Desktop: Settings > Docker Engine, remove \"seccomp-profile\": \"unconfined\"\n"+
			"  or run with --profile dev, which warns instead of refusing", config.SeccompRequired)
	}
	return nil
}

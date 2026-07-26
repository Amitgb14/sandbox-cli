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
		Runtime: runtime.NewDockerCLI(),
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
	if err := s.Runtime.EnsureImage(ctx, spec.Image, forceBuild); err != nil {
		return 1, fmt.Errorf("preparing image %q: %w", spec.Image, err)
	}
	if err := s.Runtime.EnsureNetwork(ctx, spec.Network); err != nil {
		return 1, err
	}
	// Said once, on the way in, while the user can still do something about it.
	if w, ok := s.Runtime.(interface{ WarnIfSeccompDisabled(context.Context) }); ok {
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

	// Recorded after the run, not before it: an audit line whose whole purpose is
	// "what did this do and how did it end" is not worth much written at the
	// moment nothing has happened yet.
	started := time.Now()
	code, runErr := s.Runtime.Run(ctx, spec)
	s.Audit.RecordSession(auditMeta(spec, opts, code, time.Since(started)))
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
	meta := auditMeta(spec, opts, 0, time.Since(started))
	meta.Detached = true
	s.Audit.RecordSession(meta)
	return name, startErr
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
	}
	if len(out) == 0 {
		return nil, nil
	}
	return out, nil
}

// auditMeta assembles the record for one run. Everything here is already
// resolved on the spec, which is the point: the log describes what actually ran,
// not what the flags asked for.
func auditMeta(spec runtime.RunSpec, opts Options, exitCode int, took time.Duration) audit.SessionMeta {
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
	}
	return m
}

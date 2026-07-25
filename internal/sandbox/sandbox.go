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
		Audit:   audit.NopSink{},
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

	s.Audit.RecordSession(audit.SessionMeta{
		Image:   spec.Image,
		Workdir: spec.Workdir,
		Command: spec.Command,
	})

	// Resolve brokered secrets and place them in this process's environment so
	// the runtime forwards them to the container by name (the values are never
	// on the docker argv). Done only here, on the real run path — never in
	// Prepare/--dry-run — so a secret command is not executed just to print the
	// command. BuildSpec already added the names to the spec's forwarded env.
	if err := injectSecrets(s.Cfg, opts); err != nil {
		return 1, err
	}
	injectGitIdentity(opts)

	return s.Runtime.Run(ctx, spec)
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

	s.Audit.RecordSession(audit.SessionMeta{
		Image:   spec.Image,
		Workdir: spec.Workdir,
		Command: spec.Command,
	})

	// Same rule as Run: secrets are resolved only on a real launch path, never in
	// Prepare/--dry-run, and reach the container by name rather than on the argv.
	if err := injectSecrets(s.Cfg, opts); err != nil {
		return "", err
	}
	injectGitIdentity(opts)

	return s.Runtime.Start(ctx, spec)
}

// injectGitIdentity, when --git is set, reads the host git user.name/email and
// places them in this process's environment as the GIT_AUTHOR_*/GIT_COMMITTER_*
// vars the runtime forwards by name, so commits inside the sandbox are attributed
// to the host identity. Best-effort: an unset identity or missing git is simply
// skipped (the workspace-trust env from BuildSpec still applies).
func injectGitIdentity(opts Options) {
	if !opts.GitIdentity {
		return
	}
	// Read the identity git would use in the project itself (its local config
	// wins over global), not sandbox-cli's ambient cwd.
	dir := opts.Project
	if dir == "" {
		dir, _ = os.Getwd()
	}
	name := gitConfigGet(dir, "user.name")
	email := gitConfigGet(dir, "user.email")
	if name != "" {
		os.Setenv("GIT_AUTHOR_NAME", name)
		os.Setenv("GIT_COMMITTER_NAME", name)
	}
	if email != "" {
		os.Setenv("GIT_AUTHOR_EMAIL", email)
		os.Setenv("GIT_COMMITTER_EMAIL", email)
	}
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
func injectSecrets(cfg config.Config, opts Options) error {
	sources, err := secretSources(cfg, opts)
	if err != nil {
		return err
	}
	if len(sources) == 0 {
		return nil
	}
	vars, err := creds.Resolve(sources)
	if err != nil {
		return err
	}
	for _, v := range vars {
		if err := os.Setenv(v.Name, v.Value); err != nil {
			return fmt.Errorf("setting secret %q: %w", v.Name, err)
		}
	}
	return nil
}

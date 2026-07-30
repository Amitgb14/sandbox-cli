package studioapi

import (
	"context"
	"fmt"
	"strings"

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
func toRun(c runtime.ContainerInfo) Run {
	r := Run{
		ID:          shortID(c.ID),
		ContainerID: c.ID,
		Name:        c.Name,
		Kind:        RunKindInteractive,
		State:       toRunState(c.State),
		Detached:    !c.OpenStdin && !c.TTY,
		Repo:        c.Labels[sandbox.LabelRepo],
		Branch:      c.Labels[sandbox.LabelBranch],
		Base:        c.Labels[sandbox.LabelBase],
		Agent:       c.Labels[sandbox.LabelAgent],
		Verify:      c.Labels[sandbox.LabelVerify],
		CreatedAt:   c.CreatedAt,
		OpenStdin:   c.OpenStdin,
		TTY:         c.TTY,
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

package sandbox

import (
	"context"
	"errors"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/audit"
	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
)

// A detached launch the engine refused is not a run, and the audit log must not
// say otherwise.
//
// The record carries exit code 0, because a detached run has none to wait for —
// so a line written for a container that never started reads as a run that
// completed successfully. `--runtime kata-runtime` on a host where kata is not
// registered is exactly that case, and it is the one where the lie is worst:
// the line would name a boundary nothing ever ran inside, and someone counting
// "which runs were confined by a microVM" would count it.

type recordingSink struct{ metas []audit.SessionMeta }

func (s *recordingSink) RecordSession(m audit.SessionMeta) { s.metas = append(s.metas, m) }

// startStub is a Runtime whose only interesting behaviour is whether Start
// fails. Everything else succeeds so the test reaches the audit decision.
type startStub struct {
	err     error
	started bool
}

func (s *startStub) Available(context.Context) error                   { return nil }
func (s *startStub) EnsureImage(context.Context, string, bool) error   { return nil }
func (s *startStub) EnsureNetwork(context.Context, string) error       { return nil }
func (s *startStub) Run(context.Context, runtime.RunSpec) (int, error) { return 0, nil }
func (s *startStub) Start(_ context.Context, _ runtime.RunSpec) (string, error) {
	if s.err != nil {
		return "", s.err
	}
	s.started = true
	return "sandbox-app-feature-a", nil
}

func newStartSession(t *testing.T, rt runtime.Runtime, sink audit.Sink) *Session {
	t.Helper()
	cfg := config.Default()
	cfg.Runtime = "kata-runtime"
	return &Session{Cfg: cfg, Runtime: rt, Audit: sink}
}

func TestStartRecordsNothingWhenTheEngineRefusedTheLaunch(t *testing.T) {
	sink := &recordingSink{}
	rt := &startStub{err: errors.New(`unknown or invalid runtime name: kata-runtime`)}
	s := newStartSession(t, rt, sink)

	if _, err := s.Start(context.Background(), Options{Project: t.TempDir()}, false); err == nil {
		t.Fatal("Start returned nil for a launch the engine refused")
	}
	if len(sink.metas) != 0 {
		t.Errorf("a refused launch was written to the audit log as a run: %+v", sink.metas[0])
	}
}

func TestStartRecordsTheRuntimeWhenTheLaunchHappened(t *testing.T) {
	sink := &recordingSink{}
	rt := &startStub{}
	s := newStartSession(t, rt, sink)

	if _, err := s.Start(context.Background(), Options{Project: t.TempDir()}, false); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if len(sink.metas) != 1 {
		t.Fatalf("a launched run produced %d audit records, want 1", len(sink.metas))
	}
	m := sink.metas[0]
	if m.Runtime != "kata-runtime" {
		t.Errorf("runtime = %q, want the one the run asked the engine for", m.Runtime)
	}
	if !m.Detached {
		t.Errorf("a Start() run is not recorded as detached: %+v", m)
	}
}

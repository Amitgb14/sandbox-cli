package server

import (
	"context"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/audit"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// idleServer is a sandboxd on the fake backend, with caps, and a clock the
// test moves.
func idleServer(t *testing.T, caps ...string) (*Server, *api.Client, *testClock) {
	t.Helper()
	be := fake.New(caps...)
	clk := &testClock{t: time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)}
	s := &Server{Backend: be, Policy: spec.DefaultPolicyFor(be.Capabilities()), now: clk.now,
		Audit: audit.NewLog(filepath.Join(t.TempDir(), "events.jsonl"))}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	c, _ := api.NewClient(ts.URL, "")
	return s, c, clk
}

func stateOf(t *testing.T, c *api.Client, id string) api.Sandbox {
	t.Helper()
	sb, err := c.Sandbox(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	return sb
}

// eventsOf is the sandbox's events of type typ.
func eventsOf(t *testing.T, c *api.Client, id, typ string) []api.Event {
	t.Helper()
	evs, err := c.Events(context.Background(), id)
	if err != nil {
		t.Fatal(err)
	}
	var out []api.Event
	for _, e := range evs.Events {
		if e.Type == typ {
			out = append(out, e)
		}
	}
	return out
}

// A sandbox that asked to be suspended when idle is suspended at its idle
// timeout, not terminated; reading its record leaves it asleep, and the
// next command resumes it, after which its idle clock starts again.
func TestAnIdleSandboxIsSuspendedAndWokenByUse(t *testing.T) {
	s, c, clk := idleServer(t, api.CapSuspend)
	ctx := context.Background()
	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{IdleTimeoutSecs: 60, IdleAction: api.IdleSuspend})
	if err != nil {
		t.Fatal(err)
	}
	if sb.IdleAction != api.IdleSuspend {
		t.Fatalf("idle_action %q, want suspend", sb.IdleAction)
	}

	s.reapOnce(clk.advance(59 * time.Second))
	if got := stateOf(t, c, sb.ID); got.State != api.StateRunning {
		t.Fatalf("before its idle timeout: %s", got.State)
	}
	s.reapOnce(clk.advance(time.Second))
	got := stateOf(t, c, sb.ID)
	if got.State != api.StateSuspended || !got.IdleSuspended {
		t.Fatalf("at its idle timeout: %s, idle_suspended %t; want suspended for being idle", got.State, got.IdleSuspended)
	}
	if evs := eventsOf(t, c, sb.ID, api.EventSandboxSuspended); len(evs) != 1 || evs[0].Reason != "idle" {
		t.Fatalf("suspended events: %+v, want one for idle", evs)
	}
	if _, err := c.Processes(ctx, sb.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Events(ctx, sb.ID); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(t, c, sb.ID); got.State != api.StateSuspended {
		t.Fatalf("reading its records woke it: %s", got.State)
	}

	res, err := c.Run(ctx, sb.ID, api.RunRequest{Argv: []string{"echo", "hi"}})
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("a command on the sleeping sandbox: %+v, %v", res, err)
	}
	got = stateOf(t, c, sb.ID)
	if got.State != api.StateRunning || got.IdleSuspended {
		t.Fatalf("after the command: %s, idle_suspended %t", got.State, got.IdleSuspended)
	}
	if evs := eventsOf(t, c, sb.ID, api.EventSandboxResumed); len(evs) != 1 || evs[0].Reason != "request" {
		t.Fatalf("resumed events: %+v, want one for a request", evs)
	}
	s.reapOnce(clk.advance(30 * time.Second))
	if got := stateOf(t, c, sb.ID); got.State != api.StateRunning {
		t.Fatalf("30s after it was woken: %s; its idle clock should have started again", got.State)
	}
}

// Without idle_action, the idle timeout terminates, as it always did.
func TestTheIdleTimeoutStillTerminatesByDefault(t *testing.T) {
	s, c, clk := idleServer(t, api.CapSuspend)
	sb, err := c.CreateSandbox(context.Background(), api.CreateSandboxRequest{IdleTimeoutSecs: 60})
	if err != nil || sb.IdleAction != "" {
		t.Fatalf("create: idle_action %q, %v", sb.IdleAction, err)
	}
	s.reapOnce(clk.advance(time.Minute))
	if got := stateOf(t, c, sb.ID); got.State != api.StateTerminated {
		t.Fatalf("at its idle timeout: %s, want terminated", got.State)
	}
}

// Reading a sandbox's records, and a request marked background, are not
// activity: before, any request naming a sandbox was, and a gateway reading
// an agent's login out of it every two minutes kept it from ever idling out.
// A request that uses the sandbox still is.
func TestBookkeepingIsNotActivity(t *testing.T) {
	s, c, clk := idleServer(t)
	ctx := context.Background()
	quiet, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{IdleTimeoutSecs: 60})
	if err != nil {
		t.Fatal(err)
	}
	used, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{IdleTimeoutSecs: 60})
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{quiet.ID, used.ID} {
		if err := c.WriteFile(ctx, id, "/sandbox/home/login", []byte("x")); err != nil {
			t.Fatal(err)
		}
	}

	clk.advance(50 * time.Second)
	if _, err := c.Sandbox(ctx, quiet.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Processes(ctx, quiet.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Events(ctx, quiet.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReadFile(api.Background(ctx), quiet.ID, "/sandbox/home/login"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReadFile(ctx, used.ID, "/sandbox/home/login"); err != nil {
		t.Fatal(err)
	}

	s.reapOnce(clk.advance(10 * time.Second))
	if got := stateOf(t, c, quiet.ID); got.State != api.StateTerminated {
		t.Errorf("only read about: %s, want terminated at its idle timeout", got.State)
	}
	if got := stateOf(t, c, used.ID); got.State != api.StateRunning {
		t.Errorf("a file read from it 10s ago: %s, want running", got.State)
	}
}

// A background request does not wake a sandbox suspended for being idle,
// and one suspended on request stays suspended whatever asks: resuming it is
// its owner's to do.
func TestWhatDoesNotWakeASandbox(t *testing.T) {
	s, c, clk := idleServer(t, api.CapSuspend)
	ctx := context.Background()
	idle, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{IdleTimeoutSecs: 60, IdleAction: api.IdleSuspend})
	if err != nil {
		t.Fatal(err)
	}
	asked, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{IdleAction: api.IdleSuspend})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Suspend(ctx, asked.ID); err != nil {
		t.Fatal(err)
	}
	s.reapOnce(clk.advance(time.Minute))
	if got := stateOf(t, c, idle.ID); !got.IdleSuspended {
		t.Fatalf("not suspended for being idle: %+v", got)
	}
	if got := stateOf(t, c, asked.ID); got.State != api.StateSuspended || got.IdleSuspended {
		t.Fatalf("suspended on request: %s, idle_suspended %t", got.State, got.IdleSuspended)
	}

	if _, err := c.ReadFile(api.Background(ctx), idle.ID, "/etc/hostname"); !api.IsCode(err, api.CodeConflict) {
		t.Errorf("a background read of a sleeping sandbox: %v, want conflict", err)
	}
	if got := stateOf(t, c, idle.ID); got.State != api.StateSuspended {
		t.Errorf("a background read woke it: %s", got.State)
	}
	if _, err := c.Run(ctx, asked.ID, api.RunRequest{Argv: []string{"true"}}); !api.IsCode(err, api.CodeConflict) {
		t.Errorf("a command on a sandbox suspended on request: %v, want conflict", err)
	}
	if got := stateOf(t, c, asked.ID); got.State != api.StateSuspended {
		t.Errorf("a command woke a sandbox suspended on request: %s", got.State)
	}
	// Resumed by hand, it is an ordinary running sandbox again.
	if _, err := c.Resume(ctx, asked.ID); err != nil {
		t.Fatal(err)
	}
	if got := stateOf(t, c, asked.ID); got.State != api.StateRunning || got.IdleSuspended {
		t.Errorf("resumed by hand: %s, idle_suspended %t", got.State, got.IdleSuspended)
	}
}

// Requests that arrive together at a sleeping sandbox share one resume.
func TestConcurrentRequestsWakeASandboxOnce(t *testing.T) {
	s, c, clk := idleServer(t, api.CapSuspend)
	ctx := context.Background()
	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{IdleTimeoutSecs: 60, IdleAction: api.IdleSuspend})
	if err != nil {
		t.Fatal(err)
	}
	s.reapOnce(clk.advance(time.Minute))
	if got := stateOf(t, c, sb.ID); !got.IdleSuspended {
		t.Fatalf("not asleep: %+v", got)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 8)
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := c.Run(ctx, sb.ID, api.RunRequest{Argv: []string{"true"}})
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		if err != nil {
			t.Errorf("a request at the sleeping sandbox: %v", err)
		}
	}
	if evs := eventsOf(t, c, sb.ID, api.EventSandboxResumed); len(evs) != 1 {
		t.Errorf("%d resumes for 8 requests, want 1", len(evs))
	}
}

// Suspend on idle is refused where the endpoint cannot suspend, at create
// and in an update, rather than becoming a terminate: what was asked to be
// kept would be thrown away. An action that is neither is refused too.
func TestIdleSuspendIsRefusedWhereItCannotBeHad(t *testing.T) {
	_, c, _ := idleServer(t)
	ctx := context.Background()
	if _, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{IdleAction: api.IdleSuspend}); !api.IsCode(err, api.CodeUnsupported) {
		t.Errorf("create with suspend, on an endpoint that cannot: %v, want unsupported", err)
	}
	if _, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{IdleAction: "sleep"}); !api.IsCode(err, api.CodeInvalidRequest) {
		t.Errorf("idle_action sleep: %v, want invalid_request", err)
	}
	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{IdleAction: api.IdleTerminate})
	if err != nil || sb.IdleAction != "" {
		t.Fatalf("idle_action terminate: %q, %v", sb.IdleAction, err)
	}
	suspend := api.IdleSuspend
	if _, err := c.UpdateSandbox(ctx, sb.ID, api.UpdateSandboxRequest{IdleAction: &suspend}); !api.IsCode(err, api.CodeUnsupported) {
		t.Errorf("update to suspend, on an endpoint that cannot: %v, want unsupported", err)
	}
}

// An update switches the idle action either way, and says so in its event.
func TestTheIdleActionCanBeChanged(t *testing.T) {
	s, c, clk := idleServer(t, api.CapSuspend)
	ctx := context.Background()
	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{IdleTimeoutSecs: 60})
	if err != nil {
		t.Fatal(err)
	}
	suspend := api.IdleSuspend
	up, err := c.UpdateSandbox(ctx, sb.ID, api.UpdateSandboxRequest{IdleAction: &suspend})
	if err != nil || up.IdleAction != api.IdleSuspend {
		t.Fatalf("update to suspend: %q, %v", up.IdleAction, err)
	}
	if evs := eventsOf(t, c, sb.ID, api.EventSandboxUpdated); len(evs) != 1 || evs[0].IdleAction != api.IdleSuspend {
		t.Fatalf("updated events: %+v", evs)
	}
	s.reapOnce(clk.advance(time.Minute))
	if got := stateOf(t, c, sb.ID); !got.IdleSuspended {
		t.Fatalf("switched to suspend, then idle: %+v", got)
	}
	terminate := api.IdleTerminate
	if up, err := c.UpdateSandbox(ctx, sb.ID, api.UpdateSandboxRequest{IdleAction: &terminate}); err != nil || up.IdleAction != "" {
		t.Fatalf("update back to terminate: %q, %v", up.IdleAction, err)
	}
}

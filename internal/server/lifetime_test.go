package server

import (
	"context"
	"encoding/json"
	"net/http"
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

type testClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *testClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *testClock) advance(d time.Duration) time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.t = c.t.Add(d)
	return c.t
}

// lifetimeServer is a sandboxd on the fake backend whose policy gives every
// sandbox at most maxLife, with a clock the test moves.
func lifetimeServer(t *testing.T, maxLife int) (*Server, *api.Client, *testClock) {
	t.Helper()
	be := fake.New(api.CapEgressAllowlist, api.CapSuspend)
	pol := spec.DefaultPolicyFor(be.Capabilities())
	pol.Limits.MaxLifetimeSecs = maxLife
	clk := &testClock{t: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)}
	s := &Server{Backend: be, Policy: pol, now: clk.now, Audit: audit.NewLog(filepath.Join(t.TempDir(), "events.jsonl"))}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	c, _ := api.NewClient(ts.URL, "")
	return s, c, clk
}

// The operator's lifetime is every sandbox's unless it asks for less; a
// request for more is refused, not clamped.
func TestLifetimeComesFromThePolicy(t *testing.T) {
	_, c, clk := lifetimeServer(t, 1800)
	ctx := context.Background()

	caps, err := c.Capabilities(ctx)
	if err != nil || caps.Limits.MaxLifetimeSecs != 1800 {
		t.Fatalf("capabilities: max_lifetime_secs %d, %v", caps.Limits.MaxLifetimeSecs, err)
	}
	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if want := clk.now().Add(30 * time.Minute); sb.LifetimeSecs != 1800 || sb.ExpiresAt == nil || !sb.ExpiresAt.Equal(want) {
		t.Errorf("default: lifetime %d, expires %v; want 1800 and %v", sb.LifetimeSecs, sb.ExpiresAt, want)
	}
	short, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{LifetimeSecs: 60})
	if err != nil || short.LifetimeSecs != 60 {
		t.Errorf("a shorter lifetime: %d, %v", short.LifetimeSecs, err)
	}
	for _, life := range []int{1801, -1} {
		if _, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{LifetimeSecs: life}); !api.IsCode(err, api.CodeInvalidRequest) {
			t.Errorf("lifetime_secs %d: %v, want invalid_request", life, err)
		}
	}
}

// With no limit, a sandbox has no lifetime unless it asks for one.
func TestNoLifetimeWithoutALimit(t *testing.T) {
	_, c, _ := lifetimeServer(t, 0)
	ctx := context.Background()
	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil || sb.LifetimeSecs != 0 || sb.ExpiresAt != nil {
		t.Fatalf("no limit: lifetime %d, expires %v, %v", sb.LifetimeSecs, sb.ExpiresAt, err)
	}
	sb, err = c.CreateSandbox(ctx, api.CreateSandboxRequest{LifetimeSecs: 7200})
	if err != nil || sb.LifetimeSecs != 7200 || sb.ExpiresAt == nil {
		t.Fatalf("asked for: lifetime %d, expires %v, %v", sb.LifetimeSecs, sb.ExpiresAt, err)
	}
}

// A lifetime ends a sandbox whatever it is doing: a running process keeps
// it from idling out, not from expiring, and neither does being suspended.
func TestLifetimeEndsABusyOrSuspendedSandbox(t *testing.T) {
	s, c, clk := lifetimeServer(t, 1800)
	ctx := context.Background()

	busy, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{IdleTimeoutSecs: 60})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.StartProcess(ctx, busy.ID, api.RunRequest{Argv: []string{"sleep", "100000"}}); err != nil {
		t.Fatal(err)
	}
	asleep, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.Suspend(ctx, asleep.ID); err != nil {
		t.Fatal(err)
	}

	// Past the idle timeout but busy, and short of the lifetime: kept.
	s.reapOnce(clk.advance(29 * time.Minute))
	if got, _ := c.Sandbox(ctx, busy.ID); got.State != api.StateRunning {
		t.Fatalf("at 29 minutes the busy sandbox is %s", got.State)
	}
	// Made now, with 30 minutes of its own: the clock is each sandbox's.
	later, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}

	s.reapOnce(clk.advance(time.Minute))
	for _, id := range []string{busy.ID, asleep.ID} {
		if got, _ := c.Sandbox(ctx, id); got.State != api.StateTerminated {
			t.Errorf("%s at its lifetime: %s, want terminated", id, got.State)
		}
		evs, err := c.Events(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		// Its processes' exits may be recorded after it; the reason is on
		// the termination.
		why := ""
		for _, ev := range evs.Events {
			if ev.Type == api.EventSandboxTerminated {
				why = ev.Reason
			}
		}
		if why != "lifetime" {
			t.Errorf("%s: terminated for %q, want lifetime", id, why)
		}
	}
	if got, _ := c.Sandbox(ctx, later.ID); got.State != api.StateRunning {
		t.Errorf("a sandbox with 29 minutes left: %s", got.State)
	}
}

// A sandbox kept across a restart keeps its expiry, and the time sandboxd was
// down counts: a lifetime is wall-clock, so restarting cannot extend it.
func TestLifetimeSurvivesARestart(t *testing.T) {
	b := fake.New(api.CapEgressAllowlist)
	dir := newKeepDir(t)
	pol := spec.DefaultPolicyFor(b.Capabilities())
	pol.Limits.MaxLifetimeSecs = 1800
	clk := &testClock{t: time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)}
	serve := func() (*Server, http.Handler) {
		s := &Server{Backend: b, Policy: pol, RecordDir: dir, now: clk.now}
		if _, err := s.Restore(context.Background()); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			for id := range b.Kept() {
				_ = b.Terminate(context.Background(), id)
			}
			s.watchers.Wait()
		})
		return s, s.Handler()
	}
	_, h1 := serve()
	sb := mustCreate(t, h1, api.CreateSandboxRequest{Name: "demo"})

	clk.advance(20 * time.Minute) // sandboxd is down for twenty minutes
	s2, h2 := serve()
	var got api.Sandbox
	_ = json.Unmarshal(call(t, h2, "GET", "/v1/sandboxes/demo", nil).Body.Bytes(), &got)
	if got.ExpiresAt == nil || !got.ExpiresAt.Equal(*sb.ExpiresAt) {
		t.Fatalf("expiry after the restart: %v, want %v", got.ExpiresAt, sb.ExpiresAt)
	}
	s2.reapOnce(clk.advance(10 * time.Minute))
	_ = json.Unmarshal(call(t, h2, "GET", "/v1/sandboxes/"+sb.ID, nil).Body.Bytes(), &got)
	if got.State != api.StateTerminated {
		t.Fatalf("thirty minutes after creation, restart included: %s", got.State)
	}
}

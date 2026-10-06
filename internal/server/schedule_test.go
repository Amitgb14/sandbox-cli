package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// scheduleServer is a server on the fake backend with the given capabilities,
// a one-minute minimum interval, and a clock the test moves.
func scheduleServer(t *testing.T, caps ...string) (*api.Client, *Server, func(time.Duration)) {
	t.Helper()
	be := fake.New(caps...)
	pol := spec.DefaultPolicyFor(be.Capabilities())
	pol.Limits.MinSnapshotEverySecs, pol.Limits.MaxSnapshotKeep = 60, 3
	var clock atomic.Int64
	clock.Store(time.Date(2026, 10, 6, 9, 0, 0, 0, time.UTC).UnixNano())
	s := &Server{Backend: be, Policy: pol, now: func() time.Time { return time.Unix(0, clock.Load()) }}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	c, err := api.NewClient(ts.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	return c, s, func(d time.Duration) { clock.Add(int64(d)) }
}

// scheduled waits for the sandbox's scheduled snapshots to number want, and
// returns them with the rest.
func scheduled(t *testing.T, c *api.Client, sandbox string, want int) (sched, manual []api.Snapshot) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		list, err := c.Snapshots(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		sched, manual = nil, nil
		for _, s := range list {
			if s.Sandbox != sandbox {
				continue
			}
			if s.Scheduled {
				sched = append(sched, s)
			} else {
				manual = append(manual, s)
			}
		}
		if len(sched) == want || time.Now().After(deadline) {
			return sched, manual
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// A schedule takes a snapshot each interval while the sandbox runs, keeps the
// newest it was told to, and never counts or removes one taken by request.
func TestScheduledSnapshotsKeepTheNewest(t *testing.T) {
	c, s, advance := scheduleServer(t, api.CapEgressAllowlist, api.CapDiskSnapshot)
	ctx := context.Background()
	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{SnapshotEverySecs: 60, SnapshotKeep: 2})
	if err != nil {
		t.Fatal(err)
	}
	if sb.SnapshotEverySecs != 60 || sb.SnapshotKeep != 2 {
		t.Fatalf("the sandbox says its schedule is %d/%d", sb.SnapshotEverySecs, sb.SnapshotKeep)
	}
	advance(30 * time.Second)
	s.scheduleTick(s.now())
	if got, _ := scheduled(t, c, sb.ID, 0); len(got) != 0 {
		t.Fatalf("a snapshot before the interval: %v", got)
	}
	mine, err := c.CreateSnapshot(ctx, sb.ID)
	if err != nil {
		t.Fatal(err)
	}
	for want := 1; want <= 3; want++ {
		advance(60 * time.Second)
		s.scheduleTick(s.now())
		got, _ := scheduled(t, c, sb.ID, min(want, 2))
		if len(got) != min(want, 2) {
			t.Fatalf("after %d intervals: %d scheduled snapshots; want %d", want, len(got), min(want, 2))
		}
	}
	got, manual := scheduled(t, c, sb.ID, 2)
	if len(manual) != 1 || manual[0].ID != mine.ID {
		t.Fatalf("the snapshot taken by request was touched: %v", manual)
	}
	if got[0].Kind != api.SnapshotDisk {
		t.Errorf("kind %q", got[0].Kind)
	}

	// A smaller keep applies at once; every_secs 0 stops the schedule.
	if _, err := c.SetSnapshotSchedule(ctx, sb.ID, api.SnapshotSchedule{EverySecs: 60, Keep: 1}); err != nil {
		t.Fatal(err)
	}
	if got, _ := scheduled(t, c, sb.ID, 1); len(got) != 1 {
		t.Fatalf("keep 1 left %d", len(got))
	}
	if _, err := c.SetSnapshotSchedule(ctx, sb.ID, api.SnapshotSchedule{}); err != nil {
		t.Fatal(err)
	}
	advance(10 * time.Minute)
	if n := s.scheduleTick(s.now()); n != 0 {
		t.Fatalf("a stopped schedule started %d snapshots", n)
	}
}

// A schedule that cannot be kept is refused, before anything is made: on a
// backend without snapshots, on a sandbox with volumes, or outside the
// server's limits.
func TestSnapshotScheduleRefusals(t *testing.T) {
	ctx := context.Background()
	none, _, _ := scheduleServer(t, api.CapEgressAllowlist)
	_, err := none.CreateSandbox(ctx, api.CreateSandboxRequest{SnapshotEverySecs: 60})
	if e, ok := err.(*api.Error); !ok || e.Code != api.CodeUnsupported {
		t.Fatalf("a schedule without snapshots: %v", err)
	}
	if list, _ := none.Sandboxes(ctx); len(list) != 0 {
		t.Fatalf("a refused create left %v", list)
	}

	c, _, _ := scheduleServer(t, api.CapEgressAllowlist, api.CapDiskSnapshot, api.CapVolumes)
	if _, err := c.CreateVolume(ctx, api.CreateVolumeRequest{Name: "data", SizeMB: 8}); err != nil {
		t.Fatal(err)
	}
	_, err = c.CreateSandbox(ctx, api.CreateSandboxRequest{SnapshotEverySecs: 60, Volumes: []api.VolumeMount{{Name: "data", Path: "/data"}}})
	if e, ok := err.(*api.Error); !ok || e.Status != http.StatusConflict {
		t.Fatalf("a schedule with volumes: %v", err)
	}
	for _, req := range []api.CreateSandboxRequest{{SnapshotEverySecs: 59}, {SnapshotEverySecs: 60, SnapshotKeep: 4}, {SnapshotKeep: 1}} {
		_, err := c.CreateSandbox(ctx, req)
		if e, ok := err.(*api.Error); !ok || e.Code != api.CodeInvalidRequest {
			t.Errorf("%+v: %v; want invalid_request", req, err)
		}
	}
	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.SetSnapshotSchedule(ctx, sb.ID, api.SnapshotSchedule{EverySecs: 30}); err == nil {
		t.Error("a schedule under the minimum was set on a running sandbox")
	}
	caps, err := c.Capabilities(ctx)
	if err != nil || caps.Limits.MinSnapshotEverySecs != 60 || caps.Limits.MaxSnapshotKeep != 3 {
		t.Fatalf("the limits a client sees: %+v %v", caps.Limits, err)
	}
}

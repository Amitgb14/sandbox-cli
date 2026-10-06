package server

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// slowSnapshots is the fake with a snapshot that reports part of its capture
// and then waits to be let go, as a real export of a few gigabytes does.
type slowSnapshots struct {
	*fake.Backend
	reported chan struct{}
	release  chan struct{}
}

func (b *slowSnapshots) Snapshot(ctx context.Context, id, snapshotID string) (backend.SnapshotInfo, error) {
	backend.ReportSnapshotProgress(ctx, backend.SnapshotProgress{Phase: api.SnapshotPhaseCapture, Bytes: 600 << 20, EstimatedBytes: 2 << 30})
	close(b.reported)
	<-b.release
	backend.ReportSnapshotProgress(ctx, backend.SnapshotProgress{Phase: api.SnapshotPhaseStore, Bytes: 2 << 30, EstimatedBytes: 2 << 30})
	return b.Backend.Snapshot(ctx, id, snapshotID)
}

func TestSnapshotProgressIsOnTheSandbox(t *testing.T) {
	be := &slowSnapshots{Backend: fake.New(api.CapDiskSnapshot), reported: make(chan struct{}), release: make(chan struct{})}
	s := &Server{Backend: be, Policy: spec.DefaultPolicyFor(be.Capabilities())}
	ts := httptest.NewServer(s.Handler())
	t.Cleanup(ts.Close)
	c, err := api.NewClient(ts.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	bg := context.Background()
	sb, err := c.CreateSandbox(bg, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if sb.Snapshotting != nil {
		t.Fatalf("a new sandbox is being snapshotted: %+v", sb.Snapshotting)
	}

	// The client that asks goes away part way, as a closed browser tab does.
	reqCtx, hangUp := context.WithCancel(bg)
	errc := make(chan error, 1)
	go func() { _, err := c.CreateSnapshot(reqCtx, sb.ID); errc <- err }()
	<-be.reported

	got, err := c.Sandbox(bg, sb.ID)
	if err != nil {
		t.Fatal(err)
	}
	p := got.Snapshotting
	if p == nil || p.Phase != api.SnapshotPhaseCapture || p.Bytes != 600<<20 || p.EstimatedBytes != 2<<30 || p.Scheduled || p.StartedAt.IsZero() {
		t.Fatalf("progress while capturing: %+v", p)
	}
	// One at a time: a second is refused, not run beside it.
	if _, err := c.CreateSnapshot(bg, sb.ID); !api.IsCode(err, api.CodeConflict) {
		t.Fatalf("a second snapshot while one is taken: %v", err)
	}

	hangUp()
	if err := <-errc; !errors.Is(err, context.Canceled) {
		t.Fatalf("the hung-up request: %v", err)
	}
	close(be.release)
	deadline := time.Now().Add(5 * time.Second)
	for {
		snaps, err := c.Snapshots(bg)
		if err != nil {
			t.Fatal(err)
		}
		got, err := c.Sandbox(bg, sb.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(snaps) == 1 && got.Snapshotting == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("not finished once its client hung up: %d snapshots, progress %+v", len(snaps), got.Snapshotting)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

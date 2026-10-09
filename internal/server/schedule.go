package server

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// Scheduled snapshots: a sandbox may ask to be captured every so often while
// it runs, keeping the newest few. They are taken here, by the server, so they
// happen whoever is watching — a client's timer stops when the client does —
// and are made, refused and logged exactly as a snapshot by request is
// (takeSnapshot). Retention removes only what a schedule took: a snapshot
// someone asked for is theirs to delete.

// scheduleRefusal says why a sandbox cannot have a schedule here, or "".
func (s *Server) scheduleRefusal(sched api.SnapshotSchedule, volumes []api.VolumeMount) (int, string, string) {
	if sched.EverySecs == 0 {
		return 0, "", ""
	}
	if _, ok := s.Backend.(backend.Snapshotter); !ok || !canSnapshot(s.Backend.Capabilities()) {
		return http.StatusNotImplemented, api.CodeUnsupported, "this endpoint cannot snapshot a sandbox, so it cannot schedule snapshots"
	}
	if len(volumes) > 0 {
		return http.StatusConflict, api.CodeConflict, errSnapshotVolumes.Error()
	}
	return 0, "", ""
}

// setSnapshotSchedule replaces a running sandbox's schedule: PUT
// /v1/sandboxes/{ref}/snapshot-schedule, {"every_secs": N, "keep": K}; every_secs
// 0 stops it. The first snapshot of a new schedule is one interval away, and
// a smaller keep applies at once.
func (s *Server) setSnapshotSchedule(w http.ResponseWriter, r *http.Request) {
	var req api.SnapshotSchedule
	if !decode(w, r, &req) {
		return
	}
	sched, err := spec.ResolveSchedule(req, s.Policy.Limits)
	if err != nil {
		writeSpecErr(w, err)
		return
	}
	rec, ok := s.live(w, r)
	if !ok {
		return
	}
	if status, code, msg := s.scheduleRefusal(sched, rec.snapshot().Volumes); status != 0 {
		writeErr(w, status, code, msg)
		return
	}
	rec.mu.Lock()
	rec.sbx.SnapshotEverySecs, rec.sbx.SnapshotKeep = sched.EverySecs, sched.Keep
	rec.lastScheduled = s.now()
	out := rec.sbx
	rec.mu.Unlock()
	s.persist(rec)
	s.pruneScheduled(r.Context(), out.ID, sched.Keep)
	writeJSON(w, http.StatusOK, out)
}

// runSchedules takes the snapshots that are due, once a second, until the
// process ends. One at a time per sandbox: a disk snapshot takes about a
// minute, and a second one started meanwhile would copy the same files again.
func (s *Server) runSchedules() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for range t.C {
		s.scheduleTick(s.now())
	}
}

// scheduleTick starts each snapshot that is due at now, and returns how many
// it started (for tests).
func (s *Server) scheduleTick(now time.Time) int {
	s.mu.Lock()
	var due []*record
	for _, rec := range s.sandboxes {
		rec.mu.Lock()
		every := time.Duration(rec.sbx.SnapshotEverySecs) * time.Second
		if every > 0 && rec.sbx.State == api.StateRunning && !rec.snapshotting && rec.sbx.Snapshotting == nil && now.Sub(rec.lastScheduled) >= every {
			rec.snapshotting = true
			due = append(due, rec)
		}
		rec.mu.Unlock()
	}
	s.mu.Unlock()
	for _, rec := range due {
		go s.scheduledSnapshot(rec)
	}
	return len(due)
}

func (s *Server) scheduledSnapshot(rec *record) {
	_, err := s.takeSnapshot(context.Background(), rec, true)
	rec.mu.Lock()
	// From the attempt, failed or not: a sandbox that cannot be captured is
	// not tried again every second.
	rec.lastScheduled = s.now()
	rec.snapshotting = false
	id, keep := rec.sbx.ID, rec.sbx.SnapshotKeep
	rec.mu.Unlock()
	if err != nil {
		if !errors.Is(err, backend.ErrNotFound) {
			s.logf("sandbox %s: scheduled snapshot: %v", id, err)
		}
		return
	}
	s.pruneScheduled(context.Background(), id, keep)
}

// pruneScheduled removes a sandbox's scheduled snapshots beyond the newest
// keep. Snapshots taken by request are never counted or removed.
func (s *Server) pruneScheduled(ctx context.Context, sandbox string, keep int) {
	st := s.snaps()
	st.mu.Lock()
	var mine []api.Snapshot
	for _, rec := range st.m {
		if rec.info.Scheduled && rec.info.Sandbox == sandbox {
			mine = append(mine, rec.info)
		}
	}
	sort.Slice(mine, func(i, j int) bool { return mine[i].CreatedAt.After(mine[j].CreatedAt) })
	var drop []string
	for i := keep; i < len(mine); i++ {
		drop = append(drop, mine[i].ID)
		delete(st.m, mine[i].ID)
	}
	st.mu.Unlock()
	sn, _ := s.Backend.(backend.Snapshotter)
	for _, id := range drop {
		s.forgetSnapshot(id)
		if sn != nil {
			_ = sn.DeleteSnapshot(ctx, id)
		}
		s.event(api.Event{Type: api.EventSnapshotDeleted, Sandbox: sandbox, Snapshot: id, Reason: "retention"})
	}
}

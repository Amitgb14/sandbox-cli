package studio

import (
	"context"
	"errors"
	"fmt"
	"math"
	"net/http"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// ResizeRequest is a running sandbox's new size.
type ResizeRequest struct {
	CPUs     float64 `json:"cpus"`
	MemoryMB int     `json:"memory_mb"`
	DiskMB   int     `json:"disk_mb,omitempty"`
	// DropEnv says the caller knows the environment does not carry over.
	DropEnv bool `json:"drop_env,omitempty"`
	// KeepSnapshot keeps the snapshot the copy was started from, as a way
	// back; by default it is deleted once the copy runs.
	KeepSnapshot bool `json:"keep_snapshot,omitempty"`
}

// ResizeResult is what a resize left: the copy at the new size, and the
// snapshot when it was kept.
type ResizeResult struct {
	Sandbox  api.Sandbox `json:"sandbox"`
	Replaced string      `json:"replaced"`
	Snapshot string      `json:"snapshot,omitempty"`
}

// resize gives a running sandbox a new size the only way a VM can get one:
// a copy. A VM's vCPUs and memory are fixed while it runs, so this takes a
// disk snapshot, starts a sandbox from it at the new size with the old one's
// labels, network, idle timeout and schedule, terminates the old one and
// gives the copy its name. Its files come across; its processes, memory and
// environment do not — an environment value is never readable back, so
// there is nothing to copy — and a sandbox with any is refused unless the
// caller says that is understood.
//
// Only where the backend takes disk snapshots: a copy of a memory snapshot
// is the original VM resumed, at the original's size, and sandboxd refuses
// it any other.
//
// It runs to the end on its own context, so closing the tab part way does
// not leave two sandboxes, or none; each step that fails says what exists.
func (s *Server) resize(w http.ResponseWriter, r *http.Request) {
	var req ResizeRequest
	if !decode(w, r, &req) {
		return
	}
	if !(req.CPUs > 0) || req.MemoryMB <= 0 || req.DiskMB < 0 {
		writeErr(w, http.StatusBadRequest, "cpus and memory_mb: more than 0; disk_mb: 0 for the old one's, or more")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 15*time.Minute)
	defer cancel()
	c := s.clientFor(r)
	old, err := c.Sandbox(ctx, r.PathValue("id"))
	if err != nil {
		writeAPIErr(w, err)
		return
	}
	caps, err := c.Capabilities(ctx)
	if err != nil {
		writeAPIErr(w, err)
		return
	}
	switch {
	case !caps.Has(api.CapDiskSnapshot):
		why := "takes no snapshots"
		if caps.Has(api.CapMemorySnapshot) {
			why = "takes memory snapshots, whose copies keep the original's size"
		}
		writeErr(w, http.StatusNotImplemented, "resizing needs disk snapshots, and "+caps.Backend+" "+why+"; start a new sandbox at the size you need")
		return
	case old.State != api.StateRunning:
		writeErr(w, http.StatusConflict, "only a running sandbox is resized")
		return
	case len(old.Volumes) > 0:
		writeErr(w, http.StatusConflict, "a sandbox with volumes cannot be snapshotted, so it cannot be resized; unmount them by starting it again without")
		return
	case len(old.EnvNames) > 0 && !req.DropEnv:
		writeErr(w, http.StatusConflict, "its environment ("+strings.Join(old.EnvNames, ", ")+") does not carry over: values are never readable back; resize with drop_env to go on without it")
		return
	}
	// The copy is the same sandbox at another size, so it keeps the time it
	// has left: a resize must not be a way to start a lifetime over.
	lifetime := 0
	if old.ExpiresAt != nil {
		left := int(math.Ceil(time.Until(*old.ExpiresAt).Seconds()))
		if left <= 0 {
			writeErr(w, http.StatusConflict, "it has reached the end of its lifetime")
			return
		}
		lifetime = left
	}

	snap, err := c.CreateSnapshot(ctx, old.ID)
	if err != nil {
		writeErr(w, http.StatusBadGateway, "snapshotting "+old.ID+": "+err.Error()+"; nothing was changed")
		return
	}
	dropSnap := func() {
		if !req.KeepSnapshot {
			_ = c.DeleteSnapshot(context.WithoutCancel(ctx), snap.ID)
		}
	}
	if snap.Kind != api.SnapshotDisk {
		_ = c.DeleteSnapshot(ctx, snap.ID)
		writeErr(w, http.StatusNotImplemented, "the snapshot is a "+snap.Kind+" one, whose copies keep the original's size; nothing was changed")
		return
	}
	// A gateway stamps its own labels on the copy, and refuses them from a
	// client.
	labels := map[string]string{}
	for k, v := range old.Labels {
		if !strings.HasPrefix(k, "gateway.") {
			labels[k] = v
		}
	}
	network := old.Network
	copyReq := api.CreateSandboxRequest{
		SnapshotID: snap.ID, CPUs: req.CPUs, MemoryMB: req.MemoryMB, DiskMB: req.DiskMB,
		Labels: labels, Network: &network, IdleTimeoutSecs: old.IdleTimeoutSecs, LifetimeSecs: lifetime,
		SnapshotEverySecs: old.SnapshotEverySecs, SnapshotKeep: old.SnapshotKeep,
	}
	nsb, err := c.CreateSandbox(ctx, copyReq)
	if err != nil {
		dropSnap()
		writeErr(w, http.StatusBadGateway, "starting the copy at the new size: "+err.Error()+"; "+old.ID+" is untouched")
		return
	}
	s.logf("resized %s to %s: %v vCPU, %d MiB", old.ID, nsb.ID, req.CPUs, req.MemoryMB)
	if err := c.TerminateSandbox(ctx, old.ID); err != nil {
		writeErr(w, http.StatusBadGateway, fmt.Sprintf("the copy %s runs at the new size, but %s was not terminated: %v; terminate it yourself", nsb.ID, old.ID, err))
		return
	}
	upd := api.UpdateSandboxRequest{}
	if old.Name != "" {
		upd.Name = &old.Name
	}
	// 0 — never — asks for the server's default at create, so a sandbox
	// that never idled out is given that back after.
	if old.IdleTimeoutSecs != nsb.IdleTimeoutSecs {
		upd.IdleTimeoutSecs = &old.IdleTimeoutSecs
	}
	if upd.Name != nil || upd.IdleTimeoutSecs != nil {
		if nsb, err = c.UpdateSandbox(ctx, nsb.ID, upd); err != nil {
			writeErr(w, http.StatusBadGateway, fmt.Sprintf("the copy %s runs at the new size and %s is gone, but the copy could not take its name or idle timeout: %v", nsb.ID, old.ID, err))
			return
		}
	}
	dropSnap()
	res := ResizeResult{Sandbox: nsb, Replaced: old.ID}
	if req.KeepSnapshot {
		res.Snapshot = snap.ID
	}
	writeJSON(w, http.StatusOK, res)
}

// writeAPIErr passes on the endpoint's API error, or says it did not answer.
func writeAPIErr(w http.ResponseWriter, err error) {
	var ae *api.Error
	if errors.As(err, &ae) {
		writeJSON(w, ae.Status, map[string]any{"error": map[string]string{"message": ae.Message, "code": ae.Code}})
		return
	}
	writeErr(w, http.StatusBadGateway, "the endpoint did not answer: "+err.Error())
}

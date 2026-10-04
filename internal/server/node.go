package server

import (
	"math"
	"net/http"
	"sort"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
	"github.com/Amitgb14/sandbox-cli/internal/version"
)

// A sandboxd behind a gateway is one node of many. The gateway polls GET
// /v1/node to decide where a sandbox goes, and cordons a node it wants to
// drain. Both answer on a standalone sandboxd too: there is nothing secret in
// them that /v1/capabilities and the sandbox list do not already show to the
// same token.
//
// Cordoning is held in memory, so a restart uncordons the node. That is
// acceptable because a restart already loses every sandbox's record (see the
// package comment): a node that restarts is a fresh node, and the gateway,
// which is the one that asked for the cordon, sees Cordoned false on its next
// poll and cordons it again if it still wants to. Persisting it here would
// make the node's state and the gateway's disagree whenever either is changed
// without the other.

const cordonedMsg = "this node is cordoned: it takes no new sandboxes"

// newID is the id for a sandbox this server creates: naming the node, when
// there is one.
func (s *Server) newID() string { return spec.NewIDFor(s.NodeID) }

func (s *Server) node(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.nodeStatus())
}

func (s *Server) cordon(w http.ResponseWriter, r *http.Request) {
	var req api.CordonRequest
	if !decode(w, r, &req) {
		return
	}
	s.mu.Lock()
	s.cordoned = req.Cordoned
	s.mu.Unlock()
	writeJSON(w, http.StatusOK, s.nodeStatus())
}

func (s *Server) nodeStatus() api.NodeStatus {
	st := api.NodeStatus{
		Node:         s.NodeID,
		Version:      version.Version,
		Capabilities: s.caps(),
		Capacity:     s.Capacity,
		Labels:       s.NodeLabels,
	}
	var used api.NodeResources
	add := func(cpus float64, mem, disk, n int) {
		used.CPUs += cpus * float64(n)
		used.MemoryMB += mem * n
		used.DiskMB += disk * n
	}

	s.mu.Lock()
	st.Cordoned = s.cordoned
	recs := make([]*record, 0, len(s.sandboxes))
	for _, rec := range s.sandboxes {
		recs = append(recs, rec)
	}
	pools := s.pools
	s.mu.Unlock()

	// Every sandbox not yet terminated holds what it was given: a pending one
	// is being booted with it, and a suspended one gets it back on resume.
	for _, rec := range recs {
		sb := rec.snapshot()
		if sb.State == api.StateTerminated {
			continue
		}
		if sb.State == api.StateRunning {
			st.Running++
		}
		add(sb.CPUs, sb.MemoryMB, sb.DiskMB, 1)
	}
	// Pooled sandboxes are booted VMs, ready or still booting, though no
	// client sees them; they hold their shape's resources all the same.
	for _, p := range pools {
		p.mu.Lock()
		ready, booting := len(p.ready), p.filling
		p.mu.Unlock()
		add(p.template.CPUs, p.template.MemoryMB, p.template.DiskMB, ready+booting)
		if ready > 0 {
			if st.Pooled == nil {
				st.Pooled = map[string]int{}
			}
			st.Pooled[p.template.Image] += ready
		}
	}

	// Free is never negative: a node configured with less capacity than it
	// has already given out is full, not in debt.
	st.Free = api.NodeResources{
		CPUs:     math.Max(0, s.Capacity.CPUs-used.CPUs),
		MemoryMB: max(0, s.Capacity.MemoryMB-used.MemoryMB),
		DiskMB:   max(0, s.Capacity.DiskMB-used.DiskMB),
	}

	if il, ok := s.Backend.(backend.ImageLister); ok {
		st.Images = il.CachedImages()
		sort.Strings(st.Images)
	}
	return st
}

package server

import (
	"crypto/rand"
	"encoding/hex"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
)

// Suspend, resume and snapshots: what makes a sandbox cheap to keep. Measured
// in M3 on Firecracker: a 1 GiB guest snapshots in ~230 ms and restores in
// ~6 ms. Each is a capability, and a backend without it says so (501).

type snapshotRecord struct {
	info api.Snapshot
	spec backend.SnapshotInfo
}

type snapshots struct {
	mu sync.Mutex
	m  map[string]*snapshotRecord
}

func (s *Server) snaps() *snapshots {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.snapshotStore == nil {
		s.snapshotStore = &snapshots{m: map[string]*snapshotRecord{}}
	}
	return s.snapshotStore
}

func newSnapshotID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return "snp_" + hex.EncodeToString(b[:])
}

// running processes block a suspend: their streams run over the connection a
// suspend cuts, and a process whose output nobody can collect any more would be
// reported as exited while it carries on in the guest.
func busy(rec *record) bool {
	rec.mu.Lock()
	defer rec.mu.Unlock()
	for _, pr := range rec.procs {
		if pr.info.State == api.ProcessRunning {
			return true
		}
	}
	return false
}

func (s *Server) suspend(w http.ResponseWriter, r *http.Request) {
	sus, ok := s.Backend.(backend.Suspender)
	if !ok || !s.Backend.Capabilities()[api.CapSuspend] {
		writeErr(w, http.StatusNotImplemented, api.CodeUnsupported, "this endpoint cannot suspend a sandbox")
		return
	}
	rec, ok := s.live(w, r)
	if !ok {
		return
	}
	if busy(rec) {
		writeErr(w, http.StatusConflict, api.CodeConflict, "processes are running; wait for them or signal them first")
		return
	}
	if err := sus.Suspend(r.Context(), rec.snapshot().ID); err != nil {
		writeBackendErr(w, err)
		return
	}
	rec.mu.Lock()
	rec.sbx.State = api.StateSuspended
	out := rec.sbx
	rec.mu.Unlock()
	s.event(api.Event{Type: api.EventSandboxSuspended, Sandbox: out.ID})
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) resume(w http.ResponseWriter, r *http.Request) {
	sus, ok := s.Backend.(backend.Suspender)
	if !ok {
		writeErr(w, http.StatusNotImplemented, api.CodeUnsupported, "this endpoint cannot resume a sandbox")
		return
	}
	rec, ok := s.lookup(r.PathValue("ref"))
	if !ok {
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such sandbox")
		return
	}
	if rec.snapshot().State != api.StateSuspended {
		writeErr(w, http.StatusConflict, api.CodeConflict, "sandbox is not suspended")
		return
	}
	if err := sus.Resume(r.Context(), rec.snapshot().ID); err != nil {
		writeBackendErr(w, err)
		return
	}
	rec.mu.Lock()
	rec.sbx.State = api.StateRunning
	rec.lastActive = s.now()
	out := rec.sbx
	rec.mu.Unlock()
	s.event(api.Event{Type: api.EventSandboxResumed, Sandbox: out.ID})
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) createSnapshot(w http.ResponseWriter, r *http.Request) {
	sn, ok := s.Backend.(backend.Snapshotter)
	if !ok || !s.Backend.Capabilities()[api.CapMemorySnapshot] {
		writeErr(w, http.StatusNotImplemented, api.CodeUnsupported, "this endpoint cannot snapshot a sandbox")
		return
	}
	rec, ok := s.live(w, r)
	if !ok {
		return
	}
	sb := rec.snapshot()
	id := newSnapshotID()
	info, err := sn.Snapshot(r.Context(), sb.ID, id)
	if err != nil {
		writeBackendErr(w, err)
		return
	}
	out := api.Snapshot{ID: id, Sandbox: sb.ID, Image: sb.Image, Bytes: info.Bytes, CreatedAt: s.now().UTC()}
	st := s.snaps()
	st.mu.Lock()
	st.m[id] = &snapshotRecord{info: out, spec: info}
	st.mu.Unlock()
	s.event(api.Event{Type: api.EventSnapshotCreated, Sandbox: sb.ID, Snapshot: id, Bytes: info.Bytes})
	writeJSON(w, http.StatusCreated, out)
}

func (s *Server) listSnapshots(w http.ResponseWriter, r *http.Request) {
	st := s.snaps()
	st.mu.Lock()
	out := make([]api.Snapshot, 0, len(st.m))
	for _, rec := range st.m {
		out = append(out, rec.info)
	}
	st.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	writeJSON(w, http.StatusOK, api.SnapshotList{Snapshots: out})
}

func (s *Server) deleteSnapshot(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	st := s.snaps()
	st.mu.Lock()
	_, ok := st.m[id]
	delete(st.m, id)
	st.mu.Unlock()
	if !ok {
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such snapshot")
		return
	}
	if sn, ok := s.Backend.(backend.Snapshotter); ok {
		_ = sn.DeleteSnapshot(r.Context(), id)
	}
	w.WriteHeader(http.StatusNoContent)
}

// fromSnapshot fills a create request from the snapshot it names. A snapshot
// fixes the image and the resources — they are what the captured memory was
// running with — so a request that asks for different ones is refused rather
// than quietly given the snapshot's.
func (s *Server) fromSnapshot(req *api.CreateSandboxRequest) (string, string) {
	st := s.snaps()
	st.mu.Lock()
	rec, ok := st.m[req.SnapshotID]
	st.mu.Unlock()
	if !ok {
		return api.CodeNotFound, "no such snapshot"
	}
	sp := rec.spec
	for _, c := range []struct {
		asked, have any
		zero        bool
		name        string
	}{
		{req.Image, sp.Image, req.Image == "", "image"},
		{req.CPUs, sp.CPUs, req.CPUs == 0, "cpus"},
		{req.MemoryMB, sp.MemoryMB, req.MemoryMB == 0, "memory_mb"},
		{req.DiskMB, sp.DiskMB, req.DiskMB == 0, "disk_mb"},
	} {
		if !c.zero && c.asked != c.have {
			return api.CodeInvalidRequest, c.name + " is fixed by the snapshot"
		}
	}
	req.Image, req.CPUs, req.MemoryMB, req.DiskMB = sp.Image, sp.CPUs, sp.MemoryMB, sp.DiskMB
	return "", ""
}

// tunnel splices a client connection to a port on the guest's own loopback.
func (s *Server) tunnel(w http.ResponseWriter, r *http.Request) {
	d, ok := s.Backend.(backend.Dialer)
	if !ok || !s.Backend.Capabilities()[api.CapTunnel] {
		writeErr(w, http.StatusNotImplemented, api.CodeUnsupported, "this endpoint cannot open tunnels")
		return
	}
	if !strings.EqualFold(r.Header.Get("Upgrade"), api.TunnelProtocol) {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "a tunnel needs Upgrade: "+api.TunnelProtocol)
		return
	}
	port, err := strconv.Atoi(r.URL.Query().Get("port"))
	if err != nil || port < 1 || port > 65535 {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "port: 1-65535")
		return
	}
	rec, ok := s.live(w, r)
	if !ok {
		return
	}
	guest, err := d.DialGuest(r.Context(), rec.snapshot().ID, port)
	if err != nil {
		writeBackendErr(w, err)
		return
	}
	s.event(api.Event{Type: api.EventTunnelOpened, Sandbox: rec.snapshot().ID, Port: port})
	defer guest.Close()
	hj, ok := w.(http.Hijacker)
	if !ok {
		writeErr(w, http.StatusInternalServerError, api.CodeInternal, "this connection cannot be upgraded")
		return
	}
	conn, brw, err := hj.Hijack()
	if err != nil {
		return
	}
	defer conn.Close()
	_, _ = brw.WriteString("HTTP/1.1 101 Switching Protocols\r\nConnection: Upgrade\r\nUpgrade: " + api.TunnelProtocol + "\r\n\r\n")
	if brw.Flush() != nil {
		return
	}
	// The client finishing only half-closes toward the guest; the guest's side
	// finishing ends the tunnel. Tearing both down when either ended lost the
	// reply of every client that sends and then waits for EOF.
	go func() {
		_, _ = io.Copy(guest, brw.Reader)
		if cw, ok := guest.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}()
	_, _ = io.Copy(conn, guest)
	rec.touch(s.now())
}

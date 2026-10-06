// Package server serves Sandbox API v1 (docs/api/v1.md) for one machine, over
// whichever backend that machine has.
//
// It owns three things and delegates the rest:
//   - **who may ask** — the request guard (guard.go) and the bearer token;
//   - **what may be asked** — every request goes through spec, which applies the
//     server's policy and refuses what would loosen it;
//   - **which sandboxes exist** — the server keeps its own list, and a reference
//     is matched against that list only. It is never handed to the backend to
//     resolve, so a name that happens to match something else on the machine
//     reaches nothing.
//
// Everything that touches a VM is the backend's.
//
// State lives in memory: a restart forgets its sandboxes' records (the backend's
// VMs are reconciled from the backend's own listing once a real backend exists,
// M5). Process output is kept per process, capped, so a client that connects
// late still reads it from the beginning.
package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/audit"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

const healthPath = "/v1/health"

// keepTerminated is how many terminated sandboxes stay listed. Enough for a
// client to read the final state of what it just deleted; not a history.
const keepTerminated = 100

// Server serves the API. Configure the exported fields, then use Handler.
type Server struct {
	Backend backend.Backend
	Policy  spec.Policy
	// Token, when set, is required on every request but the health check.
	Token string
	// AllowedHosts are Host names answered besides loopback — the name a
	// self-hosted server is reached by.
	AllowedHosts []string
	// CORSOrigins are browser origins allowed to call this server.
	CORSOrigins []string
	// Audit, when set, records every sandbox's events and serves them at
	// GET /v1/sandboxes/{ref}/events.
	Audit *audit.Log
	// Logf, when set, receives the operator's messages (pool trouble).
	Logf func(format string, a ...any)

	// NodeID, when set, makes this sandboxd one node of many behind a
	// gateway: every sandbox id it creates names it (spec.NewIDFor), so the
	// gateway routes later calls without a lookup. Empty is a standalone
	// sandboxd, whose ids name no node. Validated by api.ValidNodeID.
	NodeID string
	// Capacity is what this machine offers sandboxes, reported at GET
	// /v1/node; Free there is Capacity less what sandboxes are given.
	Capacity api.NodeResources
	// NodeLabels describe the node to a gateway (region, disk class, ...).
	NodeLabels map[string]string

	now func() time.Time

	mu            sync.Mutex
	sandboxes     map[string]*record // id -> record
	deleting      map[string]bool    // volumes being deleted, under mu
	pools         []*pool
	poolsOnce     sync.Once
	reaper        sync.Once
	snapshotStore *snapshots
	cordoned      bool // under mu; see node.go
	metricsOnce   sync.Once
	nm            *nodeMetrics // metrics.go
}

type record struct {
	mu         sync.Mutex
	sbx        api.Sandbox
	env        map[string]string
	procs      map[int]*procRecord
	nextPID    int
	lastActive time.Time
	// lastScheduled is when the snapshot schedule last ran (or started), and
	// snapshotting is set while one of its snapshots is being taken.
	lastScheduled time.Time
	snapshotting  bool
}

// touch records activity: any request naming the sandbox keeps it alive.
func (r *record) touch(now time.Time) {
	r.mu.Lock()
	r.lastActive = now
	r.mu.Unlock()
}

type procRecord struct {
	info api.Process
	proc backend.Proc
	log  *outputLog
}

// Handler returns the API's HTTP handler, guard included.
func (s *Server) Handler() http.Handler {
	if s.now == nil {
		s.now = time.Now
	}
	s.mu.Lock()
	if s.sandboxes == nil {
		s.sandboxes = map[string]*record{}
	}
	s.mu.Unlock()

	mux := http.NewServeMux()
	route := func(pattern string, raw bool, h http.HandlerFunc) {
		mux.Handle(pattern, s.guard(raw, h))
	}
	route("GET "+healthPath, false, func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	route("GET /v1/capabilities", false, s.capabilities)
	route("GET /v1/node", false, s.node)
	route("POST /v1/node/cordon", false, s.cordon)
	route("POST /v1/sandboxes", false, s.timedCreate)
	route("GET /v1/sandboxes", false, s.listSandboxes)
	route("GET /v1/sandboxes/{ref}", false, s.getSandbox)
	route("PATCH /v1/sandboxes/{ref}", false, s.updateSandbox)
	route("DELETE /v1/sandboxes/{ref}", false, s.terminateSandbox)
	route("POST /v1/sandboxes/{ref}/run", false, s.run)
	route("POST /v1/sandboxes/{ref}/processes", false, s.startProcess)
	route("GET /v1/sandboxes/{ref}/processes", false, s.listProcesses)
	route("GET /v1/sandboxes/{ref}/processes/{pid}", false, s.getProcess)
	route("GET /v1/sandboxes/{ref}/processes/{pid}/output", false, s.followOutput)
	route("POST /v1/sandboxes/{ref}/processes/{pid}/stdin", true, s.writeStdin)
	route("POST /v1/sandboxes/{ref}/processes/{pid}/signal", false, s.signal)
	route("GET /v1/sandboxes/{ref}/processes/{pid}/attach", false, s.attach)
	route("POST /v1/sandboxes/{ref}/suspend", false, s.suspend)
	route("POST /v1/sandboxes/{ref}/resume", false, s.resume)
	route("POST /v1/sandboxes/{ref}/snapshots", false, s.createSnapshot)
	route("PUT /v1/sandboxes/{ref}/snapshot-schedule", false, s.setSnapshotSchedule)
	route("GET /v1/snapshots", false, s.listSnapshots)
	route("DELETE /v1/snapshots/{id}", false, s.deleteSnapshot)
	route("GET /v1/sandboxes/{ref}/tunnel", false, s.tunnel)
	route("GET /v1/sandboxes/{ref}/files", false, s.readFile)
	route("PUT /v1/sandboxes/{ref}/files", true, s.writeFile)
	route("DELETE /v1/sandboxes/{ref}/files", false, s.removeFile)
	route("GET /v1/sandboxes/{ref}/dirs", false, s.listDir)
	route("GET /v1/sandboxes/{ref}/events", false, s.events)
	route("POST /v1/volumes", false, s.createVolume)
	route("GET /v1/volumes", false, s.listVolumes)
	route("DELETE /v1/volumes/{name}", false, s.deleteVolume)
	s.reaper.Do(func() {
		go s.reapIdle()
		go s.runSchedules()
	})
	s.poolsOnce.Do(s.startPools)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such endpoint")
	})
	return mux
}

// guard applies, in order: the Host check (DNS rebinding), the Origin check
// (a web page driving the API), the token, the content type, and the body cap.
// Host and Origin come before the token deliberately: a request refused for its
// origin learns nothing about whether its token was right.
func (s *Server) guard(raw bool, next http.HandlerFunc) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.hostAllowed(r) {
			writeErr(w, http.StatusForbidden, api.CodeForbiddenOrigin, "Host "+r.Host+" is not one this server answers to")
			return
		}
		if !s.originAllowed(r) {
			writeErr(w, http.StatusForbidden, api.CodeForbiddenOrigin, "requests from this origin are not accepted")
			return
		}
		if !s.authorized(r) {
			w.Header().Set("WWW-Authenticate", "Bearer")
			writeErr(w, http.StatusUnauthorized, api.CodeUnauthorized, "missing or wrong bearer token")
			return
		}
		if err := checkContentType(r, raw); err != nil {
			writeErr(w, http.StatusUnsupportedMediaType, api.CodeInvalidRequest, err.Error())
			return
		}
		limit := int64(maxRequestBody)
		if raw {
			limit = maxRawBody
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		next(w, r)
	})
}

// --- capabilities and sandboxes ---------------------------------------------

func (s *Server) capabilities(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.caps())
}

// caps is the body of /v1/capabilities, which GET /v1/node repeats.
func (s *Server) caps() api.Capabilities {
	caps := map[string]bool{
		api.CapNetworkPolicyUpdate: false, api.CapSuspend: false,
		api.CapMemorySnapshot: false,
	}
	for k, v := range s.Backend.Capabilities() {
		caps[k] = v
	}
	caps[api.CapAudit] = s.Audit != nil
	return api.Capabilities{
		APIVersion:   api.Version,
		Backend:      s.Backend.Name(),
		Capabilities: caps,
		Limits:       s.Policy.Limits,
		Network:      s.Policy.Ceiling(),
	}
}

func (s *Server) createSandbox(w http.ResponseWriter, r *http.Request) {
	var req api.CreateSandboxRequest
	if !decode(w, r, &req) {
		return
	}
	if req.SnapshotID != "" {
		if !canSnapshot(s.Backend.Capabilities()) {
			writeErr(w, http.StatusNotImplemented, api.CodeUnsupported, "this endpoint cannot start from a snapshot")
			return
		}
		if code, msg := s.fromSnapshot(&req); code != "" {
			status := http.StatusBadRequest
			if code == api.CodeNotFound {
				status = http.StatusNotFound
			}
			writeErr(w, status, code, msg)
			return
		}
	}
	id := s.newID()
	bs, err := spec.Resolve(req, s.Policy, id)
	if err != nil {
		writeSpecErr(w, err)
		return
	}
	if !s.canEnforce(w, bs.Network) {
		return
	}
	bs.FromSnapshot = req.SnapshotID
	// Refused before anything is made, as any control that cannot be
	// delivered is: a schedule that never takes a snapshot is worse than none.
	if status, code, msg := s.scheduleRefusal(api.SnapshotSchedule{EverySecs: bs.SnapshotEverySecs, Keep: bs.SnapshotKeep}, bs.Volumes); status != 0 {
		writeErr(w, status, code, msg)
		return
	}
	if len(bs.Volumes) > 0 {
		if bs.FromSnapshot != "" {
			writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "a sandbox started from a snapshot cannot mount volumes")
			return
		}
		if !s.volumesExist(w, r, bs.Volumes) {
			return
		}
	}

	// The name is claimed before the backend is asked, under the lock, so two
	// concurrent creates with one name cannot both pass a check-then-create.
	rec := &record{
		sbx: api.Sandbox{
			ID: id, Name: req.Name, State: api.StatePending, Image: bs.Image,
			CPUs: bs.CPUs, MemoryMB: bs.MemoryMB, DiskMB: bs.DiskMB,
			EnvNames: sortedKeys(bs.Env), Network: bs.Network, CreatedAt: s.now().UTC(),
			IdleTimeoutSecs:   bs.IdleTimeoutSecs,
			SnapshotEverySecs: bs.SnapshotEverySecs,
			SnapshotKeep:      bs.SnapshotKeep,
			Labels:            copyLabels(req.Labels),
			Volumes:           bs.Volumes,
		},
		env:           bs.Env,
		procs:         map[int]*procRecord{},
		lastActive:    s.now(),
		lastScheduled: s.now(),
	}
	s.mu.Lock()
	// Checked under the same lock that registers the sandbox, so once a cordon
	// has returned no create that began before it can still land.
	if s.cordoned {
		s.mu.Unlock()
		writeErr(w, http.StatusServiceUnavailable, api.CodeUnavailable, cordonedMsg)
		return
	}
	if req.Name != "" {
		for _, o := range s.sandboxes {
			if o.snapshot().Name == req.Name && o.snapshot().State != api.StateTerminated {
				s.mu.Unlock()
				writeErr(w, http.StatusConflict, api.CodeConflict, "a sandbox named "+req.Name+" already exists")
				return
			}
		}
	}
	// Claimed under the same lock as the name, for the same reason: a
	// volume has one writer or only readers (volumeBusy), and
	// check-then-attach would let two creates both pass.
	for _, m := range bs.Volumes {
		if s.deleting[m.Name] {
			s.mu.Unlock()
			writeErr(w, http.StatusConflict, api.CodeConflict, "volume "+m.Name+" is being deleted")
			return
		}
		if why := s.volumeBusy(m); why != "" {
			s.mu.Unlock()
			writeErr(w, http.StatusConflict, api.CodeConflict, why)
			return
		}
	}
	s.sandboxes[id] = rec
	s.mu.Unlock()

	// A pooled sandbox of this exact shape, if one is ready, takes the place
	// of a boot: the record moves to its id.
	from := ""
	if pooled := s.claimPooled(bs); pooled != "" {
		s.mu.Lock()
		delete(s.sandboxes, id)
		id = pooled
		rec.mu.Lock()
		rec.sbx.ID = id
		rec.mu.Unlock()
		s.sandboxes[id] = rec
		s.mu.Unlock()
		from = "pool"
	} else if err := s.Backend.Create(r.Context(), bs); err != nil {
		s.mu.Lock()
		delete(s.sandboxes, id)
		s.mu.Unlock()
		writeBackendErr(w, err)
		return
	}
	rec.mu.Lock()
	rec.sbx.State = api.StateRunning
	out := rec.sbx
	rec.mu.Unlock()
	ev := api.Event{Type: api.EventSandboxCreated, Sandbox: id, Name: out.Name, Image: out.Image,
		Labels: out.Labels, Network: &out.Network, EnvNames: out.EnvNames, Snapshot: req.SnapshotID, Volumes: out.Volumes,
		Reason: from}
	s.event(ev)
	writeJSON(w, http.StatusCreated, out)
}

// event records one audit event, if the server keeps a log.
func (s *Server) event(e api.Event) {
	if s.Audit == nil {
		return
	}
	e.Time = s.now().UTC()
	s.Audit.Record(e)
}

func copyLabels(l map[string]string) map[string]string {
	if len(l) == 0 {
		return nil
	}
	out := make(map[string]string, len(l))
	for k, v := range l {
		out[k] = v
	}
	return out
}

// maxEvents bounds one events response; the newest are kept.
const maxEvents = 5000

// events serves a sandbox's audit events. A sandbox this server has already
// forgotten is still answered by id, since the log outlives the record —
// but only by id, the one reference that cannot name somebody else's
// sandbox; a name is matched against live records only.
func (s *Server) events(w http.ResponseWriter, r *http.Request) {
	if s.Audit == nil {
		writeErr(w, http.StatusNotImplemented, api.CodeUnsupported, "this server keeps no audit log")
		return
	}
	ref := r.PathValue("ref")
	id := ref
	if rec, ok := s.find(ref); ok {
		id = rec.snapshot().ID
	} else if !spec.ValidID(ref) {
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such sandbox")
		return
	}
	events, truncated, err := s.Audit.Read(id, maxEvents)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, api.CodeInternal, "reading the audit log failed")
		return
	}
	if len(events) == 0 {
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such sandbox")
		return
	}
	writeJSON(w, http.StatusOK, api.EventList{Events: events, Truncated: truncated})
}

func (r *record) snapshot() api.Sandbox {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sbx
}

func (s *Server) listSandboxes(w http.ResponseWriter, r *http.Request) {
	// ?label=k=v, repeatable: every one must match.
	want := map[string]string{}
	for _, l := range r.URL.Query()["label"] {
		k, v, ok := strings.Cut(l, "=")
		if !ok || k == "" {
			writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "label: want key=value")
			return
		}
		want[k] = v
	}
	s.mu.Lock()
	list := make([]api.Sandbox, 0, len(s.sandboxes))
	for _, rec := range s.sandboxes {
		sb := rec.snapshot()
		match := true
		for k, v := range want {
			if got, ok := sb.Labels[k]; !ok || got != v {
				match = false
			}
		}
		if match {
			list = append(list, sb)
		}
	}
	s.mu.Unlock()
	sort.Slice(list, func(i, j int) bool {
		if !list[i].CreatedAt.Equal(list[j].CreatedAt) {
			return list[i].CreatedAt.After(list[j].CreatedAt)
		}
		return list[i].ID > list[j].ID
	})
	writeJSON(w, http.StatusOK, api.SandboxList{Sandboxes: list})
}

// lookup resolves a reference — an id, or a name — against this server's own
// sandboxes. A name prefers the live sandbox, then the newest terminated one, so
// that deleting by name twice is still idempotent.
func (s *Server) lookup(ref string) (*record, bool) {
	rec, ok := s.find(ref)
	if ok {
		rec.touch(s.now())
	}
	return rec, ok
}

func (s *Server) find(ref string) (*record, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if rec, ok := s.sandboxes[ref]; ok {
		return rec, true
	}
	var best *record
	var bestSbx api.Sandbox
	for _, rec := range s.sandboxes {
		sb := rec.snapshot()
		if sb.Name == "" || sb.Name != ref {
			continue
		}
		if sb.State != api.StateTerminated {
			return rec, true
		}
		if best == nil || sb.CreatedAt.After(bestSbx.CreatedAt) {
			best, bestSbx = rec, sb
		}
	}
	return best, best != nil
}

// live resolves ref and refuses a terminated sandbox, for every operation that
// needs a running one.
func (s *Server) live(w http.ResponseWriter, r *http.Request) (*record, bool) {
	rec, ok := s.lookup(r.PathValue("ref"))
	if !ok {
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such sandbox")
		return nil, false
	}
	if rec.snapshot().State != api.StateRunning {
		writeErr(w, http.StatusConflict, api.CodeConflict, "sandbox is not running")
		return nil, false
	}
	return rec, true
}

func (s *Server) getSandbox(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.lookup(r.PathValue("ref"))
	if !ok {
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such sandbox")
		return
	}
	writeJSON(w, http.StatusOK, rec.snapshot())
}

func (s *Server) updateSandbox(w http.ResponseWriter, r *http.Request) {
	var req api.UpdateSandboxRequest
	if !decode(w, r, &req) {
		return
	}
	rec, ok := s.live(w, r)
	if !ok {
		return
	}
	if req.Network == nil {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "nothing to update")
		return
	}
	if !s.Backend.Capabilities()[api.CapNetworkPolicyUpdate] {
		writeErr(w, http.StatusNotImplemented, api.CodeUnsupported, "this endpoint cannot change a running sandbox's network policy")
		return
	}
	pol, err := spec.ResolveNetworkUpdate(req.Network, s.Policy)
	if err != nil {
		writeSpecErr(w, err)
		return
	}
	if !s.canEnforce(w, pol) {
		return
	}
	id := rec.snapshot().ID
	if err := s.Backend.UpdateNetwork(r.Context(), id, pol); err != nil {
		writeBackendErr(w, err)
		return
	}
	rec.mu.Lock()
	rec.sbx.Network = pol
	out := rec.sbx
	rec.mu.Unlock()
	s.event(api.Event{Type: api.EventNetworkUpdated, Sandbox: id, Network: &pol})
	writeJSON(w, http.StatusOK, out)
}

func (s *Server) terminateSandbox(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.lookup(r.PathValue("ref"))
	if !ok {
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such sandbox")
		return
	}
	sb := rec.snapshot()
	if sb.State != api.StateTerminated {
		if err := s.Backend.Terminate(r.Context(), sb.ID); err != nil {
			writeBackendErr(w, err)
			return
		}
		rec.mu.Lock()
		rec.sbx.State = api.StateTerminated
		rec.mu.Unlock()
		s.event(api.Event{Type: api.EventSandboxTerminated, Sandbox: sb.ID, Reason: "request"})
		s.forgetOldTerminated()
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) forgetOldTerminated() {
	s.mu.Lock()
	defer s.mu.Unlock()
	var dead []api.Sandbox
	for _, rec := range s.sandboxes {
		if sb := rec.snapshot(); sb.State == api.StateTerminated {
			dead = append(dead, sb)
		}
	}
	if len(dead) <= keepTerminated {
		return
	}
	sort.Slice(dead, func(i, j int) bool { return dead[i].CreatedAt.Before(dead[j].CreatedAt) })
	for _, sb := range dead[:len(dead)-keepTerminated] {
		delete(s.sandboxes, sb.ID)
	}
}

// canEnforce refuses a network policy the backend cannot enforce. spec has
// already applied the server's policy; this is the second check, against the
// backend itself, so that a misconfigured policy fails closed: a request for an
// allowlist on a backend without host networking is refused, never served as a
// sandbox that is quietly open or quietly offline.
func (s *Server) canEnforce(w http.ResponseWriter, p api.NetworkPolicy) bool {
	caps := s.Backend.Capabilities()
	switch {
	case p.Mode == api.NetworkAllowlist && !caps[api.CapEgressAllowlist]:
		writeErr(w, http.StatusNotImplemented, api.CodeUnsupported, "this endpoint cannot enforce an egress allowlist")
		return false
	case p.Mode == api.NetworkOpen && !caps[api.CapEgressOpen]:
		writeErr(w, http.StatusNotImplemented, api.CodeUnsupported, "this endpoint does not offer open egress")
		return false
	}
	return true
}

// reapIdle terminates sandboxes that have been idle past their timeout: no
// request has named them and no process is running in them. A running process
// is activity even with nobody watching — a build left to finish is not idle.
func (s *Server) reapIdle() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for range t.C {
		now := s.now()
		s.mu.Lock()
		var due []*record
		for _, rec := range s.sandboxes {
			rec.mu.Lock()
			idle := rec.sbx.IdleTimeoutSecs
			live := rec.sbx.State == api.StateRunning
			busy := false
			for _, pr := range rec.procs {
				busy = busy || pr.info.State == api.ProcessRunning
			}
			if busy {
				rec.lastActive = now
			}
			if live && idle > 0 && now.Sub(rec.lastActive) >= time.Duration(idle)*time.Second {
				due = append(due, rec)
			}
			rec.mu.Unlock()
		}
		s.mu.Unlock()
		for _, rec := range due {
			id := rec.snapshot().ID
			if err := s.Backend.Terminate(context.Background(), id); err != nil {
				continue
			}
			rec.mu.Lock()
			rec.sbx.State = api.StateTerminated
			rec.mu.Unlock()
			s.event(api.Event{Type: api.EventSandboxTerminated, Sandbox: id, Reason: "idle"})
		}
		if len(due) > 0 {
			s.forgetOldTerminated()
		}
	}
}

// --- processes ---------------------------------------------------------------

// start validates a run request and starts it, recording the process.
func (s *Server) start(w http.ResponseWriter, r *http.Request, rec *record, req api.RunRequest) (*procRecord, bool) {
	if len(req.Argv) == 0 || req.Argv[0] == "" {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "argv: required")
		return nil, false
	}
	for _, a := range req.Argv {
		if strings.ContainsRune(a, 0) {
			writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "argv contains a NUL byte")
			return nil, false
		}
	}
	if req.Cwd != "" {
		if err := checkGuestPath(req.Cwd); err != nil {
			writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "cwd: "+err.Error())
			return nil, false
		}
	}
	extra, err := spec.ResolveEnv(req.Env)
	if err != nil {
		writeSpecErr(w, err)
		return nil, false
	}

	rec.mu.Lock()
	env := make(map[string]string, len(rec.env)+len(extra))
	for k, v := range rec.env {
		env[k] = v
	}
	for k, v := range extra {
		env[k] = v
	}
	id := rec.sbx.ID
	rec.mu.Unlock()

	log := newOutputLog()
	ps := backend.ProcSpec{Argv: append([]string(nil), req.Argv...), Env: env, Cwd: req.Cwd, Tty: req.Tty, Rows: req.Rows, Cols: req.Cols}
	// The process outlives this request, so it gets a context of its own; it is
	// ended by its own exit, a signal, or the sandbox being terminated.
	proc, err := s.Backend.Start(context.WithoutCancel(r.Context()), id, ps, log.writer("stdout"), log.writer("stderr"))
	if err != nil {
		writeBackendErr(w, err)
		return nil, false
	}

	rec.mu.Lock()
	rec.nextPID++
	pr := &procRecord{
		info: api.Process{PID: rec.nextPID, Tty: ps.Tty, Argv: ps.Argv, State: api.ProcessRunning, StartedAt: s.now().UTC()},
		proc: proc,
		log:  log,
	}
	rec.procs[pr.info.PID] = pr
	pid, started := pr.info.PID, pr.info.StartedAt
	rec.mu.Unlock()
	s.event(processStarted(id, pid, ps, sortedKeys(extra)))

	go func() {
		code := proc.Wait()
		rec.mu.Lock()
		pr.info.State = api.ProcessExited
		pr.info.ExitCode = &code
		rec.mu.Unlock()
		log.finish(code)
		s.event(api.Event{Type: api.EventProcessExited, Sandbox: id, PID: pid, ExitCode: &code,
			DurationMS: s.now().Sub(started).Milliseconds()})
	}()
	return pr, true
}

func (s *Server) run(w http.ResponseWriter, r *http.Request) {
	var req api.RunRequest
	if !decode(w, r, &req) {
		return
	}
	rec, ok := s.live(w, r)
	if !ok {
		return
	}
	if req.TimeoutSecs < 0 {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "timeout_secs must not be negative")
		return
	}
	if req.Tty {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "a terminal is for a background process: start one and attach to it")
		return
	}
	pr, ok := s.start(w, r, rec, req)
	if !ok {
		return
	}
	stdin := pr.proc.Stdin()
	if len(req.Stdin) > 0 {
		_, _ = stdin.Write(req.Stdin)
	}
	_ = stdin.Close()

	done := make(chan int, 1)
	go func() { done <- pr.proc.Wait() }()
	var timeout <-chan time.Time
	if req.TimeoutSecs > 0 {
		t := time.NewTimer(time.Duration(req.TimeoutSecs) * time.Second)
		defer t.Stop()
		timeout = t.C
	}

	res := api.RunResult{}
	select {
	case res.ExitCode = <-done:
	case <-timeout:
		_ = pr.proc.Signal("KILL")
		<-done
		res.ExitCode, res.TimedOut = -1, true
	case <-r.Context().Done():
		// The caller left; the process does not outlive the request it was run for.
		_ = pr.proc.Signal("KILL")
		return
	}
	res.Stdout, res.Stderr, res.Truncated = pr.log.collect()
	writeJSON(w, http.StatusOK, res)
}

func (s *Server) startProcess(w http.ResponseWriter, r *http.Request) {
	var req api.RunRequest
	if !decode(w, r, &req) {
		return
	}
	rec, ok := s.live(w, r)
	if !ok {
		return
	}
	pr, ok := s.start(w, r, rec, req)
	if !ok {
		return
	}
	rec.mu.Lock()
	info := pr.info
	rec.mu.Unlock()
	writeJSON(w, http.StatusCreated, info)
}

func (s *Server) listProcesses(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.lookup(r.PathValue("ref"))
	if !ok {
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such sandbox")
		return
	}
	rec.mu.Lock()
	list := make([]api.Process, 0, len(rec.procs))
	for _, pr := range rec.procs {
		list = append(list, pr.info)
	}
	rec.mu.Unlock()
	sort.Slice(list, func(i, j int) bool { return list[i].PID < list[j].PID })
	writeJSON(w, http.StatusOK, api.ProcessList{Processes: list})
}

// process resolves ref and pid. A process of a terminated sandbox is still
// readable — its output is the record of what happened.
func (s *Server) process(w http.ResponseWriter, r *http.Request) (*record, *procRecord, bool) {
	rec, ok := s.lookup(r.PathValue("ref"))
	if !ok {
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such sandbox")
		return nil, nil, false
	}
	pid, err := strconv.Atoi(r.PathValue("pid"))
	rec.mu.Lock()
	pr, found := rec.procs[pid]
	rec.mu.Unlock()
	if err != nil || !found {
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "no such process")
		return nil, nil, false
	}
	return rec, pr, true
}

func (s *Server) getProcess(w http.ResponseWriter, r *http.Request) {
	rec, pr, ok := s.process(w, r)
	if !ok {
		return
	}
	rec.mu.Lock()
	info := pr.info
	rec.mu.Unlock()
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) followOutput(w http.ResponseWriter, r *http.Request) {
	_, pr, ok := s.process(w, r)
	if !ok {
		return
	}
	w.Header().Set("Content-Type", "application/x-ndjson")
	w.WriteHeader(http.StatusOK)
	flusher, _ := w.(http.Flusher)
	enc := json.NewEncoder(w)
	_ = pr.log.follow(r.Context(), func(ev api.OutputEvent) error {
		if err := enc.Encode(ev); err != nil {
			return err
		}
		if flusher != nil {
			flusher.Flush()
		}
		return nil
	})
}

func (s *Server) writeStdin(w http.ResponseWriter, r *http.Request) {
	rec, pr, ok := s.process(w, r)
	if !ok {
		return
	}
	rec.mu.Lock()
	exited := pr.info.State == api.ProcessExited
	rec.mu.Unlock()
	if exited {
		writeErr(w, http.StatusConflict, api.CodeConflict, "process has exited")
		return
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, api.CodeInvalidRequest, "stdin body too large or unreadable")
		return
	}
	stdin := pr.proc.Stdin()
	if len(data) > 0 {
		if _, err := stdin.Write(data); err != nil {
			writeErr(w, http.StatusConflict, api.CodeConflict, "stdin is closed")
			return
		}
	}
	if r.URL.Query().Get("close") == "1" {
		_ = stdin.Close()
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) signal(w http.ResponseWriter, r *http.Request) {
	var req api.SignalRequest
	if !decode(w, r, &req) {
		return
	}
	_, pr, ok := s.process(w, r)
	if !ok {
		return
	}
	known := false
	for _, sig := range api.Signals {
		known = known || sig == req.Signal
	}
	if !known {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "signal "+req.Signal+": want one of "+strings.Join(api.Signals, ", "))
		return
	}
	if err := pr.proc.Signal(req.Signal); err != nil {
		writeBackendErr(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- files -------------------------------------------------------------------

// checkGuestPath accepts an absolute guest path with no `..` element and no NUL.
//
// The path is the guest's, and the server never resolves it on the host; the
// check exists so that what reaches a backend is one unambiguous spelling, and
// so that a backend which does resolve paths against something (a mounted image,
// a directory it owns) is never handed a traversal to get right on its own.
func checkGuestPath(p string) error {
	switch {
	case p == "":
		return errors.New("path is required")
	case !strings.HasPrefix(p, "/"):
		return errors.New("path must be absolute")
	case strings.ContainsRune(p, 0):
		return errors.New("path contains a NUL byte")
	}
	for _, el := range strings.Split(p, "/") {
		if el == ".." {
			return errors.New("path must not contain ..")
		}
	}
	return nil
}

func (s *Server) filePath(w http.ResponseWriter, r *http.Request) (*record, string, bool) {
	p := r.URL.Query().Get("path")
	if err := checkGuestPath(p); err != nil {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, err.Error())
		return nil, "", false
	}
	rec, ok := s.live(w, r)
	if !ok {
		return nil, "", false
	}
	return rec, path.Clean(p), true
}

func (s *Server) readFile(w http.ResponseWriter, r *http.Request) {
	rec, p, ok := s.filePath(w, r)
	if !ok {
		return
	}
	data, err := s.Backend.ReadFile(r.Context(), rec.snapshot().ID, p)
	if err != nil {
		writeBackendErr(w, err)
		return
	}
	s.event(api.Event{Type: api.EventFileRead, Sandbox: rec.snapshot().ID, Path: p, Bytes: int64(len(data))})
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(data)
}

func (s *Server) writeFile(w http.ResponseWriter, r *http.Request) {
	rec, p, ok := s.filePath(w, r)
	if !ok {
		return
	}
	data, err := io.ReadAll(r.Body)
	if err != nil {
		writeErr(w, http.StatusRequestEntityTooLarge, api.CodeInvalidRequest, "file body too large or unreadable")
		return
	}
	if err := s.Backend.WriteFile(r.Context(), rec.snapshot().ID, p, data); err != nil {
		writeBackendErr(w, err)
		return
	}
	s.event(api.Event{Type: api.EventFileWritten, Sandbox: rec.snapshot().ID, Path: p, Bytes: int64(len(data))})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) removeFile(w http.ResponseWriter, r *http.Request) {
	rec, p, ok := s.filePath(w, r)
	if !ok {
		return
	}
	if err := s.Backend.Remove(r.Context(), rec.snapshot().ID, p); err != nil {
		writeBackendErr(w, err)
		return
	}
	s.event(api.Event{Type: api.EventFileRemoved, Sandbox: rec.snapshot().ID, Path: p})
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) listDir(w http.ResponseWriter, r *http.Request) {
	rec, p, ok := s.filePath(w, r)
	if !ok {
		return
	}
	entries, err := s.Backend.ListDir(r.Context(), rec.snapshot().ID, p)
	if err != nil {
		writeBackendErr(w, err)
		return
	}
	writeJSON(w, http.StatusOK, api.DirList{Entries: entries})
}

// --- plumbing ----------------------------------------------------------------

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "request body: "+err.Error())
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, api.ErrorBody{Error: api.ErrorDetail{Code: code, Message: msg}})
}

func writeSpecErr(w http.ResponseWriter, err error) {
	var ref *spec.Refused
	var inv *spec.Invalid
	switch {
	case errors.As(err, &ref):
		writeErr(w, http.StatusForbidden, api.CodeRefused, ref.Msg)
	case errors.As(err, &inv):
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, inv.Msg)
	default:
		writeErr(w, http.StatusInternalServerError, api.CodeInternal, "internal error")
	}
}

func writeBackendErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, backend.ErrNotFound):
		writeErr(w, http.StatusNotFound, api.CodeNotFound, "not found")
	case errors.Is(err, backend.ErrIsDir):
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "is a directory")
	case errors.Is(err, backend.ErrNotDir):
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "not a directory")
	case errors.Is(err, backend.ErrNotEmpty):
		writeErr(w, http.StatusConflict, api.CodeConflict, "directory not empty")
	case errors.Is(err, backend.ErrReadOnly):
		writeErr(w, http.StatusConflict, api.CodeConflict, "read-only file system")
	case errors.Is(err, backend.ErrNoSuchCmd):
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "no such command")
	case errors.Is(err, backend.ErrNoSuchCwd):
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "cwd: no such directory")
	case errors.Is(err, backend.ErrBadSignal):
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "unsupported signal")
	case errors.Is(err, backend.ErrBusy):
		writeErr(w, http.StatusConflict, api.CodeConflict, "the sandbox is busy")
	case errors.Is(err, backend.ErrUnsupported):
		writeErr(w, http.StatusNotImplemented, api.CodeUnsupported, err.Error())
	default:
		// The message is generic on purpose: a backend error can carry host paths
		// and engine detail that are the operator's to read, not the caller's.
		writeErr(w, http.StatusInternalServerError, api.CodeInternal, "the backend failed")
	}
}

func sortedKeys(m map[string]string) []string {
	if len(m) == 0 {
		return nil
	}
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

// processStarted is the audit record of a process: what ran and with how many
// arguments, never the arguments themselves (api.Event says why).
func processStarted(id string, pid int, ps backend.ProcSpec, envNames []string) api.Event {
	ev := api.Event{Type: api.EventProcessStarted, Sandbox: id, PID: pid, Cwd: ps.Cwd, EnvNames: envNames}
	if len(ps.Argv) == 0 {
		return ev
	}
	ev.Program = ps.Argv[0]
	if args := ps.Argv[1:]; len(args) > 0 {
		h := sha256.New()
		for _, a := range args {
			h.Write([]byte(a))
			h.Write([]byte{0})
		}
		ev.ArgCount, ev.ArgsSHA256 = len(args), hex.EncodeToString(h.Sum(nil))
	}
	return ev
}

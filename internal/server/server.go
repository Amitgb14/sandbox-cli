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

	now func() time.Time

	mu        sync.Mutex
	sandboxes map[string]*record // id -> record
}

type record struct {
	mu      sync.Mutex
	sbx     api.Sandbox
	env     map[string]string
	procs   map[int]*procRecord
	nextPID int
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
	route("POST /v1/sandboxes", false, s.createSandbox)
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
	route("GET /v1/sandboxes/{ref}/files", false, s.readFile)
	route("PUT /v1/sandboxes/{ref}/files", true, s.writeFile)
	route("DELETE /v1/sandboxes/{ref}/files", false, s.removeFile)
	route("GET /v1/sandboxes/{ref}/dirs", false, s.listDir)
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
	caps := map[string]bool{
		api.CapNetworkPolicyUpdate: false, api.CapSuspend: false,
		api.CapMemorySnapshot: false, api.CapBindWorkspace: false,
	}
	for k, v := range s.Backend.Capabilities() {
		caps[k] = v
	}
	writeJSON(w, http.StatusOK, api.Capabilities{
		APIVersion:   api.Version,
		Backend:      s.Backend.Name(),
		Capabilities: caps,
		Limits:       s.Policy.Limits,
		Network:      s.Policy.Ceiling(),
	})
}

func (s *Server) createSandbox(w http.ResponseWriter, r *http.Request) {
	var req api.CreateSandboxRequest
	if !decode(w, r, &req) {
		return
	}
	id := spec.NewID()
	bs, err := spec.Resolve(req, s.Policy, id)
	if err != nil {
		writeSpecErr(w, err)
		return
	}

	// The name is claimed before the backend is asked, under the lock, so two
	// concurrent creates with one name cannot both pass a check-then-create.
	rec := &record{
		sbx: api.Sandbox{
			ID: id, Name: req.Name, State: api.StatePending, Image: bs.Image,
			CPUs: bs.CPUs, MemoryMB: bs.MemoryMB, DiskMB: bs.DiskMB,
			EnvNames: sortedKeys(bs.Env), Network: bs.Network, CreatedAt: s.now().UTC(),
		},
		env:   bs.Env,
		procs: map[int]*procRecord{},
	}
	s.mu.Lock()
	if req.Name != "" {
		for _, o := range s.sandboxes {
			if o.snapshot().Name == req.Name && o.snapshot().State != api.StateTerminated {
				s.mu.Unlock()
				writeErr(w, http.StatusConflict, api.CodeConflict, "a sandbox named "+req.Name+" already exists")
				return
			}
		}
	}
	s.sandboxes[id] = rec
	s.mu.Unlock()

	if err := s.Backend.Create(r.Context(), bs); err != nil {
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
	writeJSON(w, http.StatusCreated, out)
}

func (r *record) snapshot() api.Sandbox {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.sbx
}

func (s *Server) listSandboxes(w http.ResponseWriter, r *http.Request) {
	s.mu.Lock()
	list := make([]api.Sandbox, 0, len(s.sandboxes))
	for _, rec := range s.sandboxes {
		list = append(list, rec.snapshot())
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
	id := rec.snapshot().ID
	if err := s.Backend.UpdateNetwork(r.Context(), id, pol); err != nil {
		writeBackendErr(w, err)
		return
	}
	rec.mu.Lock()
	rec.sbx.Network = pol
	out := rec.sbx
	rec.mu.Unlock()
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
	ps := backend.ProcSpec{Argv: append([]string(nil), req.Argv...), Env: env, Cwd: req.Cwd}
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
		info: api.Process{PID: rec.nextPID, Argv: ps.Argv, State: api.ProcessRunning, StartedAt: s.now().UTC()},
		proc: proc,
		log:  log,
	}
	rec.procs[pr.info.PID] = pr
	rec.mu.Unlock()

	go func() {
		code := proc.Wait()
		rec.mu.Lock()
		pr.info.State = api.ProcessExited
		pr.info.ExitCode = &code
		rec.mu.Unlock()
		log.finish(code)
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
	case errors.Is(err, backend.ErrNoSuchCmd):
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "no such command")
	case errors.Is(err, backend.ErrBadSignal):
		writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "unsupported signal")
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

// Package studio is the local server behind `sandbox-cli studio`: it serves
// the browser UI and a small API, and holds the one thing the browser must not
// — the token of the sandboxd the current context points at.
//
// It owns no sandbox logic. Sandbox calls are proxied to sandboxd under
// /api/v1, so every isolation rule is the server's, unchanged. What it adds is
// what only the machine with your repository can do — launching a run on a
// clone of it, bringing work back, recovering, landing a fleet — and for those
// it calls the same code the CLI does (a Launcher the CLI supplies, and the
// workspace and fleet packages directly).
//
// The question beta.15's studioapi had to answer is the same one here: who may
// ask this process to act? It listens on loopback only; it answers only Host
// headers naming loopback (so a page whose own name resolves to 127.0.0.1 is
// refused — DNS rebinding); it refuses a cross-origin Origin; and every /api
// request needs the token printed in the URL Studio opens with, because a
// loopback port is reachable by every user on the machine.
package studio

import (
	"context"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httputil"
	"strconv"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// LaunchRequest is a run started from Studio. It becomes the same request a
// detached `sandbox-cli run` or `sandbox-cli agent` makes.
type LaunchRequest struct {
	Repo string `json:"repo"` // a registered repository's id
	// Agent, when set, runs that agent: headless with Prompt, or — Console —
	// interactive on a terminal the browser attaches to. Otherwise Command.
	Agent   string   `json:"agent,omitempty"`
	Prompt  string   `json:"prompt,omitempty"`
	Console bool     `json:"console,omitempty"`
	Command []string `json:"command,omitempty"`

	Name    string            `json:"name,omitempty"`
	Network string            `json:"network,omitempty"` // "", none, allowlist, open
	Allow   []string          `json:"allow,omitempty"`
	Labels  map[string]string `json:"labels,omitempty"`
	Volumes []api.VolumeMount `json:"volumes,omitempty"`
	// Git makes the run's commits with your own git identity (--git).
	Git     bool   `json:"git,omitempty"`
	Profile string `json:"profile,omitempty"`
	// Rows and Cols size a console run's terminal before anyone attaches.
	Rows uint16 `json:"rows,omitempty"`
	Cols uint16 `json:"cols,omitempty"`
}

// LaunchResult is what a launch started.
type LaunchResult struct {
	Sandbox string `json:"sandbox"`
	PID     int    `json:"pid"`
}

// Launcher starts a run on a repository at path; the CLI supplies it.
type Launcher func(ctx context.Context, repoPath string, req LaunchRequest) (LaunchResult, error)

// Server is one Studio.
type Server struct {
	Client  *api.Client // the current context's sandboxd
	Context string      // its name, for display
	Token   string      // required on /api; generated per launch
	UI      fs.FS       // the built UI; nil serves a page saying how to build it
	Launch  Launcher
	// ReposFile is where registered repositories are kept.
	ReposFile string
	Version   string
	Logf      func(format string, a ...any)
}

// Handler is everything Studio serves.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	api := func(pattern string, h http.HandlerFunc) { mux.Handle(pattern, s.guard(h)) }

	api("GET /api/info", s.info)
	api("/api/v1/", s.proxy())
	api("GET /api/ws/attach", s.attach)
	api("GET /api/repos", s.listRepos)
	api("POST /api/repos", s.addRepo)
	api("DELETE /api/repos/{id}", s.removeRepo)
	api("GET /api/repos/{id}/refs", s.refs)
	api("GET /api/repos/{id}/diff", s.diff)
	api("GET /api/repos/{id}/fleet", s.fleetState)
	api("POST /api/repos/{id}/fleet/land", s.fleetLand)
	api("GET /api/runs", s.listRuns)
	api("POST /api/runs", s.launch)
	api("POST /api/runs/{sandbox}/bring-back", s.bringBack)
	api("DELETE /api/runs/{sandbox}", s.forgetRun)
	api("GET /api/agents", s.agents)
	mux.Handle("/api/", s.guard(func(w http.ResponseWriter, r *http.Request) {
		writeErr(w, http.StatusNotFound, "no such endpoint")
	}))
	mux.Handle("/", s.hostOnly(s.ui()))
	return mux
}

// hostOnly refuses a Host that does not name loopback. Everything gets it,
// the UI included: a rebinding page that could read the UI could read the
// token out of a URL a user pasted into it.
func (s *Server) hostOnly(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !loopbackHost(r.Host) {
			writeErr(w, http.StatusForbidden, "Studio answers loopback names only")
			return
		}
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(w, r)
	})
}

// guard applies, in order: Host, Origin, the token, and for a request with a
// body, a JSON content type — which a cross-origin "simple request" cannot
// send without a preflight this server never answers.
func (s *Server) guard(next http.HandlerFunc) http.Handler {
	return s.hostOnly(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if o := r.Header.Get("Origin"); o != "" && o != "http://"+r.Host {
			writeErr(w, http.StatusForbidden, "cross-origin requests are refused")
			return
		}
		if !s.authorized(r) {
			writeErr(w, http.StatusUnauthorized, "missing or wrong Studio token")
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead && r.Method != http.MethodDelete &&
			r.ContentLength != 0 && !strings.HasPrefix(r.Header.Get("Content-Type"), "application/json") &&
			!strings.HasPrefix(r.URL.Path, "/api/v1/") {
			writeErr(w, http.StatusUnsupportedMediaType, "want Content-Type: application/json")
			return
		}
		next(w, r)
	}))
}

// authorized checks the token: from the Authorization header, or — for a
// WebSocket, which a browser cannot give headers — the token query parameter.
func (s *Server) authorized(r *http.Request) bool {
	got := ""
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer ") {
		got = strings.TrimPrefix(h, "Bearer ")
	} else if isWebSocketUpgrade(r) {
		got = r.URL.Query().Get("token")
	}
	return s.Token != "" && subtle.ConstantTimeCompare([]byte(got), []byte(s.Token)) == 1
}

func loopbackHost(hostport string) bool {
	host := hostport
	if h, _, err := net.SplitHostPort(hostport); err == nil {
		host = h
	}
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(strings.Trim(host, "[]"))
	return ip != nil && ip.IsLoopback()
}

// proxy forwards /api/v1/... to sandboxd's /v1/..., replacing the browser's
// Studio token with the context's own. An upgrade is refused here: a browser
// cannot speak the API's stream protocols, and /api/ws/attach is the bridge.
func (s *Server) proxy() http.HandlerFunc {
	base, rt, token := s.Client.Transport()
	rp := &httputil.ReverseProxy{
		Transport: rt,
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme, pr.Out.URL.Host = base.Scheme, base.Host
			pr.Out.URL.Path = strings.TrimPrefix(pr.In.URL.Path, "/api")
			pr.Out.URL.RawPath = ""
			pr.Out.Host = base.Host
			pr.Out.Header.Del("Authorization")
			pr.Out.Header.Del("Origin")
			pr.Out.Header.Del("Cookie")
			if token != "" {
				pr.Out.Header.Set("Authorization", "Bearer "+token)
			}
		},
		ErrorHandler: func(w http.ResponseWriter, _ *http.Request, err error) {
			writeErr(w, http.StatusBadGateway, "sandboxd did not answer: "+err.Error())
		},
	}
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "" {
			writeErr(w, http.StatusBadRequest, "streams go through /api/ws/attach")
			return
		}
		rp.ServeHTTP(w, r)
	}
}

func (s *Server) info(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	out := map[string]any{"context": s.Context, "version": s.Version}
	if caps, err := s.Client.Capabilities(ctx); err == nil {
		out["capabilities"] = caps
	} else {
		out["error"] = err.Error()
	}
	writeJSON(w, http.StatusOK, out)
}

// attach bridges a browser WebSocket to a process's attach stream. Browser to
// server, text frames are JSON: {"type":"input","data":"…"},
// {"type":"resize","rows":R,"cols":C}, {"type":"signal","signal":"INT"}.
// Server to browser: {"type":"output","data":"…"} and, last,
// {"type":"exit","code":N}. Closing the tab detaches; the process runs on.
func (s *Server) attach(w http.ResponseWriter, r *http.Request) {
	if !isWebSocketUpgrade(r) {
		writeErr(w, http.StatusBadRequest, "want a WebSocket upgrade")
		return
	}
	ref := r.URL.Query().Get("sandbox")
	pid := 0
	if p := r.URL.Query().Get("pid"); p != "" {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 {
			writeErr(w, http.StatusBadRequest, "pid: a positive number")
			return
		}
		pid = n
	}
	ctx, cancel := context.WithCancel(context.WithoutCancel(r.Context()))
	defer cancel()
	if pid == 0 {
		ps, err := s.Client.Processes(ctx, ref)
		if err != nil {
			writeErr(w, http.StatusNotFound, err.Error())
			return
		}
		for _, p := range ps {
			if p.State == api.ProcessRunning && (pid == 0 || p.Tty) {
				pid = p.PID
			}
		}
		if pid == 0 {
			writeErr(w, http.StatusConflict, "no running process to attach to")
			return
		}
	}
	stream, err := s.Client.Attach(ctx, ref, pid)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	defer stream.Close()
	ws, err := upgradeWebSocket(w, r)
	if err != nil {
		if !errors.Is(err, errUpgradeAborted) {
			writeErr(w, http.StatusBadRequest, err.Error())
		}
		return
	}
	defer ws.close(wsCloseNormal, "")
	go ws.keepalive(ctx)
	go ws.readLoop(func(_ byte, payload []byte) {
		var m struct {
			Type   string `json:"type"`
			Data   string `json:"data"`
			Rows   uint16 `json:"rows"`
			Cols   uint16 `json:"cols"`
			Signal string `json:"signal"`
		}
		if json.Unmarshal(payload, &m) != nil {
			return
		}
		switch m.Type {
		case "input":
			_, _ = stream.Write([]byte(m.Data))
		case "resize":
			if m.Rows > 0 && m.Cols > 0 {
				_ = stream.Resize(m.Rows, m.Cols)
			}
		case "signal":
			_ = stream.Signal(m.Signal)
		}
	}, cancel)

	out := wsWriter{ws}
	code, err := stream.Copy(out, out)
	if err == nil {
		_ = ws.writeJSON(map[string]any{"type": "exit", "code": code})
	}
}

// wsWriter sends a process's output as output messages. Bytes travel as a
// JSON string; a terminal emulator handles what is not valid UTF-8 itself, and
// the replacement it sees is the same one a JSON encoder makes.
type wsWriter struct{ ws *wsConn }

func (w wsWriter) Write(p []byte) (int, error) {
	if err := w.ws.writeJSON(map[string]string{"type": "output", "data": string(p)}); err != nil {
		return 0, err
	}
	return len(p), nil
}

// --- responses ------------------------------------------------------------------

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"message": msg}})
}

func decode(w http.ResponseWriter, r *http.Request, v any) bool {
	dec := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeErr(w, http.StatusBadRequest, "request body: "+err.Error())
		return false
	}
	return true
}

func (s *Server) logf(format string, a ...any) {
	if s.Logf != nil {
		s.Logf(format, a...)
	}
}

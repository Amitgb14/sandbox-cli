package studioapi

import (
	"context"
	"net/http"
	"os/exec"
	goruntime "runtime"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/version"
)

// hostSizer is the part of a runtime backend that can say how much memory the
// machine has. Declared here rather than imported: it is three lines, Go
// interfaces are structural, and internal/fleet declares its own for the same
// reason — the capacity check and this endpoint ask the same question of the
// same backend without either package having to know about the other.
type hostSizer interface {
	HostMemoryBytes(ctx context.Context) (int64, bool)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	available := s.dockerAvailable(r.Context())
	status := "ok"
	if !available {
		status = "degraded"
	}
	profile := s.Session.Cfg.Profile
	if profile == "" {
		profile = "dev"
	}
	writeJSON(w, http.StatusOK, HealthResponse{
		Status:          status,
		Version:         version.Version,
		Engine:          s.Engine,
		EngineVersion:   s.engineVersion(r.Context()),
		DockerAvailable: available,
		Project:         s.Project,
		Profile:         profile,
		AuthRequired:    s.Token != "",
		Egress:          s.egressPosture(),
		Host:            s.hostInfo(r.Context()),
	})
}

// engineVersion asks the engine what it is, and answers "" when it will not say.
//
// Empty rather than "unknown": absent is not a value, and a client printing a
// placeholder as though the daemon had reported one is saying something false.
// The same bargain internal/agentusage makes with a cache it cannot parse.
func (s *Server) engineVersion(ctx context.Context) string {
	out, err := exec.CommandContext(ctx, s.Engine, "version", "--format", "{{.Server.Version}}").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// hostInfo reports the machine this daemon runs on.
//
// OS, arch and CPU count come from the Go runtime — this process, so always
// answerable. Memory comes from the *engine* rather than the host: on macOS the
// daemon runs in a VM whose budget, not the laptop's RAM, is what bounds a
// container, which is the same reason fleet's capacity check asks the engine
// instead of reading /proc. An engine that will not say leaves 0, and 0 is
// honest — a client must not read it as "no memory".
func (s *Server) hostInfo(ctx context.Context) HostInfo {
	h := HostInfo{
		OS:   goruntime.GOOS,
		Arch: goruntime.GOARCH,
		CPUs: goruntime.NumCPU(),
	}
	if sizer, ok := s.RT.(hostSizer); ok {
		if b, ok := sizer.HostMemoryBytes(ctx); ok && b > 0 {
			h.MemBytes = b
		}
	}
	return h
}

// egressPosture is the network posture this daemon launches with.
//
// From the resolved config rather than from a request, because that is the only
// place it can come from: a launch may add domains and may never loosen the
// mode, so what a client needs is not a control but an answer.
func (s *Server) egressPosture() EgressPosture {
	n := s.Session.Cfg.Network
	mode := n.Mode
	if mode == "" {
		mode = "default"
	}
	p := EgressPosture{Mode: mode, Baseline: n.BaselineEnabled()}
	if mode == "allowlist" {
		p.Allow = n.EgressDomains()
	}
	return p
}

package studioapi

import (
	"net/http"

	"github.com/Amitgb14/sandbox-cli/internal/version"
)

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
		DockerAvailable: available,
		Project:         s.Project,
		Profile:         profile,
	})
}

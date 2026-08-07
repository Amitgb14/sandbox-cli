package studioapi

import (
	"context"
	"net/http"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/doctor"
)

// handleDoctor runs the same host preflight `sandbox-cli doctor` runs.
//
// The same, not an equivalent: both call doctor.RunChecks, so the terminal and
// this endpoint cannot disagree about whether seccomp is applied on the machine
// they are both looking at. A second implementation here would have been the
// easier change and the wrong one — a control plane that reports a healthier
// host than the CLI does is worse than one that reports nothing.
//
// Each check carries what it costs under *both* profiles rather than only the
// one in force. That is the whole point of the dev/prod asymmetry: the same
// fact is a warning under dev and a refusal under prod, so a reader deciding
// whether this host is ready for unattended work needs to see both answers
// without switching profiles and asking again.
func (s *Server) handleDoctor(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), doctor.Timeout)
	defer cancel()

	profile := s.Session.Cfg.Profile
	if profile == "" {
		profile = config.ProfileDev
	}

	checks := doctor.RunChecks(ctx, profile, s.Engine, s.Session.Cfg.Runtime)
	out := make([]DoctorCheck, 0, len(checks))
	for _, c := range checks {
		_, failsDev := doctor.Verdict(c.Status, false)
		_, failsProd := doctor.Verdict(c.Status, true)
		out = append(out, DoctorCheck{
			ID:        checkID(c.Name),
			Title:     c.Name,
			Result:    c.Status.String(),
			Detail:    c.Detail,
			Remedy:    c.Remedy,
			UnderDev:  cost(failsDev),
			UnderProd: cost(failsProd),
		})
	}
	writeJSON(w, http.StatusOK, DoctorResponse{Profile: profile, Checks: out})
}

// checkID turns a display name into a stable key a client can switch on, so the
// UI is not matching against prose that may be reworded.
func checkID(name string) string {
	id := make([]rune, 0, len(name))
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			id = append(id, r)
		case r >= 'A' && r <= 'Z':
			id = append(id, r+('a'-'A'))
		default:
			id = append(id, '-')
		}
	}
	return string(id)
}

func cost(fatal bool) string {
	if fatal {
		return "fail"
	}
	return "warn"
}

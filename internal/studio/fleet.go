package studio

import (
	"errors"
	"net/http"
	"os"

	"github.com/Amitgb14/sandbox-cli/internal/fleet"
)

// A fleet is started from the CLI (`sandbox-cli agent fleet run`), which owns
// its terminal and its timing; Studio shows its record and lands it, through
// the same fleet.Land the CLI uses and every refusal that comes with it.

func (s *Server) fleetState(w http.ResponseWriter, r *http.Request) {
	rp, ok := s.repo(w, r)
	if !ok {
		return
	}
	st, err := fleet.LoadState(rp.Path)
	if errors.Is(err, os.ErrNotExist) {
		writeErr(w, http.StatusNotFound, "no fleet run recorded for this repository")
		return
	}
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) fleetLand(w http.ResponseWriter, r *http.Request) {
	rp, ok := s.repo(w, r)
	if !ok {
		return
	}
	var req struct {
		Branch     string `json:"branch,omitempty"`
		All        bool   `json:"all,omitempty"`
		Unverified bool   `json:"unverified,omitempty"`
		Onto       string `json:"onto,omitempty"`
	}
	if !decode(w, r, &req) {
		return
	}
	if req.All == (req.Branch != "") {
		writeErr(w, http.StatusBadRequest, "name a branch, or all")
		return
	}
	st, err := fleet.LoadState(rp.Path)
	if err != nil {
		writeErr(w, http.StatusNotFound, "no fleet run recorded for this repository")
		return
	}
	opts := fleet.LandOptions{Unverified: req.Unverified, Onto: req.Onto}
	if req.All {
		landed, skipped, err := fleet.LandAll(st, opts)
		out := map[string]any{"landed": landed, "skipped": skipped}
		if err != nil {
			out["error"] = err.Error()
			writeJSON(w, http.StatusConflict, out)
			return
		}
		writeJSON(w, http.StatusOK, out)
		return
	}
	if err := fleet.Land(st, req.Branch, opts); err != nil {
		writeErr(w, http.StatusConflict, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"landed": []string{req.Branch}})
}

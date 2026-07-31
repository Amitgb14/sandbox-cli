package studioapi

import (
	"bufio"
	"bytes"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/Amitgb14/sandbox-cli/internal/config"
)

// auditLine is the record as internal/audit writes it — snake_case, and
// deliberately not the wire shape. The two are mapped rather than shared so the
// log's format on disk can change without breaking clients, and so this file
// documents in one place that the *only* environment data in an audit record is
// a list of names.
type auditLine struct {
	Time        string   `json:"time"`
	Image       string   `json:"image"`
	Workspace   string   `json:"workspace"`
	Workdir     string   `json:"workdir"`
	Agent       string   `json:"agent"`
	Branch      string   `json:"branch"`
	Command     []string `json:"command"`
	Engine      string   `json:"engine"`
	Network     string   `json:"network"`
	NetworkName string   `json:"network_name"`
	EnforcedBy  string   `json:"enforced_by"`
	EgressAllow []string `json:"egress_allow"`
	EnvNames    []string `json:"env_names"`
	ExitCode    int      `json:"exit_code"`
	DurationMS  int64    `json:"duration_ms"`
	Detached    bool     `json:"detached"`
}

// defaultAuditLimit bounds a listing. The log is append-only across every
// project on the machine and rotates rather than truncates, so "all of it" is
// unbounded by design; a client that wants more asks for more.
const defaultAuditLimit = 200

// handleAudit is GET /v1/audit: the run log, newest first.
//
// One rule travels with this data and is not negotiable: environment variables
// appear **by name only**. The credential broker exists to keep secret values
// off the argv and out of config files, and audit.SessionMeta has nowhere to put
// a value on purpose — so neither does this. A response is one more file, and it
// lands in a browser's cache.
//
// A missing log is an empty list rather than a 404: a machine where no run has
// finished yet is new, not broken.
func (s *Server) handleAudit(w http.ResponseWriter, r *http.Request) {
	limit := defaultAuditLimit
	if v, err := strconv.Atoi(r.URL.Query().Get("limit")); err == nil && v > 0 {
		limit = v
	}
	// Filtering here rather than in the client because the log spans every
	// project on the machine and rotates rather than truncates: asking for one
	// branch's history should not mean shipping everyone else's.
	branch := r.URL.Query().Get("branch")

	out := make([]AuditRecord, 0, limit)
	dir := config.AuditDir()
	if dir == "" {
		writeJSON(w, http.StatusOK, AuditResponse{Records: out})
		return
	}

	f, err := os.Open(filepath.Join(dir, "sessions.jsonl"))
	if err != nil {
		writeJSON(w, http.StatusOK, AuditResponse{Records: out})
		return
	}
	defer f.Close()

	// Read the lot, then take the tail: the file is newline-delimited with no
	// index, so "the last N" cannot be found without walking it. Bounded by the
	// sink's own rotation rather than by hope.
	var all []AuditRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		var a auditLine
		if branch != "" && !bytes.Contains(line, []byte(`"branch":`)) {
			continue // cheap reject before parsing: most lines have no branch at all
		}
		if err := json.Unmarshal(line, &a); err != nil {
			// A line this no longer understands is skipped, not fatal — the same
			// bargain agentctx makes with a transcript whose shape it cannot read.
			// One unparseable record must not blank the log.
			continue
		}
		if branch != "" && a.Branch != branch {
			continue
		}
		all = append(all, a.toRecord())
	}

	// Newest first, which is the order every other listing in this tool uses.
	for i := len(all) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, all[i])
	}
	writeJSON(w, http.StatusOK, AuditResponse{Records: out})
}

func (a auditLine) toRecord() AuditRecord {
	rec := AuditRecord{
		Time:        a.Time,
		Image:       a.Image,
		Workspace:   a.Workspace,
		Workdir:     a.Workdir,
		Command:     a.Command,
		Engine:      a.Engine,
		Network:     a.Network,
		NetworkName: a.NetworkName,
		EgressAllow: a.EgressAllow,
		EnvNames:    a.EnvNames,
		ExitCode:    a.ExitCode,
		DurationMS:  a.DurationMS,
		Detached:    a.Detached,
	}
	// Absent is null, not "": a run with no agent is a plain `run`, which is a
	// different thing from an agent whose name went unrecorded.
	if a.Agent != "" {
		agent := a.Agent
		rec.Agent = &agent
	}
	if a.Branch != "" {
		branch := a.Branch
		rec.Branch = &branch
	}
	if a.EnforcedBy != "" {
		by := a.EnforcedBy
		rec.EgressEnforcementRequested = &by
	}
	if rec.Command == nil {
		rec.Command = []string{}
	}
	if rec.EgressAllow == nil {
		rec.EgressAllow = []string{}
	}
	if rec.EnvNames == nil {
		rec.EnvNames = []string{}
	}
	return rec
}

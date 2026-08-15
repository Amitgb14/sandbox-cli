package studioapi

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/audit"
	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/history"
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
	// Which repository, for the screens that scope to one. Derived rather than
	// read: see repoIDForWorkspace. An unregistered id is not refused here the
	// way it is elsewhere — this is a filter over history, and history contains
	// runs from repositories nobody registered; asking about one of those should
	// answer "no records", not "no such repository".
	repo := r.URL.Query().Get("repo")
	projects := s.projects()

	out := make([]AuditRecord, 0, limit)
	dir := config.AuditDir()
	if dir == "" {
		writeJSON(w, http.StatusOK, AuditResponse{Records: out})
		return
	}

	// The index answers this when it exists, and the scan when it does not. The
	// two must agree: TestIndexedAndScannedAuditAgree asserts it over the same
	// log, because an index that quietly answers a different question is worse
	// than no index — nothing would ever notice.
	if s.History != nil {
		// The index cannot filter by repository — it has no such column, since
		// the log it indexes has no such field — so a repo-scoped request asks
		// for more rows than it needs and drops the ones that do not match. It
		// can therefore return fewer than `limit`; that is a smaller lie than
		// silently mixing in another repository's runs, which is the thing the
		// filter exists to prevent.
		want := limit
		if repo != "" {
			want = limit * auditRepoOverscan
		}
		recs, err := s.History.Runs(history.Filter{Branch: branch, Limit: want})
		if err == nil {
			for _, rec := range recs {
				r := fromHistory(rec)
				r.RepoID = repoIDForWorkspace(r.Workspace, projects)
				if repo != "" && r.RepoID != repo {
					continue
				}
				if len(out) >= limit {
					break
				}
				out = append(out, r)
			}
			writeJSON(w, http.StatusOK, AuditResponse{Records: out})
			return
		}
		// A failing index falls back to the file rather than failing the request:
		// the log is the record, and this is a view of it.
		log.Printf("sandbox-studio-api: history index unavailable, scanning the log: %v", err)
	}

	// The history is several files, not one. audit rotates at 8 MiB and keeps
	// generations beside the live log, so a reader that opened only
	// sessions.jsonl would report everything older than the last rotation as
	// though it had never happened — and would do it silently, which is the
	// failure this pairs with.
	//
	// Newest generation first, and each file read newest-record-first, so a
	// bounded request stops as soon as it has what it asked for instead of
	// parsing every generation to throw most of it away.
	for _, path := range audit.Generations(filepath.Join(dir, "sessions.jsonl")) {
		if len(out) >= limit {
			break
		}
		out = append(out, readAuditFile(path, branch, repo, projects, limit-len(out))...)
	}
	writeJSON(w, http.StatusOK, AuditResponse{Records: out})
}

// readAuditFile returns up to limit records from one generation, newest first,
// keeping only those matching branch and repo when either is given.
func readAuditFile(path, branch, repo string, projects []Project, limit int) []AuditRecord {
	f, err := os.Open(path)
	if err != nil {
		// A generation that has been rotated away between listing and opening is
		// not an error: the log is written by every run and read by this, with
		// no lock between them.
		return nil
	}
	defer f.Close()

	var all []AuditRecord
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		if len(line) == 0 {
			continue
		}
		if branch != "" && !bytes.Contains(line, []byte(`"branch":`)) {
			continue // cheap reject before parsing: most lines carry no branch
		}
		var a auditLine
		if err := json.Unmarshal(line, &a); err != nil {
			// A line this no longer understands is skipped, not fatal — the same
			// bargain agentctx makes with a transcript whose shape it cannot read.
			// One unparseable record must not blank the log.
			continue
		}
		if branch != "" && a.Branch != branch {
			continue
		}
		rec := a.toRecord()
		rec.RepoID = repoIDForWorkspace(rec.Workspace, projects)
		if repo != "" && rec.RepoID != repo {
			continue
		}
		all = append(all, rec)
	}

	// The file is append-only, so newest is last. Reverse into the caller's
	// order, which is the newest-first every listing in this tool uses.
	out := make([]AuditRecord, 0, min(limit, len(all)))
	for i := len(all) - 1; i >= 0 && len(out) < limit; i-- {
		out = append(out, all[i])
	}
	return out
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

// fromHistory maps an indexed row to the wire shape.
//
// The same mapping toRecord does from a log line, kept beside it so the two
// cannot drift: an index that returned a different shape from the scan would be
// a second contract nobody agreed to.
func fromHistory(r history.Record) AuditRecord {
	return auditLine{
		Time: r.Time, Image: r.Image, Workspace: r.Workspace, Workdir: r.Workdir,
		Agent: r.Agent, Branch: r.Branch, Command: r.Command, Engine: r.Engine,
		Network: r.Network, NetworkName: r.NetworkName, EnforcedBy: r.EnforcedBy,
		EgressAllow: r.EgressAllow, EnvNames: r.EnvNames,
		ExitCode: r.ExitCode, DurationMS: r.DurationMS, Detached: r.Detached,
	}.toRecord()
}

// handleHistoryStats is GET /v1/stats/history: the outcome summary and day
// buckets, aggregated where the data is.
//
// It exists because the dashboard was fetching five thousand records to draw a
// fourteen-day chart and count a pass rate in the browser. That does not scale,
// and it was already wrong once — the fetch was capped at two hundred, so the
// chart's older columns were empty because nothing had been fetched for them
// rather than because nothing had run.
//
// Requires the index. Without one the client keeps computing this from /v1/audit
// as it did before, which is why the endpoint 501s rather than silently scanning
// the log: an aggregate over a scan is exactly the thing this is here to stop.
func (s *Server) handleHistoryStats(w http.ResponseWriter, r *http.Request) {
	if s.History == nil {
		writeError(w, http.StatusNotImplemented,
			fmt.Errorf("no history index; start the server with -history-db to enable aggregates"))
		return
	}
	days := 14
	if v, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && v > 0 && v <= 365 {
		days = v
	}
	now := time.Now()

	summary, err := s.History.Summary(now)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	buckets, err := s.History.Buckets(days, now)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	writeJSON(w, http.StatusOK, HistoryStatsResponse{Stats: summary, Days: buckets})
}

// auditRepoOverscan is how many extra rows a repo-scoped request asks the index
// for, since the index cannot filter by repository itself. Four is a guess with
// a shape: a machine running several repositories through one Studio is the case
// this exists for, and asking for four times the page is cheap on an index and
// still bounded.
const auditRepoOverscan = 4

// repoIDForWorkspace answers which repository a recorded run belonged to.
//
// **Derived, not recorded** — and that is a compromise worth naming, because
// this codebase's own rule is that a fact not stamped is one no later command
// can recover. The audit log has no repo field: it records the *workspace*, and
// has since before repositories were plural. Stamping one now would leave every
// existing line unfilterable, so a repo-scoped dashboard would show an empty
// history on every machine that has been running sandboxes for months — the
// exact failure mode where a filter looks like an absence.
//
// Two shapes cover what a workspace can be, and both are exact rather than
// heuristic:
//
//   - a managed worktree, `<config>/worktrees/<repoId>/<branch>`, which carries
//     the id in the path because worktreeBase put it there;
//   - a checkout, which is a registered repository's root or something inside it.
//
// A workspace matching neither yields "", meaning "this run belongs to no
// repository this daemon knows about" — which is a true statement about a run in
// a checkout nobody registered, and correctly excludes it from every repo-scoped
// view rather than filing it under the wrong one. If the log ever grows a repo
// id of its own, this becomes the fallback for old lines rather than the answer.
func repoIDForWorkspace(ws string, projects []Project) string {
	if ws == "" {
		return ""
	}
	ws = filepath.Clean(ws)

	if root := config.ConfigRoot(); root != "" {
		base := filepath.Join(root, "worktrees") + string(filepath.Separator)
		if strings.HasPrefix(ws, base) {
			rest := strings.TrimPrefix(ws, base)
			if i := strings.IndexRune(rest, filepath.Separator); i > 0 {
				return rest[:i]
			}
			return rest
		}
	}

	// Longest root first, so a repository nested inside another is matched by
	// itself rather than by its parent.
	best := ""
	for _, p := range projects {
		root := filepath.Clean(p.Root)
		if ws != root && !strings.HasPrefix(ws, root+string(filepath.Separator)) {
			continue
		}
		if len(root) > len(best) {
			best, ws = root, ws
		}
	}
	for _, p := range projects {
		if filepath.Clean(p.Root) == best {
			return p.ID
		}
	}
	return ""
}

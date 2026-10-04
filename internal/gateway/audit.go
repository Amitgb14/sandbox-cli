package gateway

import (
	"bufio"
	"encoding/json"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/audit"
)

// AuditLog is the gateway's record of who did what: every authenticated API
// request and every SSH login and session, one JSON line each
// (api.AuditEntry), in a file written 0600 and rotated as a node's event log
// is (internal/audit: 8 MiB, five old generations).
//
// A node's log says what happened to each sandbox; it cannot say who asked,
// because every request reaches it with the gateway's token. This one can,
// and that is all it says: a credential by its key id, an SSH key by its
// fingerprint, a token login as "token". Never a secret, a request body, a
// query string, a header or an SSH username, any of which may carry one (a
// token login's username is the token).
//
// Best-effort, as the node's: a request is not refused because its record
// could not be written. An operator who needs the opposite should say so;
// it would be a flag.
type AuditLog struct {
	log *audit.Log
	now func() time.Time
}

// NewAuditLog returns the log at path, or nil — which records nothing — for
// an empty path.
func NewAuditLog(path string) *AuditLog {
	if path == "" {
		return nil
	}
	return &AuditLog{log: audit.NewLog(path), now: time.Now}
}

func (a *AuditLog) write(e api.AuditEntry) {
	if a == nil {
		return
	}
	if e.Time.IsZero() {
		e.Time = a.now().UTC()
	}
	a.log.Append(e)
}

// Default and largest page of GET /v1/admin/audit.
const (
	auditDefaultLimit = 100
	auditMaxLimit     = 1000
)

// read returns the newest limit entries at or after since, oldest first,
// across every generation. A line that does not parse is skipped: the file
// is appended to while it is read.
func (a *AuditLog) read(since time.Time, limit int) ([]api.AuditEntry, bool) {
	if a == nil {
		return nil, false
	}
	ring := make([]api.AuditEntry, 0, limit)
	start, truncated := 0, false
	gens := audit.Generations(a.log.Path)
	for i := len(gens) - 1; i >= 0; i-- {
		f, err := os.Open(gens[i])
		if err != nil {
			continue
		}
		br := bufio.NewReader(f)
		for {
			line, rerr := br.ReadBytes('\n')
			var e api.AuditEntry
			if len(line) > 0 && json.Unmarshal(line, &e) == nil && !e.Time.Before(since) {
				if len(ring) < limit {
					ring = append(ring, e)
				} else {
					ring[start] = e
					start = (start + 1) % limit
					truncated = true
				}
			}
			if rerr != nil {
				break
			}
		}
		f.Close()
	}
	return append(ring[start:], ring[:start]...), truncated
}

// adminAudit is GET /v1/admin/audit?since=RFC3339&limit=N.
func (g *Gateway) adminAudit(w http.ResponseWriter, r *http.Request, p Principal) {
	if !need(w, p, ScopeAdmin) {
		return
	}
	if g.audit == nil {
		writeErr(w, http.StatusNotImplemented, api.CodeUnsupported, "this gateway keeps no audit log (sandbox-gateway serve --audit-log)")
		return
	}
	q := r.URL.Query()
	var since time.Time
	if s := q.Get("since"); s != "" {
		t, err := time.Parse(time.RFC3339Nano, s)
		if err != nil {
			writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "since: want an RFC 3339 time")
			return
		}
		since = t
	}
	limit := auditDefaultLimit
	if s := q.Get("limit"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil || n < 1 || n > auditMaxLimit {
			writeErr(w, http.StatusBadRequest, api.CodeInvalidRequest, "limit: a number from 1 to "+strconv.Itoa(auditMaxLimit))
			return
		}
		limit = n
	}
	entries, truncated := g.audit.read(since, limit)
	if entries == nil {
		entries = []api.AuditEntry{}
	}
	writeJSON(w, http.StatusOK, api.AuditList{Entries: entries, Truncated: truncated})
}

// audit records an SSH login or session that went through. The username is
// never written: for a token login it is the token.
func (s *SSHServer) audit(login *sshLogin, action, session string) {
	if s.cfg.Audit == nil {
		return
	}
	node := ""
	if o, ok := s.cfg.Store.OwnerOf(login.id); ok {
		node = o.Node
	}
	p := login.principal
	s.cfg.Audit.write(api.AuditEntry{
		Kind: "ssh", Action: action, KeyID: p.KeyID, User: p.User, Tenant: p.Tenant, Remote: login.remote,
		Sandbox: login.id, Node: node, Result: "ok", Fingerprint: login.fingerprint, Session: session,
	})
}

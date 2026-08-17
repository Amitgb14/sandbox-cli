package history

import (
	"database/sql"
	"strings"
	"time"
)

// Record is one finished run, in the shape internal/audit writes it.
//
// The JSON tags are the *log's* spelling (snake_case), because this type is
// unmarshalled straight from a log line. The API maps it to the wire shape, as
// it did before this package existed — the two stay separate so the log's format
// on disk can change without breaking clients.
type Record struct {
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
	// RunID pairs a detached run's launch line with the line written when it
	// ended; Finished says which half this is. See audit.SessionMeta.
	RunID    string `json:"run_id"`
	Finished bool   `json:"finished"`

	// Routing, when this run was part of an episode. RouteID ties the attempts
	// of one chain together; without it the two lines of a failover are
	// indistinguishable from two unrelated runs.
	RoutedFrom   string `json:"routed_from"`
	RouteReason  string `json:"route_reason"`
	RouteID      string `json:"route_id"`
	RouteAttempt int    `json:"route_attempt"`
}

// Filter narrows a query. A zero Filter means "everything, newest first".
type Filter struct {
	Branch string
	Agent  string
	Since  time.Time
	Limit  int
}

// Runs returns finished runs, newest first.
//
// This must answer exactly what the file-scanning reader answers for the same
// log — that equivalence is what makes the index safe to trust, and it is
// asserted by a test rather than assumed.
func (h *DB) Runs(f Filter) ([]Record, error) {
	var (
		where []string
		args  []any
	)
	if f.Branch != "" {
		where = append(where, "branch = ?")
		args = append(args, f.Branch)
	}
	if f.Agent != "" {
		where = append(where, "agent = ?")
		args = append(args, f.Agent)
	}
	if !f.Since.IsZero() {
		where = append(where, "time >= ?")
		args = append(args, f.Since.UTC().Format(time.RFC3339))
	}

	q := `SELECT time, branch, agent, image, workspace, workdir, engine, network,
	             network_name, enforced_by, egress_allow, env_names, command,
	             exit_code, duration_ms, detached,
	             routed_from, route_reason, route_id, route_attempt,
	             run_id, finished FROM runs`
	if len(where) > 0 {
		q += " WHERE " + strings.Join(where, " AND ")
	}
	// id DESC rather than time DESC as the tiebreak: ids are insertion order,
	// which is the order the runs were written, and the log's timestamps are
	// second-resolution — several runs can share one.
	q += " ORDER BY time DESC, id DESC"
	if f.Limit > 0 {
		q += " LIMIT ?"
		args = append(args, f.Limit)
	}

	rows, err := h.sql.Query(q, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Record, 0, 64)
	for rows.Next() {
		var (
			r                          Record
			branch, agent, enforced    sql.NullString
			egressAllow, envNames, cmd string
			detached                   int
		)
		if err := rows.Scan(&r.Time, &branch, &agent, &r.Image, &r.Workspace, &r.Workdir,
			&r.Engine, &r.Network, &r.NetworkName, &enforced,
			&egressAllow, &envNames, &cmd, &r.ExitCode, &r.DurationMS, &detached,
			&r.RoutedFrom, &r.RouteReason, &r.RouteID, &r.RouteAttempt,
			&r.RunID, &r.Finished); err != nil {
			return nil, err
		}
		r.Branch, r.Agent, r.EnforcedBy = trimNull(branch), trimNull(agent), trimNull(enforced)
		r.EgressAllow, r.EnvNames, r.Command = decodeArray(egressAllow), decodeArray(envNames), decodeArray(cmd)
		r.Detached = detached != 0
		out = append(out, r)
	}
	return out, rows.Err()
}

// DayBucket is one day's runs, split by how they ended.
type DayBucket struct {
	Date         string `json:"date"` // YYYY-MM-DD
	Total        int    `json:"total"`
	Passed       int    `json:"passed"`
	Failed       int    `json:"failed"`
	VerifyFailed int    `json:"verifyFailed"`
	Stopped      int    `json:"stopped"`
}

// Stats is what the whole window says about outcomes.
type Stats struct {
	Total            int      `json:"total"`
	Decided          int      `json:"decided"`
	Passed           int      `json:"passed"`
	PassRate         *float64 `json:"passRate"` // percent; null when nothing decided
	MedianDurationMS *int64   `json:"medianDurationMs"`
	FinishedToday    int      `json:"finishedToday"`
}

// verifyFailedExit is the container exit code meaning "the agent finished and
// the task's own verify said the work is not done" (internal/fleet).
//
// An audit line carries no verify command, so this reads that code alone as a
// failed verify. That is sound rather than convenient: the number was chosen to
// sit above the usual application range and below the shell's reserved
// 126/127/128+n precisely so it could not be confused with something an agent
// produced.
const verifyFailedExit = 90

// outcomeCase is the SQL that classifies an exit code, written once because
// three queries would otherwise each have their own opinion.
const outcomeCase = `CASE
	WHEN exit_code = 0 THEN 'passed'
	WHEN exit_code = 90 THEN 'verify-failed'
	WHEN exit_code IN (137, 143) THEN 'stopped'
	ELSE 'failed' END`

// Buckets returns one row per day over the window, oldest first, with **every**
// day present.
//
// Days with nothing in them are filled in here rather than left out, because a
// chart that skips empty days draws a busy week and a quiet one the same width —
// which is the one thing a volume chart exists to distinguish.
func (h *DB) Buckets(days int, now time.Time) ([]DayBucket, error) {
	if days <= 0 {
		days = 14
	}
	start := now.AddDate(0, 0, -(days - 1))
	start = time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, now.Location())

	rows, err := h.sql.Query(`
		SELECT date(time) AS d,
		       COUNT(*),
		       SUM(CASE WHEN exit_code = 0 THEN 1 ELSE 0 END),
		       SUM(CASE WHEN exit_code = 90 THEN 1 ELSE 0 END),
		       SUM(CASE WHEN exit_code IN (137, 143) THEN 1 ELSE 0 END)
		FROM runs WHERE time >= ? GROUP BY d`, start.UTC().Format(time.RFC3339))
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	found := map[string]DayBucket{}
	for rows.Next() {
		var b DayBucket
		var passed, verifyFailed, stopped int
		if err := rows.Scan(&b.Date, &b.Total, &passed, &verifyFailed, &stopped); err != nil {
			return nil, err
		}
		b.Passed, b.VerifyFailed, b.Stopped = passed, verifyFailed, stopped
		b.Failed = b.Total - passed - verifyFailed - stopped
		found[b.Date] = b
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	out := make([]DayBucket, 0, days)
	for i := 0; i < days; i++ {
		key := start.AddDate(0, 0, i).Format("2006-01-02")
		if b, ok := found[key]; ok {
			out = append(out, b)
			continue
		}
		out = append(out, DayBucket{Date: key})
	}
	return out, nil
}

// Summary aggregates the whole window in SQL rather than shipping every record
// to a client to be counted there.
func (h *DB) Summary(now time.Time) (Stats, error) {
	var s Stats
	err := h.sql.QueryRow(`
		SELECT COUNT(*),
		       SUM(CASE WHEN `+outcomeCase+` IN ('passed','failed','verify-failed') THEN 1 ELSE 0 END),
		       SUM(CASE WHEN exit_code = 0 THEN 1 ELSE 0 END)
		FROM runs`).Scan(&s.Total, &s.Decided, &s.Passed)
	if err != nil {
		return s, err
	}
	if s.Decided > 0 {
		// Percent units, not a fraction: the same as everything else that feeds a
		// percentage formatter, which appends a sign and does not convert.
		rate := float64(s.Passed) / float64(s.Decided) * 100
		s.PassRate = &rate
	}

	dayStart := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	if err := h.sql.QueryRow(`SELECT COUNT(*) FROM runs WHERE time >= ?`,
		dayStart.UTC().Format(time.RFC3339)).Scan(&s.FinishedToday); err != nil {
		return s, err
	}

	// The median, taken in SQL: the middle row of the ordered durations. Runs
	// with no duration recorded are excluded rather than counted as zero — a run
	// that was not measured is not a run that took no time.
	var median sql.NullInt64
	err = h.sql.QueryRow(`
		SELECT duration_ms FROM runs WHERE duration_ms > 0
		ORDER BY duration_ms
		LIMIT 1 OFFSET (SELECT COUNT(*) / 2 FROM runs WHERE duration_ms > 0)`).Scan(&median)
	if err != nil && err != sql.ErrNoRows {
		return s, err
	}
	if median.Valid {
		v := median.Int64
		s.MedianDurationMS = &v
	}
	return s, nil
}

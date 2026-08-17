// Package history is a queryable index over the audit log.
//
// # What it is, and what it deliberately is not
//
// It is a *derived* index. internal/audit's JSONL remains the record: written by
// every run on the path every run already takes, readable with `tail`, and
// dependent on nothing. This database is built from it and can be deleted at any
// moment without losing anything — remove the file and the next start rebuilds
// it.
//
// That single decision is what makes the dependency affordable:
//
//   - Nothing about starting or finishing a run depends on this being writable,
//     present or uncorrupted. internal/audit's bargain is unchanged — the run is
//     what the user asked for, the record is a courtesy.
//   - A schema change is a rebuild, not a migration script.
//   - When the two disagree the file wins, because the database is a function of
//     it.
//   - sandbox-cli does not import this package. Only the Studio API links
//     SQLite, so the CLI keeps its CGO-free cross-compiled build.
//
// It also holds **no live state**. There is no column saying whether a run is
// executing, because docker is the state store and a second answer would be
// wrong the moment a container was killed from another terminal. This indexes
// runs that have *ended*, which is exactly what the audit log records and when.
//
// The driver is modernc.org/sqlite: pure Go, so CGO_ENABLED=0 still holds and
// the release build still cross-compiles without a toolchain per platform.
package history

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// DB is the index.
type DB struct {
	sql  *sql.DB
	path string
}

// schema is applied on every open. Every statement is IF NOT EXISTS, so opening
// an existing database is the same code path as creating one — there is no
// migration story because there is nothing to migrate: a shape change is a
// version bump and a rebuild.
const schema = `
CREATE TABLE IF NOT EXISTS runs (
  id            INTEGER PRIMARY KEY,
  time          TEXT NOT NULL,
  branch        TEXT,
  agent         TEXT,
  image         TEXT NOT NULL DEFAULT '',
  workspace     TEXT NOT NULL DEFAULT '',
  workdir       TEXT NOT NULL DEFAULT '',
  engine        TEXT NOT NULL DEFAULT '',
  network       TEXT NOT NULL DEFAULT '',
  network_name  TEXT NOT NULL DEFAULT '',
  enforced_by   TEXT,
  egress_allow  TEXT NOT NULL DEFAULT '[]',
  env_names     TEXT NOT NULL DEFAULT '[]',
  command       TEXT NOT NULL DEFAULT '[]',
  exit_code     INTEGER NOT NULL DEFAULT 0,
  duration_ms   INTEGER NOT NULL DEFAULT 0,
  detached      INTEGER NOT NULL DEFAULT 0,
  routed_from   TEXT NOT NULL DEFAULT '',
  route_reason  TEXT NOT NULL DEFAULT '',
  route_id      TEXT NOT NULL DEFAULT '',
  route_attempt INTEGER NOT NULL DEFAULT 0,
  -- A detached run is written twice: once at launch, once when it ends. run_id
  -- is what makes the pair one run, and finished says which half this is.
  run_id        TEXT NOT NULL DEFAULT '',
  finished      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX IF NOT EXISTS runs_route ON runs(route_id) WHERE route_id != '';
CREATE INDEX IF NOT EXISTS runs_time   ON runs(time DESC);
CREATE INDEX IF NOT EXISTS runs_branch ON runs(branch, time DESC);
CREATE INDEX IF NOT EXISTS runs_agent  ON runs(agent, time DESC);

-- Where the last sync stopped in the live log, so a resync reads only what has
-- been appended since. One row, id 1.
CREATE TABLE IF NOT EXISTS sync_state (
  id        INTEGER PRIMARY KEY CHECK (id = 1),
  offset    INTEGER NOT NULL,
  schema_v  INTEGER NOT NULL
);
`

// schemaVersion is bumped when the shape changes. A mismatch is a full rebuild,
// which is cheap precisely because the file is the record and this is not.
const schemaVersion = 3

// Open creates or opens the index at path.
func Open(path string) (*DB, error) {
	// _time_format=sqlite keeps timestamps as text we wrote, rather than the
	// driver reinterpreting them; every time here is already RFC3339 from the
	// log, and a format the log did not choose is a format that will not match.
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)")
	if err != nil {
		return nil, err
	}
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("applying schema: %w", err)
	}
	h := &DB{sql: db, path: path}

	var v int
	switch err := db.QueryRow(`SELECT schema_v FROM sync_state WHERE id = 1`).Scan(&v); {
	case err == sql.ErrNoRows:
		_, err = db.Exec(`INSERT INTO sync_state (id, offset, schema_v) VALUES (1, 0, ?)`, schemaVersion)
		if err != nil {
			db.Close()
			return nil, err
		}
	case err != nil:
		db.Close()
		return nil, err
	case v != schemaVersion:
		// Built by a different version of this code. Throw it away rather than
		// reason about what it might contain.
		if err := h.reset(); err != nil {
			db.Close()
			return nil, err
		}
	}
	return h, nil
}

func (h *DB) Close() error { return h.sql.Close() }

// reset empties the index and forgets where it had read to.
func (h *DB) reset() error {
	if _, err := h.sql.Exec(`DELETE FROM runs`); err != nil {
		return err
	}
	_, err := h.sql.Exec(
		`INSERT INTO sync_state (id, offset, schema_v) VALUES (1, 0, ?)
		 ON CONFLICT(id) DO UPDATE SET offset = 0, schema_v = excluded.schema_v`, schemaVersion)
	return err
}

// Sync brings the index up to date with the log.
//
// generations is the log's files, newest first, as audit.Generations returns
// them. live is the first of those — the file still being appended to.
//
// Incremental when it can be: the live file is read from the offset the last
// sync stopped at. A live file *shorter* than that offset means it was rotated
// or truncated underneath us, and the answer there is a full rebuild rather than
// a guess about which lines are new — the whole point of a derived index is that
// rebuilding it is cheap and safe.
func (h *DB) Sync(generations []string) error {
	if len(generations) == 0 {
		return nil
	}
	live := generations[0]

	var offset int64
	if err := h.sql.QueryRow(`SELECT offset FROM sync_state WHERE id = 1`).Scan(&offset); err != nil {
		return err
	}

	fi, err := os.Stat(live)
	if err != nil {
		return err
	}
	if fi.Size() < offset {
		// Rotated or truncated: what we indexed is no longer what is there.
		if err := h.reset(); err != nil {
			return err
		}
		offset = 0
	}

	if offset == 0 {
		// Full build: every generation, oldest first, so ids run in the order the
		// runs happened.
		if err := h.reset(); err != nil {
			return err
		}
		for i := len(generations) - 1; i >= 1; i-- {
			if err := h.ingest(generations[i], 0, nil); err != nil {
				return err
			}
		}
	}

	end := offset
	if err := h.ingest(live, offset, &end); err != nil {
		return err
	}
	_, err = h.sql.Exec(`UPDATE sync_state SET offset = ? WHERE id = 1`, end)
	return err
}

// ingest reads one log file from an offset and inserts what it finds. When end
// is non-nil it is set to the offset reached, which only the live file needs —
// a rotated generation is read whole and never revisited.
func (h *DB) ingest(path string, from int64, end *int64) error {
	f, err := os.Open(path)
	if err != nil {
		// A generation rotated away between listing and opening is not an error:
		// every run writes this log and nothing locks it.
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	defer f.Close()

	if from > 0 {
		if _, err := f.Seek(from, 0); err != nil {
			return err
		}
	}

	tx, err := h.sql.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	stmt, err := tx.Prepare(`INSERT INTO runs
		(time, branch, agent, image, workspace, workdir, engine, network, network_name,
		 enforced_by, egress_allow, env_names, command, exit_code, duration_ms, detached,
		 routed_from, route_reason, route_id, route_attempt, run_id, finished)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()

	read := from
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Bytes()
		read += int64(len(line)) + 1 // the newline the scanner consumed
		if len(line) == 0 {
			continue
		}
		var r Record
		if err := json.Unmarshal(line, &r); err != nil {
			// A line this no longer understands is skipped, not fatal — the same
			// bargain agentctx makes with a transcript whose shape it cannot read.
			continue
		}
		if _, err := stmt.Exec(
			r.Time, nullable(r.Branch), nullable(r.Agent), r.Image, r.Workspace, r.Workdir,
			r.Engine, r.Network, r.NetworkName, nullable(r.EnforcedBy),
			jsonArray(r.EgressAllow), jsonArray(r.EnvNames), jsonArray(r.Command),
			r.ExitCode, r.DurationMS, boolInt(r.Detached),
			r.RoutedFrom, r.RouteReason, r.RouteID, r.RouteAttempt,
			r.RunID, r.Finished,
		); err != nil {
			return err
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if end != nil {
		*end = read
	}
	return nil
}

// Prune drops runs older than a cutoff, and reports how many went.
//
// Retention here is a policy rather than an accident. The log's own rotation
// bounds it by *size*, dropping the oldest generation whole; this bounds the
// index by age, which is the question someone actually has ("keep a quarter").
// They are separate on purpose: the log is the record and this is a view of it.
func (h *DB) Prune(before time.Time) (int64, error) {
	res, err := h.sql.Exec(`DELETE FROM runs WHERE time < ?`, before.UTC().Format(time.RFC3339))
	if err != nil {
		return 0, err
	}
	return res.RowsAffected()
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func jsonArray(v []string) string {
	if len(v) == 0 {
		return "[]"
	}
	b, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func decodeArray(s string) []string {
	if s == "" || s == "[]" {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil {
		return []string{}
	}
	return out
}

// trimNull turns a nullable column into the empty string.
func trimNull(s sql.NullString) string {
	if !s.Valid {
		return ""
	}
	return strings.TrimSpace(s.String)
}

package studioapi

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/audit"
	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/history"
)

// writeLog puts records into the live log, oldest first.
func writeLog(t *testing.T, lines ...string) string {
	t.Helper()
	dir := config.AuditDir()
	if dir == "" {
		t.Skip("no config root in this environment")
	}
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "sessions.jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func line(time, branch, agent string, exit int, durationMS int) string {
	return `{"time":"` + time + `","branch":"` + branch + `","agent":"` + agent +
		`","image":"img","workspace":"/w","workdir":"/workspace","engine":"docker",` +
		`"network":"allowlist","exit_code":` + itoa(exit) + `,"duration_ms":` + itoa(durationMS) + `}`
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		return "-" + string(b)
	}
	return string(b)
}

// The index is only safe to trust if it answers exactly what the scan answers.
// An index that quietly returned something different would be worse than no
// index at all, because nothing would ever notice — so this asks the same
// questions of both and compares.
func TestIndexedAndScannedAuditAgree(t *testing.T) {
	s, _ := newTestServer(t)
	logPath := writeLog(t,
		line("2026-07-01T10:00:00Z", "feature-a", "claude", 0, 1200),
		line("2026-07-02T10:00:00Z", "feature-b", "codex", 90, 900),
		line("2026-07-03T10:00:00Z", "feature-a", "claude", 137, 400),
		line("2026-07-04T10:00:00Z", "feature-a", "", 1, 50),
	)

	// Scanned answers first, with no index attached.
	scanned := map[string][]AuditRecord{}
	for _, q := range []string{"", "?branch=feature-a", "?limit=2", "?branch=feature-b"} {
		rec := doRequest(t, s.Handler(), http.MethodGet, "/v1/audit"+q, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("scan %q = %d: %s", q, rec.Code, rec.Body.String())
		}
		scanned[q] = decodeBody[AuditResponse](t, rec).Records
	}

	h, err := history.Open(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	if err := h.Sync(audit.Generations(logPath)); err != nil {
		t.Fatal(err)
	}
	s.History = h

	for q, want := range scanned {
		rec := doRequest(t, s.Handler(), http.MethodGet, "/v1/audit"+q, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("indexed %q = %d: %s", q, rec.Code, rec.Body.String())
		}
		got := decodeBody[AuditResponse](t, rec).Records
		if len(got) != len(want) {
			t.Fatalf("%q: indexed returned %d records, scan returned %d", q, len(got), len(want))
		}
		for i := range want {
			if got[i].Time != want[i].Time || got[i].ExitCode != want[i].ExitCode ||
				got[i].DurationMS != want[i].DurationMS || got[i].Engine != want[i].Engine {
				t.Errorf("%q record %d: indexed %+v, scan %+v", q, i, got[i], want[i])
			}
			if (got[i].Branch == nil) != (want[i].Branch == nil) {
				t.Errorf("%q record %d: branch nullability differs", q, i)
			}
			// A run with no agent is a plain run — null, not "". The two paths
			// must agree on that or a client sees a different fact per backend.
			if (got[i].Agent == nil) != (want[i].Agent == nil) {
				t.Errorf("%q record %d: agent nullability differs (indexed %v, scan %v)",
					q, i, got[i].Agent, want[i].Agent)
			}
		}
	}
}

// Deleting the index loses nothing: it is a function of the log, and rebuilding
// it twice over the same log must produce the same rows rather than doubling
// them.
func TestIndexRebuildIsIdempotent(t *testing.T) {
	s, _ := newTestServer(t)
	logPath := writeLog(t,
		line("2026-07-01T10:00:00Z", "a", "claude", 0, 100),
		line("2026-07-02T10:00:00Z", "b", "claude", 0, 200),
	)
	_ = s

	path := filepath.Join(t.TempDir(), "history.db")
	count := func() int {
		h, err := history.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer h.Close()
		if err := h.Sync(audit.Generations(logPath)); err != nil {
			t.Fatal(err)
		}
		runs, err := h.Runs(history.Filter{})
		if err != nil {
			t.Fatal(err)
		}
		return len(runs)
	}
	first := count()
	if first != 2 {
		t.Fatalf("want 2 rows, got %d", first)
	}
	if second := count(); second != first {
		t.Errorf("re-syncing the same log changed the row count: %d then %d", first, second)
	}
}

// A log that shrank was rotated or truncated underneath the index. Reading
// forward from the old offset would attribute whatever is now at those bytes to
// runs that never happened, so the answer is a rebuild.
func TestRotationTriggersARebuild(t *testing.T) {
	s, _ := newTestServer(t)
	_ = s
	logPath := writeLog(t,
		line("2026-07-01T10:00:00Z", "a", "claude", 0, 100),
		line("2026-07-02T10:00:00Z", "b", "claude", 0, 200),
		line("2026-07-03T10:00:00Z", "c", "claude", 0, 300),
	)

	path := filepath.Join(t.TempDir(), "history.db")
	h, err := history.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Sync(audit.Generations(logPath)); err != nil {
		t.Fatal(err)
	}
	h.Close()

	// The log is replaced by a shorter one, as a rotation leaves it.
	writeLog(t, line("2026-07-04T10:00:00Z", "d", "claude", 0, 400))

	h2, err := history.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer h2.Close()
	if err := h2.Sync(audit.Generations(logPath)); err != nil {
		t.Fatal(err)
	}
	runs, err := h2.Runs(history.Filter{})
	if err != nil {
		t.Fatal(err)
	}
	if len(runs) != 1 || runs[0].Branch != "d" {
		t.Errorf("a shrunken log must trigger a rebuild, got %d rows: %+v", len(runs), runs)
	}
}

package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestJSONLSinkRecordsTheRunAndNotTheSecrets covers both halves of what this
// package is for: a run leaves a record of what it did and how it ended, and
// that record never contains an environment *value*. The credential broker
// exists to keep secret values off the argv and out of config; writing them to a
// log would hand that straight back.
func TestJSONLSinkRecordsTheRunAndNotTheSecrets(t *testing.T) {
	dir := t.TempDir()
	s := NewJSONLSink(dir)

	s.RecordSession(SessionMeta{
		Image:       "sandbox-base:1",
		Workdir:     "/workspace",
		Workspace:   "/home/me/proj",
		Agent:       "claude",
		Branch:      "feature/x",
		Command:     []string{"claude", "--resume"},
		Network:     "allowlist",
		EgressAllow: []string{"api.anthropic.com", "github.com"},
		EnvNames:    []string{"ANTHROPIC_API_KEY", "TOK"},
		ExitCode:    3,
		Duration:    1500 * time.Millisecond,
	})

	raw, err := os.ReadFile(filepath.Join(dir, "sessions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var got record
	if err := json.Unmarshal([]byte(strings.TrimSpace(string(raw))), &got); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if got.ExitCode != 3 {
		t.Errorf("exit_code = %d, want 3 — the outcome is the point of the record", got.ExitCode)
	}
	if got.DurationMS != 1500 {
		t.Errorf("duration_ms = %d, want 1500", got.DurationMS)
	}
	if got.Network != "allowlist" || len(got.EgressAllow) != 2 {
		t.Errorf("policy not recorded: %+v", got)
	}
	if got.Agent != "claude" || got.Branch != "feature/x" || got.Workspace != "/home/me/proj" {
		t.Errorf("identity not recorded: %+v", got)
	}
	// Names yes, values never — SessionMeta has nowhere to put a value, and that
	// is deliberate.
	if len(got.EnvNames) != 2 || got.EnvNames[0] != "ANTHROPIC_API_KEY" {
		t.Errorf("env names = %v, want the forwarded names", got.EnvNames)
	}

	// Appends rather than truncates: one line per run.
	s.RecordSession(SessionMeta{Image: "sandbox-base:1", ExitCode: 0})
	raw, _ = os.ReadFile(filepath.Join(dir, "sessions.jsonl"))
	if n := len(strings.Split(strings.TrimSpace(string(raw)), "\n")); n != 2 {
		t.Errorf("log has %d lines after two runs, want 2", n)
	}

	// 0600: this names the projects someone worked on and which credentials each
	// run was handed.
	fi, err := os.Stat(filepath.Join(dir, "sessions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Errorf("log mode = %v, want 0600", fi.Mode().Perm())
	}
}

// TestJSONLSinkNeverFailsARun pins the best-effort contract. The run is what the
// user asked for; the record is a courtesy, and a courtesy must not be able to
// break it.
func TestJSONLSinkNeverFailsARun(t *testing.T) {
	if _, ok := NewJSONLSink("").(NopSink); !ok {
		t.Error("no config directory should yield a no-op sink, not a broken one")
	}
	// An unwritable location must be survivable, not panic.
	dir := t.TempDir()
	blocked := filepath.Join(dir, "file-not-a-dir")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	(&JSONLSink{Path: filepath.Join(blocked, "sessions.jsonl")}).RecordSession(SessionMeta{Image: "x"})

	var nilSink *JSONLSink
	nilSink.RecordSession(SessionMeta{Image: "x"})
}

// Rotation used to keep exactly one previous generation, which deleted history
// without saying so: at a few hundred bytes per run, the twelve-thousandth-oldest
// run simply stopped existing. This pins that it now shifts along and drops only
// the last one.
func TestRotationShiftsGenerationsAndDropsOnlyTheOldest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.jsonl")
	sink := &JSONLSink{Path: path}

	// Fill each generation with a marker so a lost or overwritten one is visible
	// as content rather than only as a missing file.
	write := func(p, body string) {
		if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= MaxGenerations; i++ {
		write(generationPath(path, i), "gen"+strconv.Itoa(i)+"\n")
	}
	// The live log, over the threshold so rotation fires.
	write(path, strings.Repeat("x", maxLogBytes+1))

	sink.rotateIfLarge()

	// The live log became .1, and everything shifted along.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("the live log should have been moved aside, got err=%v", err)
	}
	for i := 2; i <= MaxGenerations; i++ {
		b, err := os.ReadFile(generationPath(path, i))
		if err != nil {
			t.Fatalf("generation %d is missing: %v", i, err)
		}
		// .2 must now hold what .1 held, and so on: a shift, not an overwrite.
		if want := "gen" + strconv.Itoa(i-1) + "\n"; string(b) != want {
			t.Errorf("generation %d = %q, want %q — generations were overwritten rather than shifted", i, b, want)
		}
	}
	// And exactly one generation was dropped, from the end.
	if _, err := os.Stat(generationPath(path, MaxGenerations+1)); !os.IsNotExist(err) {
		t.Errorf("nothing should exist past generation %d", MaxGenerations)
	}
}

// A reader that opens only the live log reports everything older than the last
// rotation as though it never happened.
func TestGenerationsListsTheWholeHistoryNewestFirst(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sessions.jsonl")

	if got := Generations(path); len(got) != 0 {
		t.Errorf("nothing written yet, got %v", got)
	}

	for _, p := range []string{path, path + ".1", path + ".3"} {
		if err := os.WriteFile(p, []byte("{}\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	got := Generations(path)
	want := []string{path, path + ".1", path + ".3"}
	if len(got) != len(want) {
		t.Fatalf("Generations = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("Generations[%d] = %q, want %q (newest first, gaps skipped)", i, got[i], want[i])
		}
	}
}

// TestJSONLSinkRecordsReportedDenials covers the field that answers the question
// the policy fields cannot: `egress_allow` says what a run was *permitted* to
// reach, never whether it went looking for anything else.
//
// The key names are asserted verbatim on purpose. They carry the caveat — these
// counts come from lines the container printed on a stream the agent can also
// write to — and a rename to `egress_denied` would quietly upgrade a report into
// a claim.
func TestJSONLSinkRecordsReportedDenials(t *testing.T) {
	dir := t.TempDir()
	NewJSONLSink(dir).RecordSession(SessionMeta{
		Image:                     "sandbox-base:1",
		Network:                   "allowlist",
		EgressDeniedReported:      intp(4),
		EgressDeniedHostsReported: []string{"gist.github.com", "docs.python.org"},
	})

	raw, err := os.ReadFile(filepath.Join(dir, "sessions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	line := strings.TrimSpace(string(raw))

	var got record
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if got.EgressDenied == nil || *got.EgressDenied != 4 {
		t.Errorf("denial count = %v, want 4", got.EgressDenied)
	}
	if len(got.EgressDeniedHosts) != 2 {
		t.Errorf("denied hosts = %v, want both", got.EgressDeniedHosts)
	}
	for _, key := range []string{`"egress_denied_reported"`, `"egress_denied_hosts_reported"`} {
		if !strings.Contains(line, key) {
			t.Errorf("on-disk key %s is missing; the name is what carries the caveat:\n%s", key, line)
		}
	}
}

func intp(n int) *int { return &n }

// TestJSONLSinkTellsUnobservedFromNothingRefused is the distinction the pointer
// exists for, and the reason a bare int was wrong: with `omitempty` on an int,
// "nobody looked" and "looked, and the answer was zero" both vanish, so the
// earlier version of this test would have passed with the field hardcoded to 0.
//
// The zero is the more useful of the two — it is the only positive evidence a
// run's allowlist was wide enough for everything it actually wanted.
func TestJSONLSinkTellsUnobservedFromNothingRefused(t *testing.T) {
	read := func(t *testing.T, meta SessionMeta) string {
		t.Helper()
		dir := t.TempDir()
		NewJSONLSink(dir).RecordSession(meta)
		raw, err := os.ReadFile(filepath.Join(dir, "sessions.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(raw))
	}

	// Not observed: no allowlist, or detached, or interactive. No field at all.
	line := read(t, SessionMeta{Image: "sandbox-base:1", Network: "default"})
	if strings.Contains(line, "egress_denied") {
		t.Errorf("an unobserved run recorded a denial field:\n%s", line)
	}

	// Observed, and nothing was refused. An explicit zero, not an absence.
	line = read(t, SessionMeta{Image: "sandbox-base:1", Network: "allowlist", EgressDeniedReported: intp(0)})
	if !strings.Contains(line, `"egress_denied_reported":0`) {
		t.Errorf("an observed run with no denials must record an explicit 0:\n%s", line)
	}

	// And it must survive the round trip as zero rather than as nil.
	var got record
	if err := json.Unmarshal([]byte(line), &got); err != nil {
		t.Fatal(err)
	}
	if got.EgressDenied == nil || *got.EgressDenied != 0 {
		t.Errorf("round-tripped denial count = %v, want a pointer to 0", got.EgressDenied)
	}
}

// TestJSONLSinkRecordsTheRuntimeAndOmitsTheDefault covers the field task 3 adds:
// which OCI runtime a finished run was on.
//
// Omitted when empty, and that is the honest spelling rather than a saving of
// bytes: empty means the run took the host default, and writing `"runtime":
// "runc"` would be this tool asserting a name it never asked for and never read
// back. A reader who needs to know what the default was on that host has the
// engine to ask; a reader who sees a name knows it was chosen.
func TestJSONLSinkRecordsTheRuntimeAndOmitsTheDefault(t *testing.T) {
	dir := t.TempDir()
	s := NewJSONLSink(dir)

	s.RecordSession(SessionMeta{Image: "sandbox-base:1", Runtime: "kata-runtime", ExitCode: 0})
	s.RecordSession(SessionMeta{Image: "sandbox-base:1", ExitCode: 0})

	raw, err := os.ReadFile(filepath.Join(dir, "sessions.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
	if len(lines) != 2 {
		t.Fatalf("log has %d lines, want 2", len(lines))
	}

	var chosen record
	if err := json.Unmarshal([]byte(lines[0]), &chosen); err != nil {
		t.Fatalf("not valid JSON: %v", err)
	}
	if chosen.Runtime != "kata-runtime" {
		t.Errorf("runtime = %q, want %q — the boundary a run got is the point of recording it",
			chosen.Runtime, "kata-runtime")
	}
	if !strings.Contains(lines[1], `"image"`) {
		t.Fatalf("second line is not a record: %q", lines[1])
	}
	if strings.Contains(lines[1], "runtime") {
		t.Errorf("a host-default run named a runtime nobody chose: %q", lines[1])
	}
}

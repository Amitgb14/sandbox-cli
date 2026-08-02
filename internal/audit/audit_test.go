package audit

import (
	"encoding/json"
	"os"
	"path/filepath"
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

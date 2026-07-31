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

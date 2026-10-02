package audit

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// An event carries environment names and never values: the type has nowhere
// to put one, and this pins what reaches the file.
func TestRecordWritesNamesNotValues(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	l := NewLog(path)
	code := 3
	l.Record(api.Event{Type: api.EventSandboxCreated, Sandbox: "sbx_1", EnvNames: []string{"ANTHROPIC_API_KEY"},
		Labels: map[string]string{"agent": "claude"}})
	l.Record(api.Event{Type: api.EventProcessExited, Sandbox: "sbx_1", PID: 1, ExitCode: &code})
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("mode %v, want 0600: the log names projects and credentials", fi.Mode().Perm())
	}
	if !strings.Contains(string(b), `"env_names":["ANTHROPIC_API_KEY"]`) || !strings.Contains(string(b), `"exit_code":3`) {
		t.Errorf("log:\n%s", b)
	}
	events, truncated, _ := l.Read("sbx_1", 0)
	if len(events) != 2 || truncated || events[0].Time.IsZero() || events[0].Labels["agent"] != "claude" {
		t.Errorf("read back %+v", events)
	}
}

func TestLogNeverFailsARequest(t *testing.T) {
	if NewLog("") != nil {
		t.Error("no path should yield a nil log, which records nothing")
	}
	var none *Log
	none.Record(api.Event{Type: "x"})
	if ev, _, err := none.Read("sbx", 0); ev != nil || err != nil {
		t.Error("a nil log read something")
	}
	dir := t.TempDir()
	blocked := filepath.Join(dir, "file-not-a-dir")
	if err := os.WriteFile(blocked, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	NewLog(filepath.Join(blocked, "events.jsonl")).Record(api.Event{Type: "x"})
}

// A sandbox's events come back in order across rotated generations, only
// its own, with the newest kept when there are more than asked for. A line
// that does not parse — the half-written last one, or junk — is skipped, and
// one longer than a scanner's buffer does not hide the lines after it.
func TestReadAcrossGenerations(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	l := NewLog(path)
	l.Record(api.Event{Type: "a", Sandbox: "sbx_1"})
	l.Record(api.Event{Type: "other", Sandbox: "sbx_2"})
	if err := os.Rename(path, generationPath(path, 1)); err != nil {
		t.Fatal(err)
	}
	l.Record(api.Event{Type: "b", Sandbox: "sbx_1", Argv: []string{strings.Repeat("x", 2<<20)}})
	f, _ := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString("not json\n")
	f.Close()
	l.Record(api.Event{Type: "c", Sandbox: "sbx_1"})
	f, _ = os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	f.WriteString(`{"type":"half","sandbox":"sbx_1"`)
	f.Close()

	events, truncated, err := l.Read("sbx_1", 0)
	if err != nil || truncated {
		t.Fatal(err, truncated)
	}
	var types []string
	for _, e := range events {
		types = append(types, e.Type)
	}
	if strings.Join(types, ",") != "a,b,c" {
		t.Errorf("types %v, want a,b,c", types)
	}
	if last, truncated, _ := l.Read("sbx_1", 2); !truncated || len(last) != 2 || last[0].Type != "b" {
		t.Errorf("max 2: %v %v", len(last), truncated)
	}
}

// Rotation used to keep exactly one previous generation, which deleted history
// without saying so: at a few hundred bytes per run, the twelve-thousandth-oldest
// run simply stopped existing. This pins that it now shifts along and drops only
// the last one.
func TestRotationShiftsGenerationsAndDropsOnlyTheOldest(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "events.jsonl")
	l := NewLog(path)

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

	l.rotateIfLarge()

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
	path := filepath.Join(dir, "events.jsonl")

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

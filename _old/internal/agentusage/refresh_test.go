package agentusage

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRolledOver(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		at   time.Time
		want bool
	}{
		{"reset still ahead", now.Add(time.Minute), false},
		{"reset just passed", now.Add(-time.Minute), true},
		{"reset exactly now", now, true},
		// A window that reported no reset time cannot be placed either side of
		// one, so it is never called rolled over — the alternative is hiding a
		// percentage that is still true.
		{"no reset reported", time.Time{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := Window{Kind: KindSevenDay, Percent: 25, ResetsAt: tc.at}
			if got := w.RolledOver(now); got != tc.want {
				t.Errorf("RolledOver = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNeedsRefresh(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		at   time.Time
		want bool
	}{
		{"hours old", now.Add(-16 * time.Hour), true},
		{"just fetched", now.Add(-time.Second), false},
		{"exactly at the threshold", now.Add(-StaleAfter), true},
		// An age we cannot vouch for is not the same as a recent one.
		{"no stamp at all", time.Time{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := Snapshot{Agent: "claude", Windows: []Window{{Kind: KindFiveHour}}, FetchedAt: tc.at}
			if got := s.NeedsRefresh(now); got != tc.want {
				t.Errorf("NeedsRefresh = %v, want %v", got, tc.want)
			}
		})
	}
}

// A missing agent is the ordinary case on a host that runs claude only in the
// sandbox, so it has to be an explanation rather than "exec: not found".
func TestRefreshWithoutTheAgent(t *testing.T) {
	swapClaudeBin(t, filepath.Join(t.TempDir(), "no-such-agent"))
	err := Refresh(context.Background())
	if err == nil {
		t.Fatal("Refresh with no agent on PATH: want an error")
	}
	if !strings.Contains(err.Error(), "PATH") {
		t.Errorf("error = %q, want it to name PATH", err)
	}
}

func TestRefreshRunsTheAgent(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stand-in agent is a shell script")
	}
	dir := t.TempDir()
	argv := filepath.Join(dir, "argv")
	bin := filepath.Join(dir, "claude-stub")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argv + "\npwd >> " + argv + "\n"
	if err := os.WriteFile(bin, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	swapClaudeBin(t, bin)

	if err := Refresh(context.Background()); err != nil {
		t.Fatalf("Refresh: %v", err)
	}
	got, err := os.ReadFile(argv)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(got)), "\n")
	if len(lines) != 3 || lines[0] != "-p" || lines[1] != refreshPrompt {
		t.Fatalf("agent invoked with %q, want -p and the throwaway prompt", lines)
	}
	// Run away from the user's project: Claude Code files transcripts by working
	// directory, and this turn has no business in the history `context list`
	// offers to resume.
	if wd, _ := os.Getwd(); lines[2] == wd {
		t.Errorf("agent ran in %s, want a scratch directory", wd)
	}
}

// A failing agent has usually said why — signed out, offline, no plan — and that
// sentence is worth more than the exit status wrapping it.
func TestRefreshReportsWhatTheAgentSaid(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("stand-in agent is a shell script")
	}
	dir := t.TempDir()
	bin := filepath.Join(dir, "claude-stub")
	if err := os.WriteFile(bin, []byte("#!/bin/sh\necho 'Invalid API key'\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	swapClaudeBin(t, bin)

	err := Refresh(context.Background())
	if err == nil {
		t.Fatal("Refresh with a failing agent: want an error")
	}
	if !strings.Contains(err.Error(), "Invalid API key") {
		t.Errorf("error = %q, want it to carry the agent's own message", err)
	}
}

func swapClaudeBin(t *testing.T, path string) {
	t.Helper()
	prev := claudeBin
	claudeBin = path
	t.Cleanup(func() { claudeBin = prev })
}

package session

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/protocol"
	"github.com/Amitgb14/sandbox-cli/internal/rescue"
)

// A running pane gets snapshots, and what they hold is the work — including files git
// has never been told about, which is the whole point of issue #163.
//
// The reported loss was a file an agent wrote and nobody committed: the only snapshot
// was the *baseline*, taken before the run, so restoring it gave back a tree without
// the file. A sweep while the pane is running is what makes the file recoverable.
func TestSweepSnapshotsTheWorkOfARunningPane(t *testing.T) {
	dir, _ := repo(t)
	t.Setenv("XDG_CONFIG_HOME", shortTmp(t))

	k := NewKeeper(snapshotsOn())
	if k.Interval() == 0 {
		t.Fatal("snapshots are off in a config that enables them")
	}

	// The file the agent writes and never commits.
	if err := os.WriteFile(filepath.Join(dir, "tmp.txt"), []byte("the work"), 0o644); err != nil {
		t.Fatal(err)
	}

	pane := protocol.Pane{ID: "p_1", State: protocol.StateUnknown, Agent: "claude"}
	k.Sweep(context.Background(), []protocol.Pane{pane}, func(protocol.Pane) string { return dir })

	sessions, err := rescue.Sessions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("rescue sessions = %d, want one for the pane", len(sessions))
	}
	// Not a baseline: a baseline is the before-image, and the before-image is what
	// the bug report already had.
	if sessions[0].IsBaseline() {
		t.Error("the pane's session is a baseline; that is the state this fixes")
	}

	snaps, err := rescue.List(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	if len(snaps) == 0 {
		t.Fatal("no snapshots taken for a running pane")
	}
	// And the untracked file is in it, which is the claim that matters.
	if !inCommit(t, dir, snaps[0].Commit, "tmp.txt") {
		t.Errorf("snapshot %s does not hold the uncommitted file", snaps[0].Commit)
	}
}

// A pane that stops gets a final snapshot and its session closed with an outcome, so
// `recover list` describes a daemon-protected run the way it describes a foreground
// one.
func TestSweepClosesASessionWhenThePaneStops(t *testing.T) {
	dir, _ := repo(t)
	t.Setenv("XDG_CONFIG_HOME", shortTmp(t))
	k := NewKeeper(snapshotsOn())

	running := protocol.Pane{ID: "p_1", State: protocol.StateUnknown, Agent: "claude"}
	at := func(protocol.Pane) string { return dir }
	k.Sweep(context.Background(), []protocol.Pane{running}, at)

	// The work arrives between the last tick and the exit, which is the case the
	// closing snapshot exists for.
	if err := os.WriteFile(filepath.Join(dir, "late.txt"), []byte("written last"), 0o644); err != nil {
		t.Fatal(err)
	}
	code := 1
	failed := protocol.Pane{ID: "p_1", State: protocol.StateFailed, ExitCode: &code, Agent: "claude"}
	k.Sweep(context.Background(), []protocol.Pane{failed}, at)

	sessions, err := rescue.Sessions(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("rescue sessions = %d, want one", len(sessions))
	}
	s := sessions[0]
	if s.EndedAt == nil {
		t.Error("the session was left open after its pane stopped")
	}
	if s.Outcome != rescue.OutcomeFailed {
		t.Errorf("outcome = %q, want %q", s.Outcome, rescue.OutcomeFailed)
	}
	if s.ExitCode == nil || *s.ExitCode != 1 {
		t.Errorf("exit code = %v, want 1", s.ExitCode)
	}

	snaps, err := rescue.List(dir, true)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, sn := range snaps {
		if inCommit(t, dir, sn.Commit, "late.txt") {
			found = true
		}
	}
	if !found {
		t.Error("the file written between the last tick and the exit is in no snapshot")
	}
}

// A pane the catalog cannot place is not snapshotted. Its worktree may have been
// removed since it started, and writing into a guessed directory would put one
// repository's work under another's manifest.
func TestSweepSkipsAPaneWithNoDirectory(t *testing.T) {
	dir, _ := repo(t)
	t.Setenv("XDG_CONFIG_HOME", shortTmp(t))
	k := NewKeeper(snapshotsOn())

	pane := protocol.Pane{ID: "p_1", State: protocol.StateUnknown, Agent: "claude"}
	k.Sweep(context.Background(), []protocol.Pane{pane}, func(protocol.Pane) string { return "" })

	if sessions, err := rescue.Sessions(dir); err != nil {
		t.Fatal(err)
	} else if len(sessions) != 0 {
		t.Errorf("sessions = %d for a pane with no directory, want none", len(sessions))
	}
}

// Snapshots off in the config means off, and the interval has a floor.
//
// The floor is what the foreground path does not need: that one runs for as long as
// somebody sits there, and this one runs for as long as the machine is on. A trusted
// config with a typo in it is still a timer the daemon honours forever, and the cost
// is paid per pane.
func TestKeeperHonoursTheConfigAndFloorsTheInterval(t *testing.T) {
	off := snapshotsOn()
	enabled := false
	off.Snapshot.Enabled = &enabled
	if k := NewKeeper(off); k.Interval() != 0 {
		t.Errorf("snapshots disabled in config: interval = %s, want 0", k.Interval())
	}

	fast := snapshotsOn()
	fast.Snapshot.Interval = "1ms"
	if got := NewKeeper(fast).Interval(); got < minKeeperInterval {
		t.Errorf("interval = %s, want at least %s — a file with a typo is still a timer", got, minKeeperInterval)
	}

	// And a sane interval is honoured rather than replaced.
	sane := snapshotsOn()
	sane.Snapshot.Interval = "5m"
	if got := NewKeeper(sane).Interval(); got != 5*time.Minute {
		t.Errorf("interval = %s, want 5m", got)
	}
}

// A nil Keeper is the state every caller but the daemon is in, and it must be inert
// rather than a crash: a one-shot command that started a snapshot loop would have
// nothing to run it.
func TestANilKeeperIsInert(t *testing.T) {
	var k *Keeper
	if k.Interval() != 0 {
		t.Error("a nil keeper reported an interval")
	}
	k.Sweep(context.Background(), []protocol.Pane{{ID: "p_1"}}, func(protocol.Pane) string { return "/repo" })
	k.Close()
}

func snapshotsOn() config.Config {
	cfg := config.Default()
	on := true
	cfg.Snapshot.Enabled = &on
	cfg.Snapshot.Interval = "30s"
	return cfg
}

// inCommit asks git whether a path is in a commit's tree, which is the only way to
// know a snapshot actually holds the work rather than merely existing.
func inCommit(t *testing.T, dir, commit, path string) bool {
	t.Helper()
	cmd := exec.Command("git", "cat-file", "-e", commit+":"+path)
	cmd.Dir = dir
	return cmd.Run() == nil
}

// The **final** snapshot must stay reachable after the pane's session closes.
//
// `Sweep` removes the entry from the live map before calling `Stop`, and `Stop` is what
// takes the final snapshot — "whatever the agent wrote between the last tick and its
// exit", which is the one most worth having. So `LastSnapshot` answered "" for exactly
// the pane whose newest snapshot had just been written: a weaker version of the bug
// `last_snapshot` was fixed for.
func TestLastSnapshotSurvivesThePaneClosing(t *testing.T) {
	dir, _ := repo(t)
	t.Setenv("XDG_CONFIG_HOME", shortTmp(t))
	k := NewKeeper(snapshotsOn())

	if err := os.WriteFile(filepath.Join(dir, "work.txt"), []byte("one"), 0o644); err != nil {
		t.Fatal(err)
	}
	at := func(protocol.Pane) string { return dir }
	live := protocol.Pane{ID: "p_1", State: protocol.StateUnknown, Agent: "claude"}
	k.Sweep(context.Background(), []protocol.Pane{live}, at)

	first := k.LastSnapshot("p_1")
	if first == "" {
		t.Fatal("no snapshot commit for a running pane")
	}

	// The work that arrives between the last tick and the exit — the case Stop's final
	// snapshot exists for.
	if err := os.WriteFile(filepath.Join(dir, "work.txt"), []byte("two, written last"), 0o644); err != nil {
		t.Fatal(err)
	}
	code := 0
	done := protocol.Pane{ID: "p_1", State: protocol.StateDone, ExitCode: &code, Agent: "claude"}
	k.Sweep(context.Background(), []protocol.Pane{done}, at)

	last := k.LastSnapshot("p_1")
	if last == "" {
		t.Fatal("the pane's newest snapshot became unreachable the moment its session closed")
	}
	if last == first {
		t.Error("last_snapshot is still the second-to-last: Stop's final snapshot is not being recorded")
	}
	// And it really holds the late write, which is the whole claim.
	if !inCommit(t, dir, last, "work.txt") {
		t.Errorf("commit %s does not hold the file", last)
	}
}

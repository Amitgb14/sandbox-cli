package session

import (
	"context"
	"net"
	"os"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/protocol"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// One repository, one session — from any directory inside it, including a linked
// worktree.
//
// `rev-parse --show-toplevel` answers with a *worktree's own* directory, while
// worktree.RepoID follows the pointer back to the main checkout. Using both meant
// one session directory holding a catalog whose Root was whichever directory the
// last command ran in: the worktree recorded as the repository, flagged Main (the
// one worktree `worktree rm` must never touch), the real checkout missing, and no
// managed worktrees found at all.
func TestOpenFromAWorktreeStillMeansTheRepository(t *testing.T) {
	dir, _ := repo(t)
	t.Setenv("XDG_CONFIG_HOME", shortTmp(t))

	// A **sandbox-managed** worktree, made through the same API `--worktree` uses,
	// because that is the case this is about: its path is derived from the main
	// repository's id, so reading the session's root from the worktree is exactly
	// what made worktree.List look in the wrong place and come back empty.
	info, err := worktree.Resolve(dir, "feat")
	if err != nil {
		t.Skipf("git worktree is needed here: %v", err)
	}
	wt := info.Path
	t.Cleanup(func() { worktree.Remove(dir, "feat", true) })

	fromRoot, err := Open(dir, "dev", "docker")
	if err != nil {
		t.Fatal(err)
	}
	if fromRoot.Root() != dir {
		t.Fatalf("Root from the repository itself = %q, want %q", fromRoot.Root(), dir)
	}
	fromWorktree, err := Open(wt, "dev", "docker")
	if err != nil {
		t.Fatal(err)
	}

	if fromWorktree.Dir() != fromRoot.Dir() {
		t.Errorf("two session directories for one repository:\n  %s\n  %s", fromRoot.Dir(), fromWorktree.Dir())
	}
	if fromWorktree.Root() != dir {
		t.Errorf("Root from inside a worktree = %q, want the main checkout %q", fromWorktree.Root(), dir)
	}

	// And the consequence that made it more than cosmetic: the grouping.
	if err := fromWorktree.Adopt(context.Background(), &fakeEngine{}); err != nil {
		t.Fatal(err)
	}
	snap := fromWorktree.Snapshot()
	if len(snap.Workspaces) != 1 {
		t.Fatalf("workspaces = %d", len(snap.Workspaces))
	}
	ws := snap.Workspaces[0]
	if ws.RepoPath != dir {
		t.Errorf("workspace.repo_path = %q, want the main checkout %q", ws.RepoPath, dir)
	}
	var mains, sawLinked int
	for _, w := range ws.Worktrees {
		if w.Main {
			mains++
			if w.Path != dir {
				t.Errorf("the worktree flagged Main is %q, not the user's own checkout", w.Path)
			}
		}
		if w.Branch == "feat" {
			sawLinked++
		}
	}
	if mains != 1 {
		t.Errorf("worktrees flagged Main = %d, want exactly the user's own checkout", mains)
	}
	if sawLinked != 1 {
		t.Errorf("the managed worktree is missing from the catalog: %+v", ws.Worktrees)
	}
}

// The container name is deterministic and therefore **reused** by the next run on
// a branch. A pane must not be rebound onto a container that names a different
// pane: it would report a finished agent as running against somebody else's
// container, and hide the new pane entirely — so a phase-2 mutation by pane id
// would act on the wrong thing.
func TestAdoptDoesNotBindAPaneToAnotherPanesContainer(t *testing.T) {
	dir, repoID := repo(t)
	s := open(t, dir)

	const name = "sandbox-x-feat"
	first := running(name, repoID, "feat", map[string]string{
		sandbox.LabelPane:  "p_one",
		sandbox.LabelAgent: "claude",
	})
	if err := s.Adopt(context.Background(), &fakeEngine{containers: []runtime.ContainerInfo{first}}); err != nil {
		t.Fatal(err)
	}

	// The first container is reaped and a new run takes the name back.
	second := running(name, repoID, "feat", map[string]string{
		sandbox.LabelPane:  "p_two",
		sandbox.LabelAgent: "codex",
	})
	second.ID = "id-second"
	if err := s.Adopt(context.Background(), &fakeEngine{containers: []runtime.ContainerInfo{second}}); err != nil {
		t.Fatal(err)
	}

	byID := map[string]protocol.Pane{}
	for _, p := range s.Snapshot().Panes {
		byID[p.ID] = p
	}
	one, ok := byID["p_one"]
	if !ok {
		t.Fatal("the finished pane vanished; a pane outlives its container")
	}
	if one.State != protocol.StateStopped {
		t.Errorf("p_one is %q, want stopped: its container was reaped, and the one with that name now is p_two's", one.State)
	}
	if one.ContainerID == "id-second" {
		t.Error("p_one was bound to p_two's container")
	}
	if _, ok := byID["p_two"]; !ok {
		t.Errorf("p_two never entered the catalog: %v", byID)
	}
	if two := byID["p_two"]; two.Agent != "codex" || two.ContainerID != "id-second" {
		t.Errorf("p_two = %+v, want codex on id-second", two)
	}
}

// A second `serve` must not steal the first one's socket. It used to: unlinking
// unconditionally meant the second bound over the first, and the second's own
// cleanup then removed the socket and pid file, leaving the first running,
// unreachable, invisible to `serve status`, and still writing session.json.
func TestServeRefusesWhenOneIsAlreadyAnswering(t *testing.T) {
	dir, _ := repo(t)
	s := open(t, dir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, &fakeEngine{}) }()
	waitForSocket(t, SockPath(s.Dir()), done)

	second, err := Open(dir, "dev", "docker")
	if err != nil {
		t.Fatal(err)
	}
	err = second.Serve(context.Background(), &fakeEngine{})
	if err == nil {
		t.Fatal("a second serve bound over a live one")
	}

	// And the first is still there afterwards, which is the half that matters.
	conn, derr := net.DialTimeout("unix", SockPath(s.Dir()), time.Second)
	if derr != nil {
		t.Fatalf("the refusal took the first daemon's socket with it: %v", derr)
	}
	conn.Close()
}

// Shutdown must not wait on a client that connected and went quiet. It used to
// block forever in that read, so wg.Wait never returned — and because
// signal.NotifyContext keeps the handler registered, every later SIGTERM was
// swallowed too: `serve stop` and Ctrl-C both looked dead, and SIGKILL then left
// the socket and pid file behind.
func TestServeStopsWithAnIdleClientConnected(t *testing.T) {
	dir, _ := repo(t)
	s := open(t, dir)

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, &fakeEngine{}) }()
	sock := SockPath(s.Dir())
	waitForSocket(t, sock, done)

	// Connect, say nothing. This is `socat`, which the package doc invites.
	idle, err := net.Dial("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	defer idle.Close()
	time.Sleep(50 * time.Millisecond)

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve returned %v, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return with one idle client connected; SIGTERM would be swallowed and only SIGKILL would work")
	}
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Error("the socket outlived a clean shutdown")
	}
}

// The two sources of a listing must answer the same question. The client-side
// filter honoured only All and dropped the workspace and worktree filters — which
// no CLI caller sets today, which is exactly how it would have stayed wrong.
func TestFilterPanesIsTheOneRule(t *testing.T) {
	dir, repoID := repo(t)
	s := open(t, dir)

	a := running("sandbox-x-a", repoID, "aaa", map[string]string{sandbox.LabelPane: "p_a"})
	b := running("sandbox-x-b", repoID, "bbb", map[string]string{sandbox.LabelPane: "p_b"})
	if err := s.Adopt(context.Background(), &fakeEngine{containers: []runtime.ContainerInfo{a, b}}); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()

	for _, p := range []protocol.PaneListParams{
		{},
		{All: true},
		{Worktree: "wt_aaa"},
		{Worktree: "wt_aaa", All: true},
		{Workspace: snap.Workspaces[0].ID, All: true},
		{Workspace: "ws_nope", All: true},
	} {
		server := s.Panes(p)
		client := FilterPanes(snap, p)
		if len(server) != len(client) {
			t.Errorf("params %+v: daemon returned %d panes, a client reading the catalog got %d", p, len(server), len(client))
			continue
		}
		for i := range server {
			if server[i].ID != client[i].ID {
				t.Errorf("params %+v: pane %d is %q from the daemon and %q from a client", p, i, server[i].ID, client[i].ID)
			}
		}
	}
	if got := FilterPanes(snap, protocol.PaneListParams{Worktree: "wt_aaa", All: true}); len(got) != 1 || got[0].ID != "p_a" {
		t.Errorf("the worktree filter did nothing: %+v", got)
	}
}

// Belt and braces on the identity the whole scheme rests on: the session id and
// the container label are the same function of the same repository.
func TestSessionIDAndRepoLabelAgree(t *testing.T) {
	dir, repoID := repo(t)
	id, err := IDFor(dir)
	if err != nil {
		t.Fatal(err)
	}
	if id != repoID {
		t.Errorf("session id %q and sandbox.repo label %q disagree; adoption joins on the label", id, repoID)
	}
	if got, err := worktree.RepoID(dir); err != nil || got != id {
		t.Errorf("worktree.RepoID = %q (%v), want %q", got, err, id)
	}
}

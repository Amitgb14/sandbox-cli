package session

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/protocol"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// fakeEngine is the Lister with no daemon behind it. Note what it is *not*: a
// new container struct. `runtime.ContainerInfo` is what the real engine returns
// and what a pane's state is decided from, so a fake that invented its own shape
// would be testing a translation layer that does not exist in production.
type fakeEngine struct {
	// mu guards the fields, because `pane.wait` polls this from the server's
	// goroutine while a test mutates it from another — which is the whole point of
	// the wait tests, and a data race without this.
	mu         sync.Mutex
	containers []runtime.ContainerInfo
	err        error
	asked      []map[string]string
}

func (f *fakeEngine) Containers(_ context.Context, labels map[string]string) ([]runtime.ContainerInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.asked = append(f.asked, labels)
	if f.err != nil {
		return nil, f.err
	}
	return f.containers, nil
}

// repo makes a real git repository, because everything here is keyed on
// worktree.RepoID and that asks git. A fake path would test the map and not the
// identity.
func repo(t *testing.T) (dir, repoID string) {
	t.Helper()
	dir = shortTmp(t)
	// macOS /var is a symlink to /private/var, and RepoID hashes the absolute
	// path: resolving here is what makes the id stable between this test's view of
	// the directory and git's.
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "T"},
		{"commit", "-q", "--allow-empty", "-m", "first"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git is needed for these tests: %v: %s", err, out)
		}
	}
	id, err := worktree.RepoID(dir)
	if err != nil {
		t.Fatal(err)
	}
	return dir, id
}

// open points the config root at a temp dir so sessions land there rather than in
// the developer's own ~/.config/sandbox.
func open(t *testing.T, dir string) *Server {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", shortTmp(t))
	s, err := Open(dir, "dev", "docker")
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func running(name, repoID, branch string, labels map[string]string) runtime.ContainerInfo {
	l := map[string]string{
		sandbox.LabelCLI:    "1",
		sandbox.LabelRepo:   repoID,
		sandbox.LabelBranch: branch,
	}
	for k, v := range labels {
		l[k] = v
	}
	return runtime.ContainerInfo{
		ID: "id-" + name, Name: name, Labels: l, State: "running",
		CreatedAt: time.Date(2026, 9, 9, 12, 0, 0, 0, time.UTC),
	}
}

// Case 1 of the phase's adoption table, and the one the whole feature rests on: a
// labelled container the catalog already knows gets its container id and state
// rebound rather than duplicated.
func TestAdoptReboundsAKnownPane(t *testing.T) {
	dir, repoID := repo(t)
	s := open(t, dir)

	c := running("sandbox-x-feat", repoID, "feat", map[string]string{
		sandbox.LabelPane:  "p_abc",
		sandbox.LabelAgent: "claude",
	})
	eng := &fakeEngine{containers: []runtime.ContainerInfo{c}}
	if err := s.Adopt(context.Background(), eng); err != nil {
		t.Fatal(err)
	}
	if got := len(s.Snapshot().Panes); got != 1 {
		t.Fatalf("panes = %d, want 1", got)
	}

	// Adopt again with the container's id changed, as a restart would leave it.
	c.ID = "id-restarted"
	eng.containers = []runtime.ContainerInfo{c}
	if err := s.Adopt(context.Background(), eng); err != nil {
		t.Fatal(err)
	}
	panes := s.Snapshot().Panes
	if len(panes) != 1 {
		t.Fatalf("a second adopt produced %d panes; it rebinds, it does not append", len(panes))
	}
	if panes[0].ID != "p_abc" {
		t.Errorf("pane id = %q, want the one on the label", panes[0].ID)
	}
	if panes[0].ContainerID != "id-restarted" {
		t.Errorf("container id = %q, want the engine's current answer", panes[0].ContainerID)
	}

	// The filter is the one `sandbox-cli list` uses, which is what makes the two
	// listings comparable — the phase's acceptance rests on it.
	if got := eng.asked[0]; got[sandbox.LabelCLI] != "1" || got[sandbox.LabelRepo] != repoID {
		t.Errorf("asked the engine for %v, want cli+repo so the catalog matches `list`", got)
	}
}

// Cases 2 and 3: a container the catalog has never seen. The distinction between
// them is what a caller can *do* with the result, so it is not cosmetic — one has
// a pane id to address, the other has only a name.
func TestAdoptSynthesisesAndMarksLegacy(t *testing.T) {
	dir, repoID := repo(t)
	s := open(t, dir)

	labelled := running("sandbox-x-one", repoID, "one", map[string]string{
		sandbox.LabelPane:     "p_known",
		sandbox.LabelPaneKind: string(protocol.PaneVerify),
	})
	legacy := running("sandbox-x-two", repoID, "two", nil) // started before any of this existed

	if err := s.Adopt(context.Background(), &fakeEngine{
		containers: []runtime.ContainerInfo{labelled, legacy},
	}); err != nil {
		t.Fatal(err)
	}

	byID := map[string]protocol.Pane{}
	for _, p := range s.Snapshot().Panes {
		byID[p.ID] = p
	}
	if len(byID) != 2 {
		t.Fatalf("panes = %v, want two", byID)
	}

	p, ok := byID["p_known"]
	if !ok {
		t.Fatal("a container carrying a pane label was not adopted under that id")
	}
	if p.Legacy {
		t.Error("a labelled container was marked legacy")
	}
	if p.Kind != protocol.PaneVerify {
		t.Errorf("kind = %q, want the label's value", p.Kind)
	}

	// The legacy one is addressed by its container *name*, and that is the whole
	// point: a minted p_ id would name something no engine has heard of, so a
	// client that used it to kill the container would be told it succeeded.
	old, ok := byID["sandbox-x-two"]
	if !ok {
		t.Fatalf("an unlabelled container was not listed under its name: %v", byID)
	}
	if !old.Legacy {
		t.Error("an unlabelled container was not marked legacy")
	}
	if old.Kind != protocol.PaneCommand {
		t.Errorf("kind = %q; with no agent label and no pane kind, command is what is known", old.Kind)
	}
}

// Case 4, and the reason the catalog exists at all: a pane whose container is
// gone becomes stopped and **stays listed**. The engine is precisely the thing
// that can no longer answer, so dropping the pane would lose the only record.
func TestAdoptKeepsAPaneWhoseContainerIsGone(t *testing.T) {
	dir, repoID := repo(t)
	s := open(t, dir)

	c := running("sandbox-x-feat", repoID, "feat", map[string]string{
		sandbox.LabelPane:    "p_gone",
		sandbox.LabelAgent:   "claude",
		sandbox.LabelSession: "conv_1",
	})
	if err := s.Adopt(context.Background(), &fakeEngine{containers: []runtime.ContainerInfo{c}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Adopt(context.Background(), &fakeEngine{}); err != nil {
		t.Fatal(err)
	}

	panes := s.Snapshot().Panes
	if len(panes) != 1 {
		t.Fatalf("panes = %d; a pane outlives its container", len(panes))
	}
	if panes[0].State != protocol.StateStopped {
		t.Errorf("state = %q, want stopped", panes[0].State)
	}
	if panes[0].ContainerID != "" {
		t.Error("a stopped pane still names a container id")
	}
	// The conversation survives, which is what makes a stopped pane worth keeping:
	// it can be carried on rather than restarted.
	if panes[0].ConversationID != "conv_1" {
		t.Errorf("conversation = %q, want it kept across the container going away", panes[0].ConversationID)
	}
}

// Case 5: two containers on one branch both list. Resolution by branch is
// therefore ambiguous and is the caller's problem — which is the existing rule in
// `resolveSession`, carried forward: stopping the wrong agent costs its work.
func TestAdoptListsBothContainersOnOneBranch(t *testing.T) {
	dir, repoID := repo(t)
	s := open(t, dir)

	old := running("sandbox-x-feat", repoID, "feat", map[string]string{sandbox.LabelPane: "p_1"})
	old.State = "exited"
	old.ExitCode = 0
	live := running("sandbox-x-feat-2", repoID, "feat", map[string]string{sandbox.LabelPane: "p_2"})

	if err := s.Adopt(context.Background(), &fakeEngine{
		containers: []runtime.ContainerInfo{old, live},
	}); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	if len(snap.Panes) != 2 {
		t.Fatalf("panes = %d, want both", len(snap.Panes))
	}
	// Both on the same worktree record, since the worktree id is derived from the
	// branch — no label, so the catalog and the container cannot disagree.
	if snap.Panes[0].WorktreeID != snap.Panes[1].WorktreeID {
		t.Errorf("two panes on branch feat landed in %q and %q",
			snap.Panes[0].WorktreeID, snap.Panes[1].WorktreeID)
	}

	// A default listing hides the finished one; --all shows both. The default
	// answers "what is running", which is the question people ask.
	if got := len(s.Panes(protocol.PaneListParams{})); got != 1 {
		t.Errorf("default listing showed %d panes, want only the live one", got)
	}
	if got := len(s.Panes(protocol.PaneListParams{All: true})); got != 2 {
		t.Errorf("--all showed %d panes, want both", got)
	}
}

// The exit code is the one thing a container can honestly say about how it went,
// and a pointer is what lets 0 and "has not exited" be different answers.
func TestPaneStateComesFromTheContainerAndNoFurther(t *testing.T) {
	cases := []struct {
		state string
		code  int
		want  protocol.PaneState
		hasEC bool
	}{
		{"running", 0, protocol.StateUnknown, false},
		{"restarting", 0, protocol.StateUnknown, false},
		{"paused", 0, protocol.StateUnknown, false},
		{"created", 0, protocol.StateStarting, false},
		{"exited", 0, protocol.StateDone, true},
		{"exited", 1, protocol.StateFailed, true},
		{"dead", 137, protocol.StateFailed, true},
	}
	for _, tc := range cases {
		got, code := paneStateFor(runtime.ContainerInfo{State: tc.state, ExitCode: tc.code})
		if got != tc.want {
			t.Errorf("%s/%d: state = %q, want %q", tc.state, tc.code, got, tc.want)
		}
		if tc.hasEC != (code != nil) {
			t.Errorf("%s/%d: exit code pointer = %v, want present=%v", tc.state, tc.code, code, tc.hasEC)
		}
		if code != nil && *code != tc.code {
			t.Errorf("%s: exit code = %d, want %d", tc.state, *code, tc.code)
		}
	}

	// The decision worth pinning: a running container does **not** report
	// "working". Whether the agent is editing a file or sitting at a permission
	// prompt is the same running container, and guessing would have somebody
	// waiting on a pane that is waiting on them. Phase 4 reads the transcript.
	if got, _ := paneStateFor(runtime.ContainerInfo{State: "running"}); got == protocol.StateWorking {
		t.Error("a running container was reported as working; only a transcript can say that")
	}
}

// Persistence: write, reopen, and the catalog is the same. This is what a `serve`
// restart depends on.
func TestSaveAndReopen(t *testing.T) {
	dir, repoID := repo(t)
	t.Setenv("XDG_CONFIG_HOME", shortTmp(t))

	s, err := Open(dir, "dev", "docker")
	if err != nil {
		t.Fatal(err)
	}
	c := running("sandbox-x-feat", repoID, "feat", map[string]string{
		sandbox.LabelPane:  "p_keep",
		sandbox.LabelAgent: "claude",
	})
	if err := s.Adopt(context.Background(), &fakeEngine{containers: []runtime.ContainerInfo{c}}); err != nil {
		t.Fatal(err)
	}
	if err := s.Save("test"); err != nil {
		t.Fatal(err)
	}

	again, err := Open(dir, "dev", "docker")
	if err != nil {
		t.Fatal(err)
	}
	before, after := s.Snapshot(), again.Snapshot()
	if after.ID != before.ID || len(after.Panes) != 1 || after.Panes[0].ID != "p_keep" {
		t.Fatalf("reopen lost the catalog: %+v", after)
	}

	// 0600, because the socket lives beside it and the directory's mode is the
	// authentication.
	st, err := os.Stat(StatePath(s.Dir()))
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Errorf("session.json is %v, want 0600", st.Mode().Perm())
	}
	if dst, err := os.Stat(s.Dir()); err == nil && dst.Mode().Perm() != 0o700 {
		t.Errorf("session dir is %v, want 0700", dst.Mode().Perm())
	}

	// No temp files left behind: a directory accumulating session.json.tmp-* is
	// how a full disk gets diagnosed as something else.
	entries, _ := os.ReadDir(s.Dir())
	for _, e := range entries {
		if len(e.Name()) > 13 && e.Name()[:13] == "session.json." && e.Name() != "session.json.lock" {
			t.Errorf("left %s behind", e.Name())
		}
	}
	// And one line in the event log per save.
	if b, err := os.ReadFile(EventsPath(s.Dir())); err != nil || len(b) == 0 {
		t.Errorf("no event recorded for the save: %v", err)
	}
}

// A catalog that will not parse is kept and rebuilt, not deleted. Keeping it costs
// a file; deleting it loses the record of panes whose containers are already gone,
// which is the one content the engine cannot give back.
func TestOpenKeepsACorruptCatalogAndRebuilds(t *testing.T) {
	dir, repoID := repo(t)
	t.Setenv("XDG_CONFIG_HOME", shortTmp(t))

	s, err := Open(dir, "dev", "docker")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(StatePath(s.Dir()), []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}

	again, err := Open(dir, "dev", "docker")
	if err != nil {
		t.Fatalf("a corrupt catalog made Open fail; it must rebuild: %v", err)
	}
	if got := len(again.Snapshot().Panes); got != 0 {
		t.Errorf("rebuilt catalog has %d panes before adopting", got)
	}

	var backups int
	entries, _ := os.ReadDir(again.Dir())
	for _, e := range entries {
		if len(e.Name()) > 17 && e.Name()[:17] == "session.json.bak-" {
			backups++
		}
	}
	if backups != 1 {
		t.Errorf("backups of the unparseable catalog = %d, want 1", backups)
	}

	// And it adopts from the engine afterwards, which is what makes rebuilding a
	// recovery rather than a reset.
	c := running("sandbox-x-feat", repoID, "feat", map[string]string{sandbox.LabelPane: "p_x"})
	if err := again.Adopt(context.Background(), &fakeEngine{containers: []runtime.ContainerInfo{c}}); err != nil {
		t.Fatal(err)
	}
	if got := len(again.Snapshot().Panes); got != 1 {
		t.Errorf("panes after rebuild+adopt = %d, want 1", got)
	}
}

// One session per repository, derived from worktree.RepoID — so two clones sharing
// a directory *name* do not share a socket, which a name alone would not prevent.
func TestSessionIDIsDerivedFromTheRepositoryIdentity(t *testing.T) {
	a, idA := repo(t)
	b, idB := repo(t)
	if idA == idB {
		t.Fatal("two temp repos got one id; the path hash is not doing its job")
	}

	sa, err := IDFor(a)
	if err != nil {
		t.Fatal(err)
	}
	sb, err := IDFor(b)
	if err != nil {
		t.Fatal(err)
	}
	if sa == sb {
		t.Errorf("two repositories resolved to one session id %q, so they would share a socket", sa)
	}
	// Derived, not minted: asking twice gives the same answer, which is what lets
	// two processes find one socket without coordinating.
	if twice, _ := IDFor(a); twice != sa {
		t.Errorf("IDFor is not stable: %q then %q", sa, twice)
	}
	// And a subdirectory of the repository resolves to the repository's session,
	// not one of its own.
	sub := filepath.Join(a, "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := IDFor(sub); err != nil || got != sa {
		t.Errorf("from a subdirectory: %q (%v), want the repository's own %q", got, err, sa)
	}
}

// The grouping: git is asked, and a branch only a container mentions gets a record
// with **no path**. Inventing a path is how a later command mounts the wrong
// directory.
func TestWorkspaceGroupingNeverInventsAPath(t *testing.T) {
	dir, repoID := repo(t)
	s := open(t, dir)

	c := running("sandbox-x-ghost", repoID, "ghost-branch", map[string]string{sandbox.LabelPane: "p_g"})
	if err := s.Adopt(context.Background(), &fakeEngine{containers: []runtime.ContainerInfo{c}}); err != nil {
		t.Fatal(err)
	}
	snap := s.Snapshot()
	if len(snap.Workspaces) != 1 {
		t.Fatalf("workspaces = %d, want one per repository", len(snap.Workspaces))
	}
	ws := snap.Workspaces[0]
	if ws.RepoHash != repoID || ws.RepoPath != dir {
		t.Errorf("workspace = %q at %q, want %q at %q", ws.RepoHash, ws.RepoPath, repoID, dir)
	}

	var main, ghost *protocol.Worktree
	for i := range ws.Worktrees {
		switch ws.Worktrees[i].Branch {
		case "main":
			main = &ws.Worktrees[i]
		case "ghost_branch", "ghost-branch":
			ghost = &ws.Worktrees[i]
		}
	}
	if main == nil || !main.Main || main.Path != dir {
		t.Errorf("the user's own checkout is not listed as main at %q: %+v", dir, ws.Worktrees)
	}
	if ghost == nil {
		t.Fatalf("a branch only a container mentions got no record: %+v", ws.Worktrees)
	}
	if ghost.Path != "" {
		t.Errorf("invented a path for a worktree git has not heard of: %q", ghost.Path)
	}
}

// The engine failing is a refused request, not an empty catalog: an empty answer
// would read as "nothing is running", which is the one wrong thing to say.
func TestAdoptReportsAnEngineFailure(t *testing.T) {
	dir, _ := repo(t)
	s := open(t, dir)
	err := s.Adopt(context.Background(), &fakeEngine{err: os.ErrPermission})
	if err == nil {
		t.Fatal("an engine that could not be asked produced a successful adopt")
	}
	if got := len(s.Snapshot().Panes); got != 0 {
		t.Errorf("panes = %d after a failed adopt", got)
	}
}

// A round trip over the real socket, with a real listener: hello first, then the
// two ops phase 1 implements, then an op that does not exist.
func TestServeAnswersThePhaseOneOps(t *testing.T) {
	dir, repoID := repo(t)
	s := open(t, dir)

	c := running("sandbox-x-feat", repoID, "feat", map[string]string{
		sandbox.LabelPane:  "p_sock",
		sandbox.LabelAgent: "claude",
	})
	eng := &fakeEngine{containers: []runtime.ContainerInfo{c}}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, eng) }()

	sock := SockPath(s.Dir())
	waitForSocket(t, sock, done)

	var list protocol.PaneListResult
	if err := Call(sock, protocol.OpPaneList, protocol.PaneListParams{}, &list); err != nil {
		t.Fatal(err)
	}
	if len(list.Panes) != 1 || list.Panes[0].ID != "p_sock" {
		t.Fatalf("pane.list over the socket returned %+v", list.Panes)
	}

	var snap protocol.Session
	if err := Call(sock, protocol.OpSnapshot, struct{}{}, &snap); err != nil {
		t.Fatal(err)
	}
	if snap.Root != dir || len(snap.Workspaces) != 1 {
		t.Errorf("session.snapshot = %+v", snap)
	}

	// An op that does not exist refuses, rather than succeeding with nothing: a
	// client cannot tell the second from a feature that silently did nothing.
	// An op from the protocol's eventual surface that this phase does not implement.
	// Named rather than invented, because "unknown op" and "op I have not written
	// yet" must answer the same way: a client cannot tell them apart and should not
	// have to.
	err := Call(sock, "session.events.subscribe", struct{}{}, nil)
	if err == nil {
		t.Fatal("an unimplemented op answered ok")
	}
	var perr *protocol.Error
	if !asProtocolError(err, &perr) || perr.Code != protocol.CodeInvalid {
		t.Errorf("unimplemented op returned %v, want a protocol error with code %q", err, protocol.CodeInvalid)
	}

	// The socket is 0600: the directory's mode is the authentication, and this is
	// the half that still holds if the directory is ever loosened.
	if st, err := os.Stat(sock); err != nil {
		t.Error(err)
	} else if st.Mode().Perm() != 0o600 {
		t.Errorf("socket is %v, want 0600", st.Mode().Perm())
	}
	// And the pid file names this process, so `serve status` can check the process
	// rather than trusting the file's existence.
	if b, err := os.ReadFile(PIDPath(s.Dir())); err != nil {
		t.Error(err)
	} else if got := string(b); got != itoa(os.Getpid())+"\n" {
		t.Errorf("pid file = %q, want this process", got)
	}

	cancel()
	if err := <-done; err != nil {
		t.Errorf("Serve returned %v after cancellation, want nil", err)
	}
	// Cleaned up on the way out, so a later `serve status` does not report a
	// daemon that stopped.
	if _, err := os.Stat(sock); !os.IsNotExist(err) {
		t.Error("the socket outlived the server")
	}
}

// Hello must be first, and the requirement is not ceremony: it is what reports
// the protocol revision, so a client on a different one finds out before it has
// asked for anything.
func TestServeRequiresHelloFirst(t *testing.T) {
	dir, _ := repo(t)
	s := open(t, dir)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, &fakeEngine{}) }()
	sock := SockPath(s.Dir())
	waitForSocket(t, sock, done)

	conn, err := dial(sock)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte(`{"id":"1","op":"pane.list","params":{}}` + "\n")); err != nil {
		t.Fatal(err)
	}
	var resp struct {
		OK    bool            `json:"ok"`
		Error *protocol.Error `json:"error"`
	}
	if err := json.NewDecoder(conn).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.OK {
		t.Fatal("an op before hello was answered")
	}
	if resp.Error == nil || resp.Error.Code != protocol.CodeInvalid {
		t.Errorf("error = %+v, want %q", resp.Error, protocol.CodeInvalid)
	}
}

// Package session owns the catalog of sandboxes for one repository: which
// worktrees exist, which containers are in them, and how each is doing.
//
// It is the state behind `sandbox-cli serve`. What makes it worth having is not
// that it remembers — docker already remembers, which is why `sandbox-cli list`
// survives a `kill -9` of the CLI — but that it holds the facts the engine has no
// field for, and holds them grouped. A worktree with an agent pane and the verify
// pane that judges it is a structure no `docker ps` can express.
//
// Three rules shape the whole package.
//
// **The engine is the truth about containers.** Nothing here is believed over
// `docker inspect`: Adopt re-reads the engine and rewrites the catalog to match,
// so a pane whose container is gone becomes stopped rather than staying hopeful.
// The catalog's authority is over what the engine does not know — grouping, pane
// identity, the agent conversation a pane belongs to.
//
// **A write is atomic or it did not happen.** session.json is written to a temp
// file, fsynced and renamed under an advisory lock, because the alternative is a
// half-written catalog after a laptop closes, and a half-written catalog is worse
// than none: it parses.
//
// **Nothing is synthesised.** A pane state that cannot be proven is
// StateUnknown, a worktree git has not heard of is not invented, and a container
// with no pane label is reported as legacy rather than given an id that addresses
// nothing.
package session

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/protocol"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// Lister is the engine capability this package needs: enumerate the containers
// this tool started. It is `runtime.Inspector` under another name, declared as a
// one-method interface here so tests can inject a fake without a daemon.
//
// Deliberately *not* a new struct of its own. The plan for this track asked for an
// `EngineContainer` stub carrying id, name, labels and a running flag;
// `runtime.ContainerInfo` already carries those four plus the exit code, the
// timestamps and the stdin/tty facts — and a pane's state cannot be decided
// without the exit code. A parallel struct would have to be kept in step by hand,
// and the hand would forget.
type Lister = runtime.Inspector

// Server is one session: one repository, one socket, one catalog.
type Server struct {
	dir  string // ~/.config/sandbox/sessions/<sid>
	root string // the repository this session is for

	mu   sync.Mutex
	sess protocol.Session
}

// ErrNoSession is returned when a session directory does not exist yet, so a
// caller can tell "no daemon has ever run here" from "the daemon is broken".
var ErrNoSession = errors.New("no session")

// SessionsDir is where every session's directory lives.
func SessionsDir() string {
	root := config.ConfigRoot()
	if root == "" {
		root = filepath.Join(os.TempDir(), "sandbox")
	}
	return filepath.Join(root, "sessions")
}

// IDFor is the session id for a repository, and it is derived rather than minted
// so that two processes asking about one repository find the same socket without
// coordinating.
//
// Built on worktree.RepoID, which is the directory name plus eight hex of the
// sha256 of its absolute path. That is already the identity behind the
// `sandbox.repo` label, the container name and the managed worktree directory, so
// deriving the session from it makes "this session is about that repository" true
// by construction — and makes two clones of a same-named repo impossible to
// confuse, which a name alone would not.
func IDFor(repoDir string) (string, error) {
	return worktree.RepoID(repoDir)
}

// MaxSockPath is the length a session's socket path must fit inside.
//
// A unix socket address is a fixed-size struct: `sun_path` is 104 bytes on
// darwin and the BSDs, 108 on Linux. Exceeding it fails at bind with "invalid
// argument", which says nothing about paths and is the kind of error somebody
// spends an afternoon on — so the limit is checked up front, with the remedy in
// the sentence. 100 rather than 104 to leave room for the NUL and for a platform
// that is stricter than the two measured.
//
// It is also why the session directory is named by worktree.RepoID alone, with no
// "-default" suffix: the suffix was eight bytes of a budget of a hundred, spent
// to distinguish sessions from each other in a scheme that has one per
// repository.
const MaxSockPath = 100

// DirFor is the session directory for a repository.
func DirFor(repoDir string) (string, error) {
	id, err := IDFor(repoDir)
	if err != nil {
		return "", err
	}
	return filepath.Join(SessionsDir(), id), nil
}

// SockPath, StatePath and EventsPath name the files in a session directory.
func SockPath(dir string) string { return filepath.Join(dir, "sock") }

// CheckSockPath reports whether the socket for this session directory can be
// bound at all, with a message naming what to shorten.
//
// Separate from Serve so a client can say the same thing: a `pane list` whose
// daemon could never have started should not report "no daemon is running" as if
// starting one would help.
func CheckSockPath(dir string) error {
	sock := SockPath(dir)
	if len(sock) <= MaxSockPath {
		return nil
	}
	return fmt.Errorf("the session socket path is %d bytes and a unix socket address holds about %d:\n  %s\n"+
		"  shorten it by setting XDG_CONFIG_HOME to a shorter directory, or by moving the repository somewhere with a shorter path",
		len(sock), MaxSockPath, sock)
}
func StatePath(dir string) string  { return filepath.Join(dir, "session.json") }
func lockPath(dir string) string   { return filepath.Join(dir, "session.json.lock") }
func EventsPath(dir string) string { return filepath.Join(dir, "events.jsonl") }
func PIDPath(dir string) string    { return filepath.Join(dir, "pid") }

// Open loads the session for the repository containing dir, creating the session
// directory and a fresh catalog if there is none.
//
// The directory is 0700 and everything in it 0600. That is the authentication:
// the socket lives here, and same-uid is the whole check — a caller who can open
// it can already run docker as this user, so a token would be a second secret
// protecting nothing.
func Open(dir string, profile, engine string) (*Server, error) {
	root, err := worktree.RepoRoot(dir)
	if err != nil {
		return nil, fmt.Errorf("a session is scoped to a repository, and %s is not in one: %w", dir, err)
	}
	sdir, err := DirFor(root)
	if err != nil {
		return nil, err
	}
	// 0700 on every level, including the parent: the socket inside is the
	// capability, and a traversable parent is how it becomes reachable.
	if err := os.MkdirAll(sdir, 0o700); err != nil {
		return nil, err
	}
	if err := os.Chmod(sdir, 0o700); err != nil {
		return nil, err
	}

	s := &Server{dir: sdir, root: root}
	switch loaded, err := loadState(StatePath(sdir)); {
	case err == nil:
		s.sess = loaded
	case errors.Is(err, os.ErrNotExist):
		id, _ := IDFor(root)
		s.sess = protocol.Session{
			Version:   protocol.Version,
			ID:        "s_" + id,
			Name:      "default",
			Root:      root,
			Profile:   profile,
			Engine:    engine,
			CreatedAt: time.Now().UTC(),
		}
	default:
		// A catalog that will not parse is kept rather than deleted, and rebuilt
		// from the engine. Keeping it costs a file; deleting it loses the only
		// record of panes whose containers are already gone — which is exactly
		// the content the engine cannot give back.
		bak := StatePath(sdir) + fmt.Sprintf(".bak-%d", time.Now().Unix())
		if rerr := os.Rename(StatePath(sdir), bak); rerr == nil {
			fmt.Fprintf(os.Stderr, "sandbox-cli: %s would not parse (%v)\n"+
				"  kept as %s; the catalog is being rebuilt from the engine's labels,\n"+
				"  so panes whose containers have already gone will be missing from it.\n",
				StatePath(sdir), err, bak)
		}
		id, _ := IDFor(root)
		s.sess = protocol.Session{
			Version:   protocol.Version,
			ID:        "s_" + id,
			Name:      "default",
			Root:      root,
			Profile:   profile,
			Engine:    engine,
			CreatedAt: time.Now().UTC(),
		}
	}
	// The profile and engine a daemon is running under are facts about *now*, not
	// about whenever the catalog was written, so they are refreshed on open.
	if profile != "" {
		s.sess.Profile = profile
	}
	if engine != "" {
		s.sess.Engine = engine
	}
	return s, nil
}

// Dir is the session directory, so callers can name the socket and the pid file
// without rebuilding the path.
func (s *Server) Dir() string { return s.dir }

// Root is the repository this session is about.
func (s *Server) Root() string { return s.root }

// Snapshot returns the catalog. A deep copy, because the caller is usually about
// to marshal it on another goroutine while Adopt is rewriting it.
func (s *Server) Snapshot() protocol.Session {
	s.mu.Lock()
	defer s.mu.Unlock()
	return cloneSession(s.sess)
}

func cloneSession(in protocol.Session) protocol.Session {
	out := in
	out.Workspaces = make([]protocol.Workspace, len(in.Workspaces))
	for i, ws := range in.Workspaces {
		ws.Worktrees = append([]protocol.Worktree(nil), ws.Worktrees...)
		out.Workspaces[i] = ws
	}
	out.Panes = make([]protocol.Pane, len(in.Panes))
	for i, p := range in.Panes {
		if p.ExitCode != nil {
			code := *p.ExitCode
			p.ExitCode = &code
		}
		out.Panes[i] = p
	}
	if in.Focus != nil {
		f := *in.Focus
		out.Focus = &f
	}
	return out
}

// Panes returns the panes matching a listing request.
func (s *Server) Panes(p protocol.PaneListParams) []protocol.Pane {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []protocol.Pane
	for _, pane := range s.sess.Panes {
		if p.Workspace != "" && workspaceOf(s.sess, pane.WorktreeID) != p.Workspace {
			continue
		}
		if p.Worktree != "" && pane.WorktreeID != p.Worktree {
			continue
		}
		if !p.All && !pane.Running() {
			continue
		}
		out = append(out, pane)
	}
	return out
}

func workspaceOf(sess protocol.Session, worktreeID string) string {
	for _, ws := range sess.Workspaces {
		for _, wt := range ws.Worktrees {
			if wt.ID == worktreeID {
				return ws.ID
			}
		}
	}
	return ""
}

// loadState reads a catalog off disk.
func loadState(path string) (protocol.Session, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return protocol.Session{}, err
	}
	var sess protocol.Session
	if err := json.Unmarshal(b, &sess); err != nil {
		return protocol.Session{}, err
	}
	if sess.Version != protocol.Version {
		return protocol.Session{}, fmt.Errorf("session.json is version %d, this sandbox-cli speaks %d", sess.Version, protocol.Version)
	}
	return sess, nil
}

// Save writes the catalog, atomically and under a lock, and appends one line to
// the event log.
//
// The order is load-bearing. Lock, write a temp file, fsync *the file*, rename
// over the target, then append the event. Without the fsync a rename can land
// before the bytes do, which on a crash leaves a zero-length session.json that
// parses as nothing; without the lock two `serve` processes interleave two
// renames and the loser's panes vanish. The event is appended last, so the log
// never claims a write that did not reach disk.
func (s *Server) Save(reason string) error {
	s.mu.Lock()
	s.sess.UpdatedAt = time.Now().UTC()
	snap := cloneSession(s.sess)
	s.mu.Unlock()

	unlock, err := flock(lockPath(s.dir))
	if err != nil {
		return err
	}
	defer unlock()

	b, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return err
	}
	b = append(b, '\n')

	tmp, err := os.CreateTemp(s.dir, "session.json.tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	// Every failure from here removes the temp file: a directory accumulating
	// session.json.tmp-* is how a full disk gets diagnosed as something else.
	defer os.Remove(tmpName)

	if err := tmp.Chmod(0o600); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpName, StatePath(s.dir)); err != nil {
		return err
	}
	s.appendEvent(reason, snap)
	return nil
}

// appendEvent records that the catalog changed. Best-effort and silent on
// failure, the same bargain internal/audit makes: the catalog is what was asked
// for, the log of changes to it is a courtesy.
func (s *Server) appendEvent(reason string, snap protocol.Session) {
	f, err := os.OpenFile(EventsPath(s.dir), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	line, err := json.Marshal(map[string]any{
		"at":     time.Now().UTC().Format(time.RFC3339),
		"event":  "session.save",
		"reason": reason,
		"panes":  len(snap.Panes),
	})
	if err != nil {
		return
	}
	f.Write(append(line, '\n'))
}

// newPaneID mints a pane id. Random rather than sequential: a counter in the
// catalog would have to survive the catalog being rebuilt, and a reused p_3 is a
// client acting on the wrong pane.
func newPaneID() string {
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "p_" + strconv.FormatInt(time.Now().UnixNano(), 36)
	}
	return "p_" + hex.EncodeToString(b[:])
}

// worktreeIDFor derives a worktree's id from its branch, deterministically.
//
// Derived rather than minted so that adoption does not need a label for it: a
// container carries its branch already (`sandbox.branch`), so the same branch
// always lands in the same worktree record, whether it was discovered from a
// container or from git. A minted id would need storing on the container, which
// is a second place for the catalog and the engine to disagree about what a pane
// is in.
func worktreeIDFor(branch string) string {
	if branch == "" {
		return "wt_detached"
	}
	return "wt_" + sanitizeID(branch)
}

func sanitizeID(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '_':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + 32)
		default:
			b.WriteByte('_')
		}
	}
	return b.String()
}

// sortPanes orders a catalog stably: newest first, ties broken by id.
//
// Stable because the catalog is written to disk and diffed by people. An
// unordered marshal produces a different file from the same state, which makes
// every save look like a change.
func sortPanes(panes []protocol.Pane) {
	sort.SliceStable(panes, func(i, j int) bool {
		if !panes[i].CreatedAt.Equal(panes[j].CreatedAt) {
			return panes[i].CreatedAt.After(panes[j].CreatedAt)
		}
		return panes[i].ID < panes[j].ID
	})
}

// paneStateFor decides how a pane is doing from what the container can prove, and
// no further.
//
// The three states this cannot reach — working, blocked, idle — are about the
// agent rather than the container, and only a transcript can tell them apart
// (phase 4, `internal/detect`). A running container therefore reports
// StateUnknown rather than StateWorking: an agent sitting at a permission prompt
// and an agent editing a file are the same running container, and guessing
// "working" would have somebody waiting on a pane that is waiting on them.
func paneStateFor(c runtime.ContainerInfo) (protocol.PaneState, *int) {
	switch c.State {
	case "running", "restarting", "paused":
		return protocol.StateUnknown, nil
	case "created":
		return protocol.StateStarting, nil
	case "exited", "dead":
		code := c.ExitCode
		if code == 0 {
			return protocol.StateDone, &code
		}
		return protocol.StateFailed, &code
	}
	return protocol.StateUnknown, nil
}

// paneKindFor reads a pane's kind off the container, falling back to what the
// older labels imply.
//
// The fallback is the whole reason adoption works on containers this daemon never
// started: a fleet container has `sandbox.verify`, an agent container has
// `sandbox.agent`, and `run --` has neither. None of that is as good as the
// label phase 2 stamps, which is why the label exists — but it is better than
// reporting every pre-existing container as an unknown kind.
func paneKindFor(labels map[string]string) protocol.PaneKind {
	if k := labels[sandbox.LabelPaneKind]; k != "" {
		return protocol.PaneKind(k)
	}
	if labels[sandbox.LabelAgent] != "" {
		return protocol.PaneAgent
	}
	return protocol.PaneCommand
}

// Adopt rebuilds the catalog from the engine.
//
// This is the operation that makes a restart survivable, and the rule it keeps is
// that the **engine is the truth about containers**. Every pane already in the
// catalog is checked against what the engine reports: found and running, its
// container id and state are refreshed; found and exited, it records how it
// ended; not found at all, it becomes stopped and *stays in the catalog*, because
// what ran is worth knowing after it stops running and the engine is precisely
// the thing that can no longer say.
//
// Containers the catalog does not know are added. Two kinds arrive that way, and
// the difference matters to a caller: one carries a pane label, so it belongs to
// a session whose catalog was lost, and one does not, so it was started before
// any of this existed. The second is marked Legacy and its id is its container
// name, because minting a `p_` id for it would name something no engine has heard
// of — and a mutation addressed that way is refused rather than silently
// applied to nothing.
func (s *Server) Adopt(ctx context.Context, l Lister) error {
	repoID, err := worktree.RepoID(s.root)
	if err != nil {
		return err
	}
	// Scoped to this repository, which is the same filter `sandbox-cli list` uses
	// — so the two are comparable, which is what the phase's acceptance asks for.
	// Not scoped to this *session*: a container from a session whose catalog was
	// lost is still this repository's, and leaving it out would make `pane list`
	// quieter than `list` while claiming to be the catalog.
	found, err := l.Containers(ctx, map[string]string{
		sandbox.LabelCLI:  "1",
		sandbox.LabelRepo: repoID,
	})
	if err != nil {
		return fmt.Errorf("asking %s what is running: %w", s.sess.Engine, err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	// Index the engine's answer by the two things a pane can be joined on.
	byPane := map[string]runtime.ContainerInfo{}
	byName := map[string]runtime.ContainerInfo{}
	for _, c := range found {
		if id := c.Labels[sandbox.LabelPane]; id != "" {
			byPane[id] = c
		}
		byName[c.Name] = c
	}

	claimed := map[string]bool{} // container names this pass has accounted for
	var panes []protocol.Pane

	// 1. Existing panes, rebound or stopped.
	for _, pane := range s.sess.Panes {
		c, ok := byPane[pane.ID]
		if !ok {
			// Fall back to the name, for a pane recorded before the label existed.
			c, ok = byName[pane.ContainerName]
		}
		if !ok {
			// The container is gone. The pane is not: this is the one fact the
			// catalog holds that the engine cannot give back.
			pane.State = protocol.StateStopped
			pane.ContainerID = ""
			pane.UpdatedAt = time.Now().UTC()
			panes = append(panes, pane)
			continue
		}
		claimed[c.Name] = true
		pane.ContainerID = c.ID
		pane.ContainerName = c.Name
		pane.State, pane.ExitCode = paneStateFor(c)
		pane.UpdatedAt = time.Now().UTC()
		panes = append(panes, pane)
	}

	// 2. Containers the catalog has never seen.
	for _, c := range found {
		if claimed[c.Name] {
			continue
		}
		pane := paneFromContainer(c)
		panes = append(panes, pane)
	}

	sortPanes(panes)
	s.sess.Panes = panes
	s.sess.Workspaces = s.workspacesFor(repoID, panes)
	return nil
}

// paneFromContainer builds a pane for a container the catalog did not know about.
func paneFromContainer(c runtime.ContainerInfo) protocol.Pane {
	state, code := paneStateFor(c)
	id := c.Labels[sandbox.LabelPane]
	legacy := id == ""
	if legacy {
		// The container name, not a minted id. A `p_` id for a container carrying
		// no pane label would address nothing, and a client that used it to kill
		// something would be told it succeeded.
		id = c.Name
	}
	created := c.CreatedAt
	if created.IsZero() {
		created = time.Now().UTC()
	}
	return protocol.Pane{
		ID:            id,
		WorktreeID:    worktreeIDFor(c.Labels[sandbox.LabelBranch]),
		Kind:          paneKindFor(c.Labels),
		Agent:         c.Labels[sandbox.LabelAgent],
		Sandbox:       c.Labels[sandbox.LabelSandbox],
		ContainerName: c.Name,
		ContainerID:   c.ID,
		State:         state,
		ExitCode:      code,
		// The agent conversation, when the run recorded one — which is only the
		// resumed case, since that is the only one where it is known rather than
		// inferred.
		ConversationID: c.Labels[sandbox.LabelSession],
		LastSnapshot:   c.Labels[sandbox.LabelBaseline],
		Legacy:         legacy,
		CreatedAt:      created.UTC(),
		UpdatedAt:      time.Now().UTC(),
	}
}

// workspacesFor builds the grouping: one workspace for this repository, and a
// worktree record for every branch a pane is on plus every worktree git knows
// about.
//
// Git is asked, and what it says is not overridden: a worktree that exists with
// no pane in it is real and is listed, and a branch that only a container
// mentions gets a record too — a pane has to hang off something, and the
// container is evidence that the branch was checked out at least once. What is
// never done is inventing a *path* for a worktree git has not heard of, because a
// path is the thing a later command would try to mount.
func (s *Server) workspacesFor(repoID string, panes []protocol.Pane) []protocol.Workspace {
	ws := protocol.Workspace{
		ID:       "ws_" + sanitizeID(repoID),
		Label:    filepath.Base(s.root),
		RepoPath: s.root,
		RepoHash: repoID,
	}
	seen := map[string]bool{}
	add := func(wt protocol.Worktree) {
		if seen[wt.ID] {
			return
		}
		seen[wt.ID] = true
		ws.Worktrees = append(ws.Worktrees, wt)
	}

	if branch := worktree.Branch(s.root); branch != "" {
		add(protocol.Worktree{
			ID:     worktreeIDFor(branch),
			Branch: branch,
			Path:   s.root,
			Main:   true,
		})
	}
	// What git says, and only what git says. An error here is left to speak for
	// itself by absence: a repository whose worktrees cannot be listed still has
	// panes, and refusing the whole catalog because the grouping is incomplete
	// would make the common failure (a worktree directory deleted by hand) fatal.
	managed, _ := worktree.List(s.root)
	for _, wt := range managed {
		add(protocol.Worktree{
			ID:     worktreeIDFor(wt.Branch),
			Branch: wt.Branch,
			Path:   wt.Path,
		})
	}
	// Branches only a container mentions: a record with no path, which is the
	// honest shape — the worktree may have been removed since the container
	// started, and guessing a path is how a later command mounts the wrong one.
	for _, p := range panes {
		if !seen[p.WorktreeID] {
			add(protocol.Worktree{ID: p.WorktreeID, Branch: branchOfID(p.WorktreeID)})
		}
	}
	sort.SliceStable(ws.Worktrees, func(i, j int) bool {
		if ws.Worktrees[i].Main != ws.Worktrees[j].Main {
			return ws.Worktrees[i].Main
		}
		return ws.Worktrees[i].Branch < ws.Worktrees[j].Branch
	})
	return []protocol.Workspace{ws}
}

// branchOfID is the inverse of worktreeIDFor, and it is lossy: sanitizeID maps
// several characters onto "_", so this reconstructs a label to show rather than a
// branch name to act on. Nothing resolves a branch from it.
func branchOfID(id string) string {
	return strings.TrimPrefix(id, "wt_")
}

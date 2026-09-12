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
	"crypto/sha256"
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

	"github.com/Amitgb14/sandbox-cli/internal/agentctx"
	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/detect"
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

// Transcripts finds an agent's conversation for a pane, so its state can be
// decided from something better than the container's.
//
// A function rather than a concrete lookup for two reasons. It does file I/O — and
// `Adopt` is otherwise pure given a Lister, which is what lets the adoption table be
// tested without a disk. And correlating a container to a conversation is
// `agentctx`'s problem, already solved there with rules this package should not
// restate.
//
// Returning nothing is normal and is not an error: a pane with no agent has no
// conversation, an agent with no verified store has none to read, and two
// conversations in one window are an ambiguity `agentctx` declines rather than
// guesses. Each of those lands as `unknown`, which is an answer.
type Transcripts func(pane protocol.Pane, project string) []agentctx.Message

// Server is one session: one repository, one socket, one catalog.
type Server struct {
	dir  string // ~/.config/sandbox/sessions/<sid>
	root string // the repository this session is for

	// Launcher is what actually starts containers, and nil means this daemon can
	// catalog them and not create them — which is what `pane.spawn` refuses on
	// rather than improvising. Separate from the catalog because the two have
	// genuinely different lifetimes: a `serve` in a directory with no usable engine
	// is still useful for reading, and a read-only client has no business holding
	// something that can launch.
	Launcher *sandbox.Session

	// Keeper is the crash safety net for this session's panes. Nil means none —
	// which is what every caller but the daemon wants, since a one-shot command that
	// started a snapshot loop would have nothing to run it.
	Keeper *Keeper

	// Transcripts is how a pane's conversation is found. Nil means none is looked
	// for, and every running pane then reports `unknown` — which is exactly what
	// this catalog did before agent state existed, so a caller that does not set it
	// loses nothing it had.
	Transcripts Transcripts

	mu   sync.Mutex
	sess protocol.Session
	// lastSaved is the fingerprint of the catalog as last written, so a refresh
	// that found nothing new writes nothing. Empty means "nothing written yet",
	// which is correct after Open: the file on disk may match, but this process has
	// not proven it and a first save costs one write.
	lastSaved string
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
	// worktree.MainRepo, not RepoRoot: `rev-parse --show-toplevel` answers with a
	// linked worktree's *own* directory, and the session id comes from
	// worktree.RepoID, which follows the pointer back to the main checkout. Using
	// both meant one session directory whose Root was whichever directory the
	// command last ran in — so `serve` from inside a managed worktree recorded the
	// worktree as the repository, flagged it `Main: true` (the one worktree
	// `worktree rm` must never touch), lost the real checkout from the catalog, and
	// found no managed worktrees at all, since worktreeBase is computed from the
	// main root. Two invocations from different directories then overwrote each
	// other's Root in one file.
	//
	// One repository, one session, and every branch of it agrees which — which is
	// the same property RepoID exists to give the container labels.
	root := worktree.MainRepo(dir)
	if root == "" {
		return nil, fmt.Errorf("a session is scoped to a repository, and %s is not in one", dir)
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

// WorktreeDir is the host directory a pane is working in, or "" when the catalog
// cannot say.
//
// Read from the catalog's own worktree records rather than derived from the branch,
// because those come from git and from the containers themselves — and a branch a
// container mentions whose worktree has since been removed deliberately has no path.
// Returning "" there is the point: a caller about to write into that directory must
// not be handed a guess.
func (s *Server) WorktreeDir(pane protocol.Pane) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, ws := range s.sess.Workspaces {
		for _, wt := range ws.Worktrees {
			if wt.ID == pane.WorktreeID {
				return wt.Path
			}
		}
	}
	return ""
}

// Panes returns the panes matching a listing request.
func (s *Server) Panes(p protocol.PaneListParams) []protocol.Pane {
	s.mu.Lock()
	defer s.mu.Unlock()
	return FilterPanes(s.sess, p)
}

// FilterPanes applies a listing request to a catalog.
//
// Exported because there are two callers and one rule: the daemon answering
// pane.list, and a client that read the catalog itself because no daemon was
// running. A second copy of the filter is how those two came to answer different
// questions — the client's honoured `All` and silently ignored the workspace and
// worktree filters.
func FilterPanes(sess protocol.Session, p protocol.PaneListParams) []protocol.Pane {
	var out []protocol.Pane
	for _, pane := range sess.Panes {
		if p.Workspace != "" && workspaceOf(sess, pane.WorktreeID) != p.Workspace {
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

// SaveIfChanged writes the catalog only when it differs from the last write.
//
// The fingerprint is of the catalog *minus its timestamps*, which is the whole point:
// `UpdatedAt` and every pane's own move on each refresh, so comparing the marshalled
// whole would find a difference every time and save every time. What a reader cares
// about is whether a pane appeared, went away or changed state.
func (s *Server) SaveIfChanged(reason string) error {
	s.mu.Lock()
	fp := fingerprint(s.sess)
	unchanged := fp == s.lastSaved
	s.mu.Unlock()
	if unchanged {
		return nil
	}
	if err := s.Save(reason); err != nil {
		return err
	}
	s.mu.Lock()
	s.lastSaved = fp
	s.mu.Unlock()
	return nil
}

// fingerprint is what "changed" means for a catalog: the panes that exist and how
// they are, plus the worktrees they hang off. Deliberately not the timestamps.
func fingerprint(sess protocol.Session) string {
	var b strings.Builder
	for _, ws := range sess.Workspaces {
		b.WriteString(ws.ID)
		b.WriteByte(';')
		for _, wt := range ws.Worktrees {
			b.WriteString(wt.ID + "=" + wt.Branch + "@" + wt.Path + ",")
		}
		b.WriteByte('|')
	}
	for _, p := range sess.Panes {
		b.WriteString(p.ID + "=" + string(p.State) + "/" + p.ContainerID + "/" + string(p.Kind))
		if p.ExitCode != nil {
			b.WriteString(":" + strconv.Itoa(*p.ExitCode))
		}
		b.WriteByte(',')
	}
	return b.String()
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

	// Read-modify-write, inside the lock, and this is the part that took a second
	// writer to notice.
	//
	// `Save` wrote the whole catalog from a copy taken at `Open`, so two processes
	// each doing Open → Spawn → Save would clobber one another: the second's view
	// predates the first's write, and the first's pane vanishes. The lock serialises
	// the writes and does nothing about the lost update.
	//
	// It was narrow while every writer was short-lived — `sandbox-cli claude
	// --detach` opens, appends and exits — and self-healing for *running* panes,
	// since the pane id is a label and the next `Adopt` recovers the row from the
	// engine. What it could lose permanently is a **stopped** pane, which is the one
	// thing the catalog holds that the engine cannot give back.
	//
	// It stops being narrow the moment a long-lived process is also a writer, which
	// is what `internal/studioapi` becomes: its in-memory copy would be hours old.
	if onDisk, lerr := loadState(StatePath(s.dir)); lerr == nil {
		snap = mergeCatalogs(onDisk, snap)
		s.mu.Lock()
		s.sess = cloneSession(snap)
		s.mu.Unlock()
	}

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

// mergeCatalogs combines the catalog on disk with this process's own view.
//
// The rule follows from where authority actually lies. Panes are the **union**, keyed
// by id, and the newer `UpdatedAt` wins — because a pane another writer knows about is
// a real container, and dropping it is the lost update this exists to prevent. The
// grouping is taken from *ours*, because workspaces and worktrees are derived from git
// and the engine on every `Adopt`: a merge of two derivations is not more true than
// the fresher one.
//
// Session identity (id, name, root) is ours too. Two writers for one repository agree
// on all three by construction — the id is a function of the repository — so there is
// nothing to reconcile and a disagreement would mean something worse than a stale
// read.
func mergeCatalogs(onDisk, ours protocol.Session) protocol.Session {
	out := ours
	byID := make(map[string]protocol.Pane, len(ours.Panes)+len(onDisk.Panes))
	order := make([]string, 0, len(ours.Panes)+len(onDisk.Panes))
	add := func(p protocol.Pane) {
		prev, seen := byID[p.ID]
		if !seen {
			order = append(order, p.ID)
			byID[p.ID] = p
			return
		}
		// Newer wins. Equal timestamps keep what is already there, which makes the
		// merge stable rather than dependent on argument order.
		if p.UpdatedAt.After(prev.UpdatedAt) {
			byID[p.ID] = p
		}
	}
	for _, p := range onDisk.Panes {
		add(p)
	}
	for _, p := range ours.Panes {
		add(p)
	}
	out.Panes = make([]protocol.Pane, 0, len(order))
	for _, id := range order {
		out.Panes = append(out.Panes, byID[id])
	}
	sortPanes(out.Panes)

	// A pane adopted from the other writer may be on a worktree this view has no
	// record of, and a pane that renders under nothing is the failure the ids exist
	// to prevent. Fill those in from the disk copy rather than inventing them.
	if len(out.Workspaces) > 0 {
		known := map[string]bool{}
		for _, wt := range out.Workspaces[0].Worktrees {
			known[wt.ID] = true
		}
		var missing []protocol.Worktree
		for _, ws := range onDisk.Workspaces {
			for _, wt := range ws.Worktrees {
				if !known[wt.ID] {
					known[wt.ID] = true
					missing = append(missing, wt)
				}
			}
		}
		out.Workspaces[0].Worktrees = append(out.Workspaces[0].Worktrees, missing...)
	} else {
		out.Workspaces = onDisk.Workspaces
	}
	return out
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
	id := sanitizeID(branch)
	if id == branch {
		// Already safe, so the id *is* the branch and nothing is lost. This is the
		// common case — `feat`, `main`, `release_2` — and it keeps the id readable.
		return "wt_" + id
	}
	// Sanitising lost information, so a suffix puts it back. Without this,
	// `live-one` and `live_one` produced the same id (and so did `Feat` and `feat`,
	// since sanitizeID lowercases) — two real branches sharing one worktree record,
	// whose reported name was then whichever container the engine happened to list
	// last: a wrong name that looked authoritative, and one that changed between two
	// refreshes of unchanged state.
	//
	// Eight hex of the branch rather than four: this is a map key, and the cost of a
	// collision is two branches' panes grouped under one worktree, which is the
	// failure being fixed.
	sum := sha256.Sum256([]byte(branch))
	return "wt_" + id + "_" + hex.EncodeToString(sum[:])[:8]
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

// stateOf decides how a pane is doing, using its conversation when there is one to
// read.
//
// Two layers, and the order is the order of certainty. `internal/detect` is asked
// first because it knows everything the container knows *and* what the transcript
// says; with no transcript it answers exactly what the container alone proves, which
// is what `paneStateFor` has always answered. So the fallback is not a different
// rule — it is the same rule with less evidence, and a caller that sets no
// Transcripts keeps precisely the behaviour it had.
func (s *Server) stateOf(c runtime.ContainerInfo, pane protocol.Pane) (protocol.PaneState, *int) {
	state, code := paneStateFor(c)
	if s.Transcripts == nil || c.Finished() {
		// Nothing to add. A finished container's state is an exit code, and no
		// conversation outranks that.
		return state, code
	}
	msgs := s.Transcripts(pane, s.root)
	if len(msgs) == 0 {
		return state, code
	}
	return detect.From(detect.Evidence{
		ContainerState: c.State,
		ExitCode:       c.ExitCode,
		// Whether anything can type at this pane, read back from the container
		// rather than inferred from its kind: a console run is one docker recorded
		// with an open stdin, and that is the fact `detect` needs to tell "waiting
		// for a human" from "quiet".
		OpenStdin:  c.OpenStdin,
		Transcript: msgs,
		Now:        time.Now(),
	}), code
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
			// Fall back to the name — but only onto a container that is not somebody
			// else's, and only once.
			//
			// The container name is deterministic (`sandbox-<repo>-<branch>`), so it
			// is **reused** by the next run on that branch. A bare name lookup
			// therefore bound a finished pane to the container that replaced it:
			// `p_one` reported running against `p_two`'s container, still carrying
			// `p_one`'s agent, while `p_two` never entered the catalog at all — and
			// a phase-2 mutation by pane id would then act on the wrong container.
			//
			// So a container that names a *different* pane is not a match, and
			// `claimed` is consulted so two catalog panes cannot both take one
			// container. What is left over is picked up as a new pane below, which is
			// where it belongs.
			if cand, found := byName[pane.ContainerName]; found && !claimed[cand.Name] {
				if label := cand.Labels[sandbox.LabelPane]; label == "" || label == pane.ID {
					c, ok = cand, true
				}
			}
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
		pane.State, pane.ExitCode = s.stateOf(c, pane)
		pane.UpdatedAt = time.Now().UTC()
		panes = append(panes, pane)
	}

	// 2. Containers the catalog has never seen.
	for _, c := range found {
		if claimed[c.Name] {
			continue
		}
		pane := paneFromContainer(c)
		pane.State, pane.ExitCode = s.stateOf(c, pane)
		panes = append(panes, pane)
	}

	// The exact branch name per worktree id, taken from the container's own label.
	// Built here because this is where the containers are in hand, and the
	// alternative is reversing worktreeIDFor — which cannot be done: sanitizeID maps
	// several characters onto "_", so `live-one` and `live_one` produce the same id,
	// and the reconstruction reported `live_one` for a branch actually called
	// `live-one`.
	// The exact branch name per worktree id, taken from the container's own label.
	// Built here because this is where the containers are in hand, and the
	// alternative is reversing worktreeIDFor, which cannot be done: sanitizeID maps
	// several characters onto "_" and lowercases, so the reconstruction reported
	// `live_one` for a branch actually called `live-one`.
	//
	// No collision handling, and that is a property of worktreeIDFor rather than an
	// assumption: an id whose sanitised form lost information carries a hash of the
	// branch, so distinct branches have distinct ids and this map cannot be
	// overwritten by a different name.
	branches := map[string]string{}
	for _, c := range found {
		if b := c.Labels[sandbox.LabelBranch]; b != "" {
			branches[worktreeIDFor(b)] = b
		}
	}

	sortPanes(panes)
	s.sess.Panes = panes
	s.sess.Workspaces = s.workspacesFor(repoID, panes, branches)
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
func (s *Server) workspacesFor(repoID string, panes []protocol.Pane, branches map[string]string) []protocol.Workspace {
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
		if seen[p.WorktreeID] {
			continue
		}
		// The container's own label first. It carries the branch exactly as git has
		// it, where the id has been through sanitizeID and cannot be turned back.
		branch, ok := branches[p.WorktreeID]
		if !ok {
			branch = branchOfID(p.WorktreeID)
		}
		add(protocol.Worktree{ID: p.WorktreeID, Branch: branch})
	}
	sort.SliceStable(ws.Worktrees, func(i, j int) bool {
		if ws.Worktrees[i].Main != ws.Worktrees[j].Main {
			return ws.Worktrees[i].Main
		}
		return ws.Worktrees[i].Branch < ws.Worktrees[j].Branch
	})
	return []protocol.Workspace{ws}
}

// branchOfID is the last resort, and it is lossy: worktreeIDFor sanitises and may
// append a hash, so this reconstructs something to *show* rather than a branch name
// to act on. Nothing resolves a branch from it.
//
// It is reached only for a worktree id with no container to read a branch off and no
// record of one in the catalog — in practice a catalog entry that outlived every
// container mentioning it, since both git and a container supply the real name.
func branchOfID(id string) string {
	return strings.TrimPrefix(id, "wt_")
}

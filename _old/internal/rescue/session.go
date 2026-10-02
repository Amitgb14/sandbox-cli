package rescue

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// Outcome values recorded on a finished run. An empty Outcome with no EndedAt is
// the interesting one: nothing closed the manifest, so the run did not end — it
// died.
const (
	OutcomeClean     = "clean"
	OutcomeSignalled = "signalled"
	OutcomeFailed    = "failed" // sandbox-cli itself errored before/while running
)

// Session is the manifest for one sandbox run: enough to answer "what was I
// working on, and on which branch?" after the repository itself has become
// unreadable. It lives outside every repository, under ~/.config/sandbox/rescue,
// for exactly that reason.
type Session struct {
	ID          string     `json:"id"`
	Repo        string     `json:"repo"`      // main repository root
	Workspace   string     `json:"workspace"` // what was mounted at /workspace
	Branch      string     `json:"branch"`
	Agent       string     `json:"agent,omitempty"`
	HeadAtStart string     `json:"head_at_start,omitempty"`
	Ref         string     `json:"ref"`
	StartedAt   time.Time  `json:"started_at"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`
	Outcome     string     `json:"outcome,omitempty"`
	ExitCode    *int       `json:"exit_code,omitempty"`

	Snapshots      int        `json:"snapshots"`
	LastSnapshot   string     `json:"last_snapshot,omitempty"`
	LastSnapshotAt *time.Time `json:"last_snapshot_at,omitempty"`

	path string // manifest file on disk; not serialized
}

// Crashed reports whether the run ended without anything closing its manifest —
// a SIGKILL, a panic, a lost daemon, a power cut. This is the state `recover`
// leads with, because it is the state the user is standing in when they run it.
func (s Session) Crashed() bool { return s.EndedAt == nil }

// Activity is the last moment this session is known to have been alive, used for
// display and for retention.
func (s Session) Activity() time.Time {
	switch {
	case s.EndedAt != nil:
		return *s.EndedAt
	case s.LastSnapshotAt != nil:
		return *s.LastSnapshotAt
	default:
		return s.StartedAt
	}
}

// Status renders the outcome for display.
func (s Session) Status() string {
	switch {
	case s.Crashed():
		return "crashed"
	case s.Outcome == OutcomeSignalled:
		return "interrupted"
	case s.ExitCode != nil && *s.ExitCode != 0:
		return fmt.Sprintf("exit %d", *s.ExitCode)
	case s.Outcome == OutcomeFailed:
		return "failed"
	default:
		return "clean"
	}
}

// newSessionID is sortable-by-time and collision-proof between two sandboxes
// started in the same second, since it also names the ref.
func newSessionID(now time.Time) string {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A failed read is not worth failing a run over; the timestamp alone is
		// near-unique and a collision only costs one refused ref update.
		return now.UTC().Format("20060102-150405")
	}
	return now.UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
}

// sessionsDir is where one repository's manifests live, under the same
// repository identity that names its managed worktrees and its containers — so
// everything sandbox-cli keeps about a repository agrees by construction.
//
// repoRoot has already been resolved by MainRepoRoot, so RepoID only fails for a
// repository that has since been deleted. The fallback then costs nothing but a
// stale index directory left behind for a project that no longer exists.
func sessionsDir(repoRoot string) string {
	root := config.RescueDir()
	if root == "" {
		root = filepath.Join(os.TempDir(), "sandbox", "rescue")
	}
	id, err := worktree.RepoID(repoRoot)
	if err != nil {
		id = filepath.Base(repoRoot)
	}
	return filepath.Join(root, id)
}

// Save writes the manifest atomically: a crash mid-write must not leave a
// half-parsed JSON file where the record of the user's work should be.
func (s *Session) Save() error {
	if s.path == "" {
		s.path = filepath.Join(sessionsDir(s.Repo), s.ID+".json")
	}
	if err := os.MkdirAll(filepath.Dir(s.path), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// loadSession reads one manifest file.
func loadSession(path string) (Session, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Session{}, err
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return Session{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	s.path = path
	return s, nil
}

// Sessions returns the recorded sessions for one repository, newest activity
// first. A manifest that fails to parse is skipped rather than failing the
// listing: one corrupt file must not hide every other recoverable run.
func Sessions(repoRoot string) ([]Session, error) {
	return sessionsIn(sessionsDir(repoRoot))
}

// AllSessions returns the recorded sessions for every repository, newest first.
func AllSessions() ([]Session, error) {
	root := config.RescueDir()
	if root == "" {
		return nil, nil
	}
	buckets, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var all []Session
	for _, b := range buckets {
		if !b.IsDir() {
			continue
		}
		in, err := sessionsIn(filepath.Join(root, b.Name()))
		if err != nil {
			continue
		}
		all = append(all, in...)
	}
	sortSessions(all)
	return all, nil
}

func sessionsIn(dir string) ([]Session, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Session
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		s, err := loadSession(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		out = append(out, s)
	}
	sortSessions(out)
	return out, nil
}

func sortSessions(s []Session) {
	sort.Slice(s, func(i, j int) bool { return s[i].Activity().After(s[j].Activity()) })
}

// findSession resolves a user-typed session id against one repository. An
// unambiguous prefix is enough — the full id is a timestamp plus six hex
// characters and nobody should have to type it.
func findSession(repoRoot, id string) (Session, error) {
	all, err := Sessions(repoRoot)
	if err != nil {
		return Session{}, err
	}
	var hits []Session
	for _, s := range all {
		if s.ID == id {
			return s, nil
		}
		if strings.HasPrefix(s.ID, id) {
			hits = append(hits, s)
		}
	}
	switch len(hits) {
	case 0:
		return Session{}, fmt.Errorf("no snapshot session %q for this repository (see: sandbox-cli recover list)", id)
	case 1:
		return hits[0], nil
	default:
		return Session{}, fmt.Errorf("session id %q is ambiguous (%d matches); use the full id", id, len(hits))
	}
}

// remove deletes a session's manifest and its private snapshot index.
func (s Session) remove() {
	if s.path != "" {
		os.Remove(s.path)
	}
	os.RemoveAll(indexDir(s.Repo, s.ID))
}

// indexDir holds the private index file for one session's snapshots. It is kept
// out of the repository deliberately: the whole mechanism rests on git never
// being pointed at the user's real index.
func indexDir(repoRoot, sessionID string) string {
	return filepath.Join(sessionsDir(repoRoot), sessionID+".index.d")
}

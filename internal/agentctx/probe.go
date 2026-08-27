package agentctx

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/config"
)

// State is what a probe concluded about one agent's session store.
const (
	StateVerified  = "verified"  // the directory exists and holds sessions
	StateEmpty     = "empty"     // the directory exists but holds no sessions yet
	StateMissing   = "missing"   // no candidate directory exists (agent never run?)
	StateUntracked = "untracked" // sandbox-cli has no descriptor for this agent
)

// Location is one directory a probe actually found on disk.
type Location struct {
	Root     string    `json:"root"` // RootAgent or RootHome
	Dir      string    `json:"dir"`
	Sessions int       `json:"sessions"`
	Newest   time.Time `json:"newest,omitempty"`
}

// Finding is the verified truth about one agent's session store on this machine.
// It is what gets persisted: later commands read a Finding instead of re-deriving
// a path, so a layout is discovered once and then simply known.
type Finding struct {
	Agent  string `json:"agent"`
	Format string `json:"format,omitempty"`
	State  string `json:"state"`

	// Dir is the store to use: the location with the most recent activity when
	// more than one exists. The claude wrapper genuinely has two (the host's own
	// history, and the persisted agent HOME used with --no-sync), so Locations
	// keeps the full picture rather than hiding the one that lost.
	Dir       string     `json:"dir,omitempty"`
	Root      string     `json:"root,omitempty"`
	Locations []Location `json:"locations,omitempty"`

	Sessions int       `json:"sessions"`
	Newest   time.Time `json:"newest,omitempty"`

	Resume     []string `json:"resume,omitempty"`
	MintIDFlag string   `json:"mint_id_flag,omitempty"`
	Note       string   `json:"note,omitempty"`

	// FirstSeen is when this store was first verified, VerifiedAt the last time
	// it was seen holding sessions, and CheckedAt the last probe of any outcome.
	// Stale marks a Finding kept from an earlier probe because the latest one
	// could not see the directory — the record is still the best answer we have,
	// but it is no longer first-hand.
	FirstSeen  time.Time `json:"first_seen,omitempty"`
	VerifiedAt time.Time `json:"verified_at,omitempty"`
	CheckedAt  time.Time `json:"checked_at,omitempty"`
	Stale      bool      `json:"stale,omitempty"`
}

// Roots resolves the two home directories a candidate path can hang off. It is a
// struct rather than direct calls into config so that tests can probe a fixture
// tree instead of the developer's real home.
type Roots struct {
	Home  string
	Agent func(agent string) string
}

// DefaultRoots is the real machine: the user's home, and the sandbox-owned
// per-agent state directory that persists an agent's login across containers.
func DefaultRoots() Roots {
	home, err := os.UserHomeDir()
	if err != nil {
		home = ""
	}
	return Roots{Home: home, Agent: config.AgentStateDir}
}

// resolve turns a candidate into an absolute path, or "" when its root is
// unavailable (no home directory, or an agent with no state dir).
func (r Roots) resolve(agent string, c Candidate) string {
	var base string
	switch c.Root {
	case RootHome:
		base = r.Home
	case RootAgent:
		if r.Agent != nil {
			base = r.Agent(agent)
		}
	}
	if base == "" {
		return ""
	}
	return filepath.Join(base, filepath.FromSlash(c.Rel))
}

// Probe looks for this agent's store on disk and reports what is actually there.
// It never fails: a missing directory, an unreadable one, and an agent that has
// never been run are all findings, not errors — the whole point is to be able to
// say "not here" with the same confidence as "here".
func (s Store) Probe(r Roots, now time.Time) Finding {
	f := Finding{
		Agent:      s.Agent,
		Format:     s.Format,
		State:      StateMissing,
		Resume:     s.Resume,
		MintIDFlag: s.MintIDFlag,
		Note:       s.Note,
		CheckedAt:  now,
	}
	for _, c := range s.Candidates {
		dir := r.resolve(s.Agent, c)
		if dir == "" {
			continue
		}
		fi, err := os.Stat(dir)
		if err != nil || !fi.IsDir() {
			continue
		}
		n, newest := countSessions(dir, s, s.MaxDepth)
		f.Locations = append(f.Locations, Location{Root: c.Root, Dir: dir, Sessions: n, Newest: newest})
	}
	if len(f.Locations) == 0 {
		return f
	}

	// Pick the live one. "Most sessions" would be wrong: a user who switched to
	// --no-sync yesterday has a large stale store and a small current one, and it
	// is the recent one they mean.
	best := f.Locations[0]
	for _, l := range f.Locations[1:] {
		if l.Newest.After(best.Newest) {
			best = l
		}
	}
	f.Dir, f.Root, f.Sessions, f.Newest = best.Dir, best.Root, best.Sessions, best.Newest
	if best.Sessions > 0 {
		f.State = StateVerified
		f.VerifiedAt = now
		f.FirstSeen = now
		return f
	}
	f.State = StateEmpty
	return f
}

// countSessions counts the session files under dir, no deeper than maxDepth
// directory levels, and returns the newest modification time among them. The
// depth bound is what keeps a probe cheap and keeps it away from things that
// merely look like sessions: for claude it stops before the per-session
// subagent transcripts, which are sidechains of a session rather than sessions.
func countSessions(dir string, s Store, maxDepth int) (int, time.Time) {
	var n int
	var newest time.Time
	_ = walkSessionFiles(dir, s.Glob, s.SubDir, maxDepth, func(_ string, info fs.FileInfo) {
		n++
		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	})
	return n, newest
}

// walkSessionFiles calls fn for every session file in a store. Probing and
// listing share it so that a session which counts towards "33 sessions" is
// exactly a session that will appear in the listing — two traversals with
// slightly different rules would be a bug waiting to be reported as one.
func walkSessionFiles(dir, glob, subDir string, maxDepth int, fn func(path string, info fs.FileInfo)) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// An unreadable subtree is skipped, not fatal: one bad directory must
			// not turn a store that plainly exists into "missing".
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		rel, rerr := filepath.Rel(dir, p)
		if rerr != nil {
			return nil
		}
		if d.IsDir() {
			if rel == "." {
				return nil
			}
			if depthOf(rel) > maxDepth {
				return fs.SkipDir
			}
			return nil
		}
		if ok, _ := filepath.Match(glob, d.Name()); !ok {
			return nil
		}
		if subDir != "" && filepath.Base(filepath.Dir(p)) != subDir {
			return nil // a file that looks like a session but is not kept with them
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		fn(p, info)
		return nil
	})
}

// depthOf counts the path components in a relative path.
func depthOf(rel string) int {
	if rel == "" || rel == "." {
		return 0
	}
	return len(strings.Split(filepath.ToSlash(rel), "/"))
}

// CandidateDirs lists the places a probe would look for an agent's sessions. It
// exists so that "not found" can name where it looked: a user whose sessions are
// somewhere else entirely can only tell us so if we say where we searched.
func CandidateDirs(agent string, r Roots) []string {
	s, ok := Lookup(agent)
	if !ok {
		return nil
	}
	var out []string
	for _, c := range s.Candidates {
		if dir := r.resolve(agent, c); dir != "" {
			out = append(out, dir)
		}
	}
	return out
}

// ProbeAll probes every known store, in table order.
func ProbeAll(r Roots, now time.Time) []Finding {
	out := make([]Finding, 0, len(stores))
	for _, s := range stores {
		out = append(out, s.Probe(r, now))
	}
	return out
}

// Untracked is the Finding for an adapter sandbox-cli has no store descriptor
// for. It exists so that listing can name those agents instead of omitting them:
// "sandbox-cli does not know where cursor keeps sessions" is information, and
// silence is not.
func Untracked(agent string, now time.Time) Finding {
	return Finding{Agent: agent, State: StateUntracked, CheckedAt: now}
}

// MarshalJSON omits timestamps that were never set. A store that has never been
// verified has no verified-at date, and Go's zero time would put one there:
// "0001-01-01T00:00:00Z" is a real-looking date to anything reading this file,
// and `omitempty` does not apply to a struct like time.Time. Absent is the honest
// encoding of unknown.
func (f Finding) MarshalJSON() ([]byte, error) {
	type alias Finding // shed this method, so encoding/json does the real work
	return dropZeroTimes(alias(f), "newest", "first_seen", "verified_at", "checked_at")
}

// MarshalJSON omits an unset newest-session time, for the same reason.
func (l Location) MarshalJSON() ([]byte, error) {
	type alias Location
	return dropZeroTimes(alias(l), "newest")
}

func dropZeroTimes(v any, keys ...string) ([]byte, error) {
	data, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, err
	}
	const zero = `"0001-01-01T00:00:00Z"`
	for _, k := range keys {
		if string(m[k]) == zero {
			delete(m, k)
		}
	}
	return json.Marshal(m)
}

// ProjectBucket mirrors how Claude Code names a project's session directory
// under ~/.claude/projects: the absolute path with every character that is not
// a letter or digit replaced by '-' (e.g. /Users/x/proj -> -Users-x-proj,
// /workspace -> -workspace). It lives here, next to the store descriptors,
// because it is the one piece of a store layout that a caller has to reproduce
// rather than merely read.
//
// It used to replace only '/' and '.', and that was wrong for any path carrying
// an underscore — the two sides then disagree about where a project's sessions
// live, so sandbox-cli writes (and creates) a directory the host's Claude Code
// never reads. Reported as #57: one project had two buckets, `…-intrupt_api`
// with four sandboxed sessions and `…-intrupt-api` with fifteen host ones.
//
// Every non-alphanumeric rather than a longer list of characters, because that
// is what the evidence supports: of every bucket Claude Code created on the
// reporting machine, none contained anything but letters, digits and '-', and
// its own bucket for `~/.config/sandbox/worktrees/intrupt_api-…` collapsed the
// '.' and the '_' alike. Guessing a specific set would fix this path and leave
// the next one broken.
// legacyProjectBucket is ProjectBucket as it behaved before #57: only '/' and
// '.' were replaced. Kept because sessions recorded under the old name are
// still on disk and still the user's — a corrected name must not make history
// disappear, which is a worse failure than the one it fixes.
//
// Sandbox-written only. Claude Code never used this spelling, so a directory
// bearing it exists solely because sandbox-cli created it.
func legacyProjectBucket(absPath string) string {
	b := strings.ReplaceAll(absPath, "/", "-")
	return strings.ReplaceAll(b, ".", "-")
}

// ProjectBuckets names every directory a project's sessions may live in, most
// current first: the name Claude Code uses, and the one we used to write.
// Identical for any path without an underscore or other special character, and
// then only one is returned.
func ProjectBuckets(absPath string) []string {
	cur := ProjectBucket(absPath)
	if old := legacyProjectBucket(absPath); old != cur {
		return []string{cur, old}
	}
	return []string{cur}
}

func ProjectBucket(absPath string) string {
	var b strings.Builder
	b.Grow(len(absPath))
	for _, r := range absPath {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	return b.String()
}

package studioapi

import (
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/rescue"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// This file is how a second repository becomes nameable.
//
// One daemon still has one *default* project — `-project` fixes the directory
// every unqualified request is about, which is what keeps every existing client
// working unchanged. What is new is a persisted list of other host directories
// the user has pointed Studio at, so a request can say which repository it means
// and the UI can offer the ones that exist instead of inventing them.
//
// The rule that keeps this inside the trust boundary, and the reason the
// registry exists at all rather than each handler taking a path: **a request
// names a repository by id, never by path.** POST /v1/projects is the single
// endpoint that accepts a host path, and it is therefore the single place a path
// is checked — absolute, on disk, a git repository, and past
// sandbox.RefuseUnsafeHostPath. Every other handler resolves an id against what
// that endpoint recorded, so no amount of parameter-guessing talks one into
// reading a directory nobody registered. A registry is also the only shape that
// can be *audited*: the set of directories this control plane will touch is a
// file the user can read, rather than the union of whatever paths were ever
// posted at it.
//
// Ids are recomputed from the path, never trusted from the file. worktree.RepoID
// is what stamps the sandbox.repo label on a container, so deriving the
// registry's id the same way makes "this run belongs to that repository" true by
// construction instead of by two pieces of code agreeing. The stored id is a
// cache for the one case git cannot answer — a directory that has since gone
// away — and that case is reported rather than hidden.

const projectsFileVersion = 1

// projectRecord is one registered repository as it is persisted.
//
// Root is the authority and the other two are derived from it: a file that
// disagreed with git about a repository's identity would be a second source of
// truth for the fact every container label already encodes.
type projectRecord struct {
	Root string `json:"root"`
	ID   string `json:"id"`
	Name string `json:"name"`
}

type projectsFile struct {
	Version  int             `json:"version"`
	Projects []projectRecord `json:"projects"`
}

// projectStore is the persisted set of repositories this daemon may be asked
// about, at ~/.config/sandbox/studio/projects.json.
//
// Outside every repository, like the rescue and audit state and for the sharper
// version of the same reason: a list of repositories cannot live inside one of
// them. Guarded by a mutex because these are HTTP handlers — internal/creds'
// warn-once state carries one for the same reason, and unlike the CLI's
// sequential launches, two browser tabs really can add a project at once.
type projectStore struct {
	mu   sync.Mutex
	path string // "" when there is no home directory to persist into
	recs []projectRecord
}

// projectsPath is the registry file. Empty when the config directory cannot be
// resolved, which makes the store in-memory rather than an error: Studio still
// manages the project it was started in, and refusing to serve because a list of
// *extra* repositories has nowhere to live would be the wrong trade.
func projectsPath() string {
	dir := config.StudioDir()
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, "projects.json")
}

// loadProjectStore reads the registry, returning an empty one when it does not
// exist or cannot be parsed. Unparseable is deliberately not an error: this is a
// convenience list, and a daemon that would not start because of it would be
// harder to recover than the file is to rewrite.
func loadProjectStore(path string) *projectStore {
	st := &projectStore{path: path}
	if path == "" {
		return st
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	var on projectsFile
	if err := json.Unmarshal(data, &on); err != nil || on.Version != projectsFileVersion {
		return st
	}
	for _, rec := range on.Projects {
		if rec.Root != "" {
			st.recs = append(st.recs, rec)
		}
	}
	return st
}

// records returns a copy of what is registered, in the order it was added.
func (st *projectStore) records() []projectRecord {
	st.mu.Lock()
	defer st.mu.Unlock()
	out := make([]projectRecord, len(st.recs))
	copy(out, st.recs)
	return out
}

// add records a repository, replacing any earlier record for the same id so
// registering a path twice is idempotent rather than duplicating a row.
func (st *projectStore) add(rec projectRecord) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	for i, have := range st.recs {
		if have.ID == rec.ID {
			st.recs[i] = rec
			return st.save()
		}
	}
	st.recs = append(st.recs, rec)
	return st.save()
}

// remove drops a repository from the registry. It reports whether anything was
// removed, so the handler can answer 404 for an id nobody registered instead of
// pretending to have deleted something.
//
// Nothing on disk is touched: this is a list of directories Studio will answer
// about, and forgetting one has never meant deleting a repository.
func (st *projectStore) remove(id string) (bool, error) {
	st.mu.Lock()
	defer st.mu.Unlock()
	for i, have := range st.recs {
		if have.ID == id {
			st.recs = append(st.recs[:i], st.recs[i+1:]...)
			return true, st.save()
		}
	}
	return false, nil
}

// save writes the registry atomically, so an interrupted write cannot leave a
// half-written file where the registered paths should be. Callers hold the mutex.
func (st *projectStore) save() error {
	if st.path == "" {
		return fmt.Errorf("cannot determine the sandbox config directory (no HOME?), so this repository cannot be remembered")
	}
	if err := os.MkdirAll(filepath.Dir(st.path), 0o700); err != nil {
		return err
	}
	recs := st.recs
	if recs == nil {
		recs = []projectRecord{}
	}
	data, err := json.MarshalIndent(projectsFile{Version: projectsFileVersion, Projects: recs}, "", "  ")
	if err != nil {
		return err
	}
	tmp := st.path + ".tmp"
	if err := os.WriteFile(tmp, append(data, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, st.path)
}

// describeProject turns a host directory into the wire type, asking git for the
// identity rather than deriving one from the directory name.
//
// A path git will not answer for yields Missing with whatever was recorded when
// it would: the same bargain internal/agentctx makes with a store it cannot see
// today — a repository on an unmounted volume is not the same as one that never
// existed, and dropping it silently is how a list starts lying about what the
// user asked for.
func describeProject(rec projectRecord) Project {
	p := Project{ID: rec.ID, Name: rec.Name, Root: rec.Root}
	if p.Name == "" {
		p.Name = filepath.Base(rec.Root)
	}
	fi, err := os.Stat(rec.Root)
	if err != nil || !fi.IsDir() {
		p.Missing = true
		return p
	}
	id, err := worktree.RepoID(rec.Root)
	if err != nil {
		// On disk but not a repository any more — the directory was replaced, or
		// its .git removed. Branch-addressed requests will fail against it, so it
		// is reported as missing rather than offered as usable.
		p.Missing = true
		return p
	}
	p.ID = id
	return p
}

// projects lists every repository this daemon will answer about: the one it was
// started in first, then the registered ones in the order they were added.
//
// The default is always present and is never read from the file — it is what
// `-project` named, so a registry that had been emptied still leaves Studio able
// to describe the repository it is standing in.
func (s *Server) projects() []Project {
	def := Project{ID: s.RepoID, Name: filepath.Base(s.Project), Root: s.Project, Default: true}
	if fi, err := os.Stat(s.Project); err != nil || !fi.IsDir() {
		def.Missing = true
	}
	out := []Project{def}
	if s.Projects == nil {
		return out
	}
	for _, rec := range s.Projects.records() {
		p := describeProject(rec)
		if p.Root == s.Project || (p.ID != "" && p.ID == def.ID) {
			continue // the default, registered again; one row, not two
		}
		out = append(out, p)
	}
	return out
}

// repoScope is the one repository a request is about: the host directory to ask
// git about, and the id containers belonging to it are labelled with.
//
// The two travel together deliberately. They used to be two fields on Server
// read independently, which is fine while there is one of each and wrong the
// moment there are two — a handler that took the project from a request and the
// id from the server would file a worktree under a repository it never touched,
// which is the mistake buildRunOptions already documents for the run path.
type repoScope struct {
	Project string
	RepoID  string
}

// defaultScope is the repository this daemon was started in.
func (s *Server) defaultScope() repoScope {
	return repoScope{Project: s.Project, RepoID: s.RepoID}
}

// scopeFor resolves a repository id to the directory to work in.
//
// An empty id means the default project, which is what keeps every request
// written before this file existed meaning exactly what it meant then. A
// non-empty id must be one this daemon lists — an unregistered id is refused
// rather than guessed at, and a *path* is never accepted here at all, which is
// the whole point of the registry.
func (s *Server) scopeFor(id string) (repoScope, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return s.defaultScope(), nil
	}
	for _, p := range s.projects() {
		if p.ID != id {
			continue
		}
		if p.Missing {
			return repoScope{}, fmt.Errorf(
				"repository %q (%s) is registered but cannot be read right now — "+
					"the directory is gone, is not a git repository, or lives on a volume that is not mounted", p.Name, p.Root)
		}
		return repoScope{Project: p.Root, RepoID: p.ID}, nil
	}
	return repoScope{}, fmt.Errorf(
		"no repository %q is registered with this Studio; POST /v1/projects to add one", id)
}

// scopeOf resolves the ?repo= query parameter, for the handlers that read.
func (s *Server) scopeOf(r *http.Request) (repoScope, error) {
	return s.scopeFor(r.URL.Query().Get("repo"))
}

// scopeOfRun is which repository a container belongs to, from the label it was
// stamped with.
//
// Docker is the state store, so the label is the answer — and falling back to
// the default project matters more than it looks: a run launched before this
// daemon knew about its repository, or one whose repository has since been
// removed from the registry, still has a diff worth showing. Answering with the
// default is what the code did before repositories were plural, so a run that
// cannot be placed is no worse off than it was.
func (s *Server) scopeOfRun(repoID string) repoScope {
	if repoID != "" && repoID != s.RepoID {
		for _, p := range s.projects() {
			if p.ID == repoID && !p.Missing {
				return repoScope{Project: p.Root, RepoID: p.ID}
			}
		}
	}
	return s.defaultScope()
}

// validateProjectPath is the one place a host path from a request is turned into
// a repository this daemon will touch. Every refusal here is a refusal for every
// endpoint, because every other endpoint takes an id.
//
// The order is the order the failures are worth reporting in: a relative path is
// a client mistake, a missing directory is a typo, an unsafe one is the boundary
// this tool exists to hold, and "not a git repository" is last because it is the
// only one that needed git to answer.
func validateProjectPath(in string) (projectRecord, error) {
	in = strings.TrimSpace(in)
	if in == "" {
		return projectRecord{}, fmt.Errorf("path is required")
	}
	if !filepath.IsAbs(in) {
		return projectRecord{}, fmt.Errorf(
			"%q is not an absolute path: this daemon resolves it on the host, where it has no working directory to read it against", in)
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(in))
	if err != nil {
		return projectRecord{}, fmt.Errorf("no such directory: %s", in)
	}
	fi, err := os.Stat(real)
	if err != nil || !fi.IsDir() {
		return projectRecord{}, fmt.Errorf("not a directory: %s", in)
	}
	// The repository root, not the directory that was named. Studio addresses
	// everything by branch and a worktree belongs to a repository, so registering
	// a subdirectory would leave every branch-addressed request answering for a
	// repository the user never named — the same reason studio.sh resolves
	// -project to the toplevel before starting.
	root, err := rescue.MainRepoRoot(real)
	if err != nil {
		return projectRecord{}, fmt.Errorf(
			"%s is not a git repository: Studio's worktrees, diffs and branch-addressed runs all need one", in)
	}
	// Applied to the root, because the root is what gets bind-mounted at
	// /workspace. Naming a subdirectory of your home is safe; naming a git
	// repository whose root *is* your home is not, and only the root knows that.
	if err := sandbox.RefuseUnsafeHostPath(root); err != nil {
		return projectRecord{}, err
	}
	id, err := worktree.RepoID(root)
	if err != nil {
		return projectRecord{}, err
	}
	return projectRecord{Root: root, ID: id, Name: filepath.Base(root)}, nil
}

// handleListProjects is GET /v1/projects.
func (s *Server) handleListProjects(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, ProjectsResponse{Projects: s.projects()})
}

// handleCreateProject is POST /v1/projects: the only endpoint that accepts a
// host path, and the only one that needs to.
func (s *Server) handleCreateProject(w http.ResponseWriter, r *http.Request) {
	var req ProjectCreateRequest
	if err := decodeJSON(r, &req); err != nil {
		writeError(w, http.StatusBadRequest, err)
		return
	}
	rec, err := validateProjectPath(req.Path)
	if err != nil {
		// 422 rather than 400: the request was well-formed JSON and the client
		// could not have known the answer without asking this host — which
		// directories exist, and which of them are repositories, is a fact about
		// this machine rather than about the request.
		writeError(w, http.StatusUnprocessableEntity, err)
		return
	}
	if s.Projects == nil {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("this daemon has no project registry to add to"))
		return
	}
	if err := s.Projects.add(rec); err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	// Answer with the row the listing will show, not merely with what was
	// recorded. They differ for the repository this daemon was started in —
	// which carries Default and is not in the registry at all — and a client that
	// rendered the recorded form would drop the "started here" marker off the one
	// repository that cannot be removed. Adding a repository Studio already
	// manages is a no-op, and this is what lets the client say so.
	for _, p := range s.projects() {
		if p.ID == rec.ID {
			writeJSON(w, http.StatusCreated, p)
			return
		}
	}
	writeJSON(w, http.StatusCreated, describeProject(rec))
}

// handleDeleteProject is DELETE /v1/projects/{id}. It forgets a repository; it
// never touches one.
func (s *Server) handleDeleteProject(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if id == s.RepoID {
		// The default is what `-project` named, so it is not the registry's to
		// forget: it would be back on the next listing, which is a worse answer
		// than saying so. Changing it is a restart — `studio.sh up --project DIR`.
		writeError(w, http.StatusConflict, fmt.Errorf(
			"%s is the repository this daemon was started in and cannot be removed; "+
				"start Studio against another one with `studio.sh up --project DIR`", id))
		return
	}
	if s.Projects == nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("no repository %q is registered", id))
		return
	}
	removed, err := s.Projects.remove(id)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	if !removed {
		writeError(w, http.StatusNotFound, fmt.Errorf("no repository %q is registered", id))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

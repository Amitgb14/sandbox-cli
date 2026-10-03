package studio

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/hostpath"
	"github.com/Amitgb14/sandbox-cli/internal/workspace"
)

// The repositories Studio may act on, kept in a file the user can read. The
// rule that keeps this inside the boundary is beta.15's: **a request names a
// repository by id, never by path.** Adding one is the only request that
// carries a path, so it is the one place a path is checked — absolute, on disk,
// a git repository's root, and past hostpath's refusals — and every other
// handler resolves an id, so no parameter-guessing reaches a directory nobody
// registered. Ids are recomputed from the path rather than trusted from the
// file.

// Repo is one registered repository.
type Repo struct {
	ID      string    `json:"id"`
	Path    string    `json:"path"`
	Name    string    `json:"name"`
	Added   time.Time `json:"added"`
	Missing bool      `json:"missing,omitempty"` // registered, and gone from disk
}

var reposMu sync.Mutex

func lastElem(p string) string { return filepath.Base(p) }

func nowUTC() time.Time { return time.Now().UTC() }

func repoID(path string) string {
	h := sha256.Sum256([]byte(path))
	return hex.EncodeToString(h[:6])
}

func (s *Server) loadRepos() ([]Repo, error) {
	b, err := os.ReadFile(s.ReposFile)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []Repo
	if err := json.Unmarshal(b, &out); err != nil {
		return nil, fmt.Errorf("%s: %w", s.ReposFile, err)
	}
	for i := range out {
		out[i].ID = repoID(out[i].Path)
		if fi, err := os.Stat(out[i].Path); err != nil || !fi.IsDir() {
			out[i].Missing = true
		}
	}
	return out, nil
}

func (s *Server) saveRepos(repos []Repo) error {
	if err := os.MkdirAll(filepath.Dir(s.ReposFile), 0o700); err != nil {
		return err
	}
	b, _ := json.MarshalIndent(repos, "", "  ")
	tmp := s.ReposFile + ".tmp"
	if err := os.WriteFile(tmp, append(b, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.ReposFile)
}

// repo resolves an id to a registered, present repository.
func (s *Server) repo(w http.ResponseWriter, r *http.Request) (Repo, bool) {
	reposMu.Lock()
	repos, err := s.loadRepos()
	reposMu.Unlock()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return Repo{}, false
	}
	id := r.PathValue("id")
	for _, rp := range repos {
		if rp.ID == id {
			if rp.Missing {
				writeErr(w, http.StatusGone, rp.Path+" is no longer on disk")
				return Repo{}, false
			}
			return rp, true
		}
	}
	writeErr(w, http.StatusNotFound, "no repository with that id; add it first")
	return Repo{}, false
}

// validateRepoPath is the one check a path gets.
func validateRepoPath(in string) (string, error) {
	in = strings.TrimSpace(in)
	if !filepath.IsAbs(in) {
		return "", fmt.Errorf("%q is not an absolute path", in)
	}
	real, err := filepath.EvalSymlinks(filepath.Clean(in))
	if err != nil {
		return "", fmt.Errorf("no such directory: %s", in)
	}
	if fi, err := os.Stat(real); err != nil || !fi.IsDir() {
		return "", fmt.Errorf("not a directory: %s", in)
	}
	// The repository's root, not the directory named: a subdirectory of your
	// home is fine, a repository whose root is your home is not, and only the
	// root knows which.
	root := workspace.RepoRoot(real)
	if root == "" {
		return "", fmt.Errorf("%s is not a git repository", in)
	}
	if err := hostpath.RefuseUnsafeHostPath(root); err != nil {
		return "", err
	}
	return root, nil
}

func (s *Server) listRepos(w http.ResponseWriter, _ *http.Request) {
	reposMu.Lock()
	repos, err := s.loadRepos()
	reposMu.Unlock()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	if repos == nil {
		repos = []Repo{}
	}
	sort.Slice(repos, func(i, j int) bool { return repos[i].Name < repos[j].Name })
	writeJSON(w, http.StatusOK, map[string]any{"repos": repos})
}

func (s *Server) addRepo(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Path string `json:"path"`
	}
	if !decode(w, r, &req) {
		return
	}
	root, err := validateRepoPath(req.Path)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	reposMu.Lock()
	defer reposMu.Unlock()
	repos, err := s.loadRepos()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	for _, rp := range repos {
		if rp.Path == root {
			writeJSON(w, http.StatusOK, rp)
			return
		}
	}
	rp := Repo{ID: repoID(root), Path: root, Name: filepath.Base(root), Added: time.Now().UTC()}
	if err := s.saveRepos(append(repos, rp)); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, rp)
}

func (s *Server) removeRepo(w http.ResponseWriter, r *http.Request) {
	reposMu.Lock()
	defer reposMu.Unlock()
	repos, err := s.loadRepos()
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	id := r.PathValue("id")
	kept := repos[:0]
	found := false
	for _, rp := range repos {
		if rp.ID == id {
			found = true
			continue
		}
		kept = append(kept, rp)
	}
	if !found {
		writeErr(w, http.StatusNotFound, "no repository with that id")
		return
	}
	if err := s.saveRepos(kept); err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

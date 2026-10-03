package studioapi

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// Picking a repository to add, by walking the host's directories.
//
// It exists because a browser cannot answer the question. `<input
// webkitdirectory>` hands over File objects carrying *relative* paths, and
// `showDirectoryPicker()` hands over a handle whose `name` is the last segment —
// neither yields `/Users/you/code/thing`, which is the only form the daemon can
// use, since it runs on the host and bind-mounts what it is given. So the
// directory listing has to come from here, and typing a path stays as the escape
// hatch it always was.
//
// This is the widest-reaching read in the API and the only one that leaves a
// repository, so it is deliberately narrow in four ways:
//
//   - **Directories only.** Files are never listed, so this cannot be used to
//     find out that `~/Documents/tax-2025.pdf` exists.
//   - **Names only.** No sizes, no modification times, no contents — there is no
//     endpoint here that opens anything. Reading a file still requires
//     /v1/files, which refuses to leave a *registered* repository.
//   - **No dot-directories.** `.ssh`, `.aws`, `.gnupg` and friends are never
//     enumerated. This costs the ability to pick a repository whose root is a
//     dot-directory, which is what the typed path is still there for.
//   - **Same gate as everything else**: loopback binding, the Origin and Host
//     checks, and the bearer token.
//
// The honest framing of the trade: a caller that can reach this can already
// POST /v1/runs and start a container mounting a directory, so knowing which
// directories exist is not the boundary — it is a convenience built on top of
// reach that was already granted. What it must not become is a file reader.

// maxBrowseEntries bounds one listing. A home directory with thousands of
// subdirectories is legitimate; a picker showing all of them is not.
const maxBrowseEntries = 500

// handleBrowse is GET /v1/browse?path=.
//
// An empty path answers the home directory, which is where somebody looking for
// their code almost always starts. Anything else must be absolute: this resolves
// on the host, where there is no working directory to read a relative path
// against.
func (s *Server) handleBrowse(w http.ResponseWriter, r *http.Request) {
	requested := strings.TrimSpace(r.URL.Query().Get("path"))
	if requested == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			writeError(w, http.StatusInternalServerError, fmt.Errorf("cannot determine your home directory: %w", err))
			return
		}
		requested = home
	}
	if !filepath.IsAbs(requested) {
		writeError(w, http.StatusBadRequest, fmt.Errorf(
			"%q is not an absolute path: this daemon resolves it on the host, where it has no working directory to read it against", requested))
		return
	}

	dir, err := filepath.EvalSymlinks(filepath.Clean(requested))
	if err != nil {
		writeError(w, http.StatusNotFound, fmt.Errorf("no such directory: %s", requested))
		return
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		writeError(w, http.StatusNotFound, fmt.Errorf("not a directory: %s", requested))
		return
	}

	des, err := os.ReadDir(dir)
	if err != nil {
		// Permission denied is the common one, and it is the user's own answer
		// about their own machine rather than a fault: report it and let the
		// picker show it beside the directory they tried to open.
		writeError(w, http.StatusForbidden, err)
		return
	}

	// Which ids are already registered, so the picker can say "already added"
	// rather than letting somebody add the same repository twice and wonder why
	// no new row appeared.
	registered := map[string]bool{}
	for _, p := range s.projects() {
		registered[p.ID] = true
	}

	resp := BrowseResponse{Path: dir, Entries: []BrowseEntry{}}
	if parent := filepath.Dir(dir); parent != dir {
		resp.Parent = parent
	}
	if home, err := os.UserHomeDir(); err == nil {
		resp.Home = home
	}

	for _, de := range des {
		if !de.IsDir() {
			continue // directories only; this is not a file browser
		}
		name := de.Name()
		if strings.HasPrefix(name, ".") {
			continue // never enumerate ~/.ssh and friends
		}
		full := filepath.Join(dir, name)
		e := BrowseEntry{Name: name, Path: full}
		if isGitRepoRoot(full) {
			e.Repo = true
			// Computed here rather than by the client: the id is a hash of the
			// path, and a client deriving it would be a second implementation of
			// the one function that decides what a repository *is*.
			if id, err := worktree.RepoID(full); err == nil {
				e.Registered = registered[id]
			}
		}
		resp.Entries = append(resp.Entries, e)
		if len(resp.Entries) >= maxBrowseEntries {
			resp.Truncated = true
			break
		}
	}
	sort.SliceStable(resp.Entries, func(i, j int) bool {
		return strings.ToLower(resp.Entries[i].Name) < strings.ToLower(resp.Entries[j].Name)
	})

	// Whether the directory you are *standing in* is itself addable, so the
	// dialog's primary button can be "Use this folder" without a round trip.
	resp.Repo = isGitRepoRoot(dir)
	writeJSON(w, http.StatusOK, resp)
}

// isGitRepoRoot reports whether dir holds a .git — a directory for an ordinary
// checkout, a file for a linked worktree.
//
// Deliberately a stat rather than a git invocation: this runs once per row of a
// listing, and shelling out five hundred times to draw a picker is the kind of
// thing that makes a UI feel broken. It is also only a *hint* — POST /v1/projects
// still resolves the real repository root and refuses what it must, so a wrong
// guess here costs a refusal rather than an unchecked add.
func isGitRepoRoot(dir string) bool {
	_, err := os.Lstat(filepath.Join(dir, ".git"))
	return err == nil
}

package studioapi

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// Browsing a repository's files.
//
// Two endpoints, one rule, and the rule is the only interesting part: **every
// path is relative to a registered repository's root, and the resolved path must
// still be inside that root.** `/workspace` is attacker-controlled by this
// tool's own threat model — an agent writes there — so a repository can contain
// a symlink named `notes.md` pointing at `~/.ssh/id_ed25519`, and a file browser
// that simply opened what it was asked for would read it out to the browser over
// a loopback port. `resolveInRepo` is where that is stopped, and it is the only
// way either handler turns a request into a filesystem path.
//
// Read-only, deliberately and completely. There is no write endpoint here and
// should not be one: the whole point of the sandbox is that the *agent* edits
// the workspace inside a container, and a control plane that could also write to
// it from an HTTP request would be a second, unsandboxed editor for the same
// tree — with none of the isolation that makes the first one safe.

const (
	// maxFileBytes bounds a single file read. Large enough for source, small
	// enough that one request cannot make the daemon hold a database dump in
	// memory. A file past it is served truncated and says so, rather than
	// refused: the first half of a big log is usually what was wanted.
	maxFileBytes = 512 << 10

	// maxDirEntries bounds one listing. node_modules is a legitimate directory
	// and rendering four hundred thousand rows helps nobody.
	maxDirEntries = 2000

	// binarySniffBytes is how much of a file is examined for a NUL before
	// deciding it is not text. The same heuristic git uses, and for the same
	// reason: there is no reliable answer, and this one is wrong rarely.
	binarySniffBytes = 8000
)

// errOutsideRepo is what every containment failure becomes, so a caller cannot
// tell "that symlink leaves the repository" from "no such file" by the message.
// The distinction is only useful to somebody probing for what exists on the host.
var errOutsideRepo = errors.New("no such file in this repository")

// resolveInRepo turns a repository-relative path into an absolute host path,
// and refuses anything that does not stay inside the repository.
//
// The order matters. Cleaning first removes `..` as *text*, which is necessary
// and nowhere near sufficient — `a/../../etc` is caught here, but a symlink is
// not textual at all. So the cleaned path is resolved on disk with
// EvalSymlinks, and the *result* is what gets checked for containment. That is
// the same reason internal/sandbox compares mount paths by what they resolve to
// rather than by what they say.
//
// The containment test appends a separator before comparing, because a plain
// prefix test says `/repo-secrets/x` is inside `/repo`. Casing is consistent
// because both sides come from EvalSymlinks; where it is not — a symlink whose
// stored target differs in case from the root on a case-insensitive filesystem —
// the comparison fails, and failing closed is the right direction for this
// check.
func resolveInRepo(root, rel string) (string, error) {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		return "", fmt.Errorf("cannot read repository %s: %w", root, err)
	}

	// Slash-separated on the wire whatever the host separator is: this is a URL
	// parameter, not a host path, and accepting both spellings would mean two
	// code paths for one question.
	clean := path.Clean("/" + strings.TrimPrefix(strings.ReplaceAll(rel, "\\", "/"), "/"))
	joined := filepath.Join(realRoot, filepath.FromSlash(clean))

	real, err := filepath.EvalSymlinks(joined)
	if err != nil {
		// Absent, or a dangling symlink. Both are "not there" from here.
		return "", errOutsideRepo
	}
	if real != realRoot && !strings.HasPrefix(real, realRoot+string(filepath.Separator)) {
		return "", errOutsideRepo
	}
	return real, nil
}

// relativeTo renders a host path back as the repository-relative one a client
// sent, so the response can be fed straight back as the next request.
func relativeTo(root, abs string) string {
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		realRoot = root
	}
	rel, err := filepath.Rel(realRoot, abs)
	if err != nil || rel == "." {
		return ""
	}
	return filepath.ToSlash(rel)
}

// browseRoot is the directory a file request is about: the repository's own
// checkout, or one branch's worktree.
//
// A branch is not a view of the repository here — it is a *different directory*
// on disk. `--worktree` gives each branch its own checkout under
// `<config>/worktrees/<repoId>/<branch>`, which is the whole reason several
// agents can work in parallel, and it means "show me this branch's files" is
// answered by browsing that directory rather than by asking git to render a
// tree at a ref. That also keeps this endpoint honest about what it shows:
// the files as they are **on disk right now**, uncommitted work included, which
// is the state somebody reviewing an agent's work actually wants.
//
// The containment rule moves with it. resolveInRepo is re-rooted at whatever
// this returns, so a path is checked against the directory being browsed rather
// than against the repository root — otherwise browsing a worktree would compare
// against the wrong tree and either refuse everything or, worse, allow a path
// that escapes the worktree while staying inside the repo.
func (s *Server) browseRoot(sc repoScope, branch string) (string, error) {
	branch = strings.TrimSpace(branch)
	if branch == "" {
		return sc.Project, nil
	}
	// The checkout answers to its own branch name too. It has no managed
	// worktree, so Path finds nothing for it — and refusing there would mean the
	// one branch the listing now names (`main`, usually) is the one branch no
	// link could open, which is worse than the omission this replaced.
	if branch == worktree.HeadBranch(sc.Project) {
		return sc.Project, nil
	}
	path, exists, err := worktree.Path(sc.Project, branch)
	if err != nil {
		return "", err
	}
	if !exists {
		return "", fmt.Errorf("no worktree for branch %q in this repository", branch)
	}
	return path, nil
}

// handleListFiles is GET /v1/files?repo=&branch=&path=.
func (s *Server) handleListFiles(w http.ResponseWriter, r *http.Request) {
	sc, err := s.scopeOf(r)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	root, err := s.browseRoot(sc, r.URL.Query().Get("branch"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	rel := r.URL.Query().Get("path")
	abs, err := resolveInRepo(root, rel)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		writeError(w, http.StatusNotFound, errOutsideRepo)
		return
	}
	if !info.IsDir() {
		writeError(w, http.StatusBadRequest, fmt.Errorf(
			"%s is a file, not a directory; read it with /v1/files/content", relativeTo(root, abs)))
		return
	}

	des, err := os.ReadDir(abs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	resp := FilesResponse{Path: relativeTo(root, abs), Entries: []FileEntry{}}
	if len(des) > maxDirEntries {
		des = des[:maxDirEntries]
		resp.Truncated = true
	}
	for _, de := range des {
		e := FileEntry{
			Name: de.Name(),
			Path: path.Join(resp.Path, de.Name()),
			Dir:  de.IsDir(),
			// From the DirEntry, so a symlink is reported as one rather than
			// followed. Following here would size and type whatever it points at,
			// which is a read of the target before anyone asked for it — and a
			// loop of two symlinks would be an infinite one.
			Symlink: de.Type()&os.ModeSymlink != 0,
		}
		if fi, err := de.Info(); err == nil {
			if !e.Dir {
				e.Size = fi.Size()
			}
			e.ModifiedAt = fi.ModTime().UTC().Format(time.RFC3339)
		}
		resp.Entries = append(resp.Entries, e)
	}

	// Directories first, then case-insensitive by name — what every file browser
	// does, and doing it here means three clients cannot each do it differently.
	sort.SliceStable(resp.Entries, func(i, j int) bool {
		a, b := resp.Entries[i], resp.Entries[j]
		if a.Dir != b.Dir {
			return a.Dir
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	writeJSON(w, http.StatusOK, resp)
}

// handleFileContent is GET /v1/files/content?repo=&branch=&path=.
func (s *Server) handleFileContent(w http.ResponseWriter, r *http.Request) {
	sc, err := s.scopeOf(r)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	root, err := s.browseRoot(sc, r.URL.Query().Get("branch"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	rel := strings.TrimSpace(r.URL.Query().Get("path"))
	if rel == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("path is required"))
		return
	}
	abs, err := resolveInRepo(root, rel)
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		writeError(w, http.StatusNotFound, errOutsideRepo)
		return
	}
	if info.IsDir() {
		writeError(w, http.StatusBadRequest, fmt.Errorf(
			"%s is a directory; list it with /v1/files", relativeTo(root, abs)))
		return
	}
	// Regular files only. A fifo would block the read forever and a device would
	// answer something that is not a file at all; neither is a thing a repository
	// browser has any business opening.
	if !info.Mode().IsRegular() {
		writeError(w, http.StatusUnprocessableEntity, fmt.Errorf(
			"%s is not a regular file", relativeTo(root, abs)))
		return
	}

	f, err := os.Open(abs)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}
	defer f.Close()

	buf, err := io.ReadAll(io.LimitReader(f, maxFileBytes))
	if err != nil {
		writeError(w, http.StatusInternalServerError, err)
		return
	}

	resp := FileContentResponse{
		Path:      relativeTo(root, abs),
		Size:      info.Size(),
		Truncated: info.Size() > int64(len(buf)),
	}
	sniff := buf
	if len(sniff) > binarySniffBytes {
		sniff = sniff[:binarySniffBytes]
	}
	if bytes.IndexByte(sniff, 0) >= 0 {
		// Reported, not sent. A client showing the bytes of a PNG as text is
		// noise at best, and the size is the useful fact about it.
		resp.Binary = true
		writeJSON(w, http.StatusOK, resp)
		return
	}
	resp.Content = string(buf)
	writeJSON(w, http.StatusOK, resp)
}

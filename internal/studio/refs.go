package studio

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/workspace"
)

// Work that came back from a sandbox lives under refs/sandbox/ in the
// repository — bring-back, checkpoints, fleet tasks — and Studio's review is a
// list of those refs and the diff of each against HEAD. Every git call goes
// through workspace.Git, so through githard: the repository's own config names
// diff drivers and textconv commands a sandbox could have planted on its way
// back, and a diff must not run them.

// SandboxRef is one ref of returned work.
type SandboxRef struct {
	Ref     string    `json:"ref"`
	Commit  string    `json:"commit"`
	Subject string    `json:"subject"`
	Author  string    `json:"author"`
	Date    time.Time `json:"date"`
	Ahead   int       `json:"ahead"` // commits not in HEAD
}

// refRE is what a ref from a request may be: under refs/sandbox/, made of
// plain segments. It is checked before git sees it, so nothing in it can read
// as an option or a revision range.
var refRE = regexp.MustCompile(`^refs/sandbox/[A-Za-z0-9_][A-Za-z0-9._-]*(/[A-Za-z0-9_][A-Za-z0-9._-]*)*$`)

func validSandboxRef(ref string) bool {
	return refRE.MatchString(ref) && !strings.Contains(ref, "..") && !strings.HasSuffix(ref, ".lock")
}

func (s *Server) refs(w http.ResponseWriter, r *http.Request) {
	rp, ok := s.repo(w, r)
	if !ok {
		return
	}
	out, err := workspace.Git(rp.Path, "for-each-ref", "--sort=-committerdate",
		"--format=%(refname)%00%(objectname)%00%(subject)%00%(authorname)%00%(committerdate:iso-strict)", "refs/sandbox/")
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	refs := []SandboxRef{}
	for _, line := range strings.Split(out, "\n") {
		f := strings.Split(line, "\x00")
		if len(f) != 5 || !validSandboxRef(f[0]) {
			continue
		}
		ref := SandboxRef{Ref: f[0], Commit: f[1], Subject: f[2], Author: f[3]}
		ref.Date, _ = time.Parse(time.RFC3339, f[4])
		if n, err := workspace.Git(rp.Path, "rev-list", "--count", "HEAD.."+f[0]); err == nil {
			ref.Ahead, _ = strconv.Atoi(strings.TrimSpace(n))
		}
		refs = append(refs, ref)
	}
	writeJSON(w, http.StatusOK, map[string]any{"refs": refs})
}

// maxPatch bounds a diff's text: a review screen is for reading, and a
// generated file can make a patch of any size.
const maxPatch = 2 << 20

func (s *Server) diff(w http.ResponseWriter, r *http.Request) {
	rp, ok := s.repo(w, r)
	if !ok {
		return
	}
	ref := r.URL.Query().Get("ref")
	if !validSandboxRef(ref) {
		writeErr(w, http.StatusBadRequest, "ref: a ref under refs/sandbox/")
		return
	}
	rng := "HEAD..." + ref
	stat, err := workspace.Git(rp.Path, "diff", "--no-ext-diff", "--no-textconv", "--no-color", "--numstat", rng)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	patch, err := workspace.Git(rp.Path, "diff", "--no-ext-diff", "--no-textconv", "--no-color", rng)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	truncated := false
	if len(patch) > maxPatch {
		patch, truncated = patch[:maxPatch], true
	}
	type fileStat struct {
		Path    string `json:"path"`
		Added   int    `json:"added"` // -1 for a binary file
		Removed int    `json:"removed"`
	}
	files := []fileStat{}
	for _, line := range strings.Split(stat, "\n") {
		f := strings.SplitN(line, "\t", 3)
		if len(f) != 3 {
			continue
		}
		a, errA := strconv.Atoi(f[0])
		d, errD := strconv.Atoi(f[1])
		if errA != nil || errD != nil {
			a, d = -1, -1
		}
		files = append(files, fileStat{Path: f[2], Added: a, Removed: d})
	}
	writeJSON(w, http.StatusOK, map[string]any{"ref": ref, "files": files, "patch": patch, "truncated": truncated})
}

package studio

import (
	"embed"
	"io/fs"
)

// The built UI, when this binary was built with one: `make studio` exports the
// app in studio/ into ui/, and a release build always does. A plain `go build`
// has only ui/.keep, and Studio then serves a page saying how to build it.
//
//go:embed all:ui
var uiFiles embed.FS

// EmbeddedUI is the UI built into this binary, or nil.
func EmbeddedUI() fs.FS {
	sub, err := fs.Sub(uiFiles, "ui")
	if err != nil {
		return nil
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil
	}
	return sub
}

// RegisterRepo adds the repository containing dir, if it is one Studio may
// act on, so `sandbox-cli studio` run from a checkout opens on it. A directory
// that is not a repository, or one refused, is simply not registered.
func (s *Server) RegisterRepo(dir string) (Repo, bool) {
	root, err := validateRepoPath(dir)
	if err != nil {
		return Repo{}, false
	}
	reposMu.Lock()
	defer reposMu.Unlock()
	repos, err := s.loadRepos()
	if err != nil {
		return Repo{}, false
	}
	for _, rp := range repos {
		if rp.Path == root {
			return rp, true
		}
	}
	rp := Repo{ID: repoID(root), Path: root, Name: lastElem(root), Added: nowUTC()}
	if err := s.saveRepos(append(repos, rp)); err != nil {
		return Repo{}, false
	}
	return rp, true
}

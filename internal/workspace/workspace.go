// Package workspace decides how the project reaches the sandbox: bind (mounted
// read-write, edits visible live) or clone (mounted read-only and cloned inside,
// work brought back as a verified bundle into refs/sandbox/).
//
// Clone mode exists because every `.git` escape found so far — planted hooks, a
// forged gitdir, an agent-named merge driver — needs the container to write files
// that host-side git later executes, and a read-only mount gives them nowhere to
// happen. Built at M3.
package workspace

//go:build unix

package worktree

import (
	"sync"
	"syscall"
)

// umaskMu serializes the process-wide umask change below. It is not for this
// package's own callers, which create worktrees one at a time; it is for
// studioapi, which drives runs from an HTTP handler and could otherwise have two
// creations overlap and restore each other's mask.
var umaskMu sync.Mutex

// groupWritable runs f with a umask that leaves the group bits alone.
//
// A worktree sandbox-cli creates is a directory it makes *for a container to
// work in*, and on Linux that container runs as uid 1001 with the host's primary
// group. Created under the ordinary umask 022 it comes back 0755, which the
// container cannot write — so `sandbox-cli --worktree` handed the agent a tree it
// could not edit, and under `--profile prod` the writability check now refuses a
// path the same command created seconds earlier, naming a fix the user had no
// opportunity to apply.
//
// The mask rather than a chmod afterwards: `git worktree add` writes a whole
// checkout, and walking it to repair modes is both slower and a second thing to
// keep in step. This is the same 0002 sandbox-init applies inside the container,
// for the same reason — the shared group is worth nothing if what gets created
// has the group bit stripped.
//
// Deliberately narrow. It covers one git invocation, not the process: the umask
// is global state, and anything else running concurrently would inherit it.
func groupWritable(f func() error) error {
	umaskMu.Lock()
	prev := syscall.Umask(0o002)
	defer func() {
		syscall.Umask(prev)
		umaskMu.Unlock()
	}()
	return f()
}

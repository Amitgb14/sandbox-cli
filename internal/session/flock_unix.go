//go:build unix

package session

import (
	"os"
	"syscall"
)

// flock takes an exclusive advisory lock on path, returning the release.
//
// It exists because two `serve` processes for one repository is a thing that
// happens — a forgotten foreground daemon in another terminal, a shell script
// that starts one per command — and two interleaved renames over session.json
// means the loser's panes vanish with no error anywhere. The lock is on a file of
// its own rather than on session.json, since the thing being replaced by a rename
// cannot be the thing holding the lock.
//
// Advisory and blocking: advisory because nothing outside this package writes the
// file, and blocking because the wait is microseconds and the alternative — a
// caller that gives up — turns a contended save into a silently skipped one.
func flock(path string) (release func(), err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX); err != nil {
		f.Close()
		return nil, err
	}
	return func() {
		syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		f.Close()
	}, nil
}

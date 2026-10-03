//go:build !unix

package worktree

// groupWritable is a plain call off unix: there is no umask to adjust, and the
// group sharing it exists for is Linux-only at run time (see
// internal/sandbox/hostgroup.go).
//
// Split by build tag rather than by a runtime.GOOS branch for the reason
// internal/sandbox/owner_unix.go records: a GOOS check still compiles the
// unavailable symbol on every platform, and that is how the windows release
// build broke once already.
func groupWritable(f func() error) error { return f() }

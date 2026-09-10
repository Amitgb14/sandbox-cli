//go:build !unix

package session

// flock is a no-op where flock(2) is not available.
//
// The cost is named rather than hidden: on such a platform two `serve` processes
// for one repository can interleave their saves, and the loser's panes are lost.
// The rename itself is still atomic, so the file is never half-written — what is
// unprotected is the read-modify-write around it. Windows has LockFileEx and this
// is where it would go; until then a platform with no lock gets the atomic write
// and not the mutual exclusion, which is the better half of the two.
func flock(path string) (release func(), err error) {
	return func() {}, nil
}

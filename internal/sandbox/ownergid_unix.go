//go:build unix

package sandbox

import (
	"io/fs"
	"syscall"
)

// ownerGID reports the group owning a file, and whether that could be answered
// at all.
//
// It exists in two build-tagged halves because `syscall.Stat_t` does not exist
// on Windows, and `hostgroup.go` referring to it directly broke the *release
// build* rather than any test: `go test ./...` compiles for the host only, so a
// darwin machine and CI's Linux runners both stayed green while
// `windows_amd64` failed at the one line. The group-sharing this supports is
// already Linux-only at run time (see hostgroup.go), so nothing here needs a
// Windows implementation — it needs to *compile* on Windows.
//
// Keep that shape for anything else platform-specific: one narrow helper behind
// a build tag, not a `runtime.GOOS` check, which compiles the unavailable symbol
// on every platform whether it runs or not.
func ownerGID(fi fs.FileInfo) (int, bool) {
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return int(st.Gid), true
}

//go:build !linux

package sandbox

// hasACL has no answer off Linux, and the check that asks never runs there —
// bind-mount ownership is virtualized on the platforms this covers, so the host
// mode decides nothing in the first place.
//
// Split by build tag rather than by a runtime.GOOS branch for the reason
// owner_unix.go records: a GOOS check still compiles the unavailable symbol on
// every platform, and that is how the windows release build broke once already.
func hasACL(string) bool { return false }

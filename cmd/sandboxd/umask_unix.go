//go:build unix

package main

import "syscall"

// withPrivateUmask runs fn with a umask that makes new files owner-only.
func withPrivateUmask(fn func()) {
	old := syscall.Umask(0o177)
	defer syscall.Umask(old)
	fn()
}

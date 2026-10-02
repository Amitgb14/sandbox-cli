//go:build linux

package sandbox

import "syscall"

// hasACL reports whether path carries a POSIX access ACL.
//
// It exists so the writability check can decline to answer rather than answer
// wrongly. The mode bits are not the whole permission story on a filesystem
// where ACLs are in use — `setfacl -m g:devs:rwx` grants access that `ls -l`
// renders as a trailing `+` and nothing else — so a directory carrying one must
// not be called unwritable on the strength of bits that no longer decide it.
//
// syscall rather than golang.org/x/sys: this project's dependencies are the
// standard library plus cobra and yaml.v3, and one xattr read does not earn an
// exception.
//
// Linux-only, like the check that calls it, and named by the attribute the
// kernel actually stores. A filesystem without xattr support, or a path that
// cannot be read, answers "no ACL" — this is used to *suppress* a report, so the
// failure direction is to keep reporting rather than to go quiet.
func hasACL(path string) bool {
	sz, err := syscall.Getxattr(path, "system.posix_acl_access", nil)
	return err == nil && sz > 0
}

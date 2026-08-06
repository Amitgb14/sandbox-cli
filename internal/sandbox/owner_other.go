//go:build !unix

package sandbox

import "io/fs"

// ownerGID has no answer off unix: there is no owning gid to read, and the
// group-sharing that asks for one never runs there. Reporting "unknown" rather
// than a plausible 0 keeps the caller's own distinction intact — it treats
// unknown as "not the group I want", which is the safe direction.
func ownerGID(fs.FileInfo) (int, bool) { return 0, false }

// ownerUID likewise has no answer off unix, and the check that uses it is about a
// unix id mapping that does not arise there.
func ownerUID(fs.FileInfo) (int, bool) { return 0, false }

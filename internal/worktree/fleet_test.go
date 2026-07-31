package worktree

import "testing"

// A commit id from a client becomes a git argument, and a value beginning with a
// dash is read as an option — `--upload-pack=…` is how that ends badly. Hex or
// nothing; git decides whether the object exists, this decides whether the
// string is allowed to be a question.
func TestCommitLookupsRefuseAnythingButHex(t *testing.T) {
	for _, bad := range []string{
		"--upload-pack=/bin/sh",
		"-n",
		"HEAD",
		"main",
		"deadbeef; rm -rf /",
		"",
		"abc", // too short to be an id
		"0123456789012345678901234567890123456789beef", // too long
		"DEADBEEF", // upper case is not git's spelling
	} {
		if isHexSHA(bad) {
			t.Errorf("%q was accepted as an object id", bad)
		}
		if got := CommitStat(".", bad); got != nil {
			t.Errorf("CommitStat(%q) reached git and returned %v", bad, got)
		}
		if got := CommitFileDiff(".", bad, "x.go"); got != "" {
			t.Errorf("CommitFileDiff(%q) reached git", bad)
		}
	}
	for _, good := range []string{"deadbeef", "0123456789abcdef0123456789abcdef01234567"} {
		if !isHexSHA(good) {
			t.Errorf("%q is a valid object id and was refused", good)
		}
	}
}

package handoff

import (
	"os"
	"path/filepath"
	"testing"
)

// The briefing has to be readable by the container it is mounted into.
//
// It is bind-mounted read-only at GuestDir, and on native Linux the guest runs
// as 1001:<the host user's primary gid> — so a directory left at os.MkdirTemp's
// 0700 and files at 0600 give the fallback agent EACCES on every path its own
// prompt tells it to read. Nothing fails: the run has already printed that the
// briefing was carried. macOS hides it, because Docker Desktop virtualizes
// bind-mount ownership, which is why this is a test rather than something local
// use would catch.
func TestTheBriefingIsGroupReadable(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "export")
	if err := os.MkdirAll(dir, 0o700); err != nil { // as MkdirTemp leaves it
		t.Fatal(err)
	}
	if _, err := Write(dir, "claude", "", t.TempDir(), ""); err != nil {
		t.Fatal(err)
	}

	info, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if mode := info.Mode().Perm(); mode&0o050 != 0o050 {
		t.Errorf("export dir is %o, want the group to be able to enter and list it", mode)
	}
	// Not world-readable: /tmp is shared and the brief quotes a conversation.
	if mode := info.Mode().Perm(); mode&0o007 != 0 {
		t.Errorf("export dir is %o, want nothing for other", mode)
	}

	for _, name := range []string{"HANDOFF.md", "files.md", "transcript.jsonl"} {
		fi, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if mode := fi.Mode().Perm(); mode&0o040 == 0 {
			t.Errorf("%s is %o, want group-readable", name, mode)
		}
		if mode := fi.Mode().Perm(); mode&0o007 != 0 {
			t.Errorf("%s is %o, want nothing for other", name, mode)
		}
	}
}

// A brief is fed to another agent, so a truncated line has to stay valid UTF-8.
func TestFirstLineTruncatesOnRunes(t *testing.T) {
	long := ""
	for range 300 {
		long += "é"
	}
	got := firstLine(long)
	if !utf8ValidString(got) {
		t.Errorf("truncation produced invalid UTF-8: %q", got)
	}
	if want := 201; len([]rune(got)) != want { // 200 runes plus the ellipsis
		t.Errorf("kept %d runes, want %d", len([]rune(got)), want)
	}
}

func utf8ValidString(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}

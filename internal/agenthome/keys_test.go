package agenthome

import (
	"os"
	"path/filepath"
	"testing"
)

// A file left at path.tmp decides nothing about the write: a 0644 one does
// not make the result readable by others, and a link there is not written
// through. Both happened with os.WriteFile, which keeps an existing file's
// mode and follows a link.
func TestWritePrivateMakesItsTemporaryFileFresh(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "agent-keys.json")
	if err := os.WriteFile(path+".tmp", nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(path + ".tmp"); fi.Mode().Perm() != 0o644 {
		t.Fatalf("precondition: the leftover is %v", fi.Mode().Perm())
	}
	if err := WritePrivate(dir, "agent-keys.json", []byte("secret")); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(path); fi.Mode().Perm() != 0o600 {
		t.Errorf("written %v over a 0644 leftover, want 0600", fi.Mode().Perm())
	}

	target := filepath.Join(t.TempDir(), "elsewhere")
	if err := os.WriteFile(target, []byte("untouched"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, path+".tmp"); err != nil {
		t.Fatal(err)
	}
	if err := WritePrivate(dir, "agent-keys.json", []byte("secret2")); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(target); string(b) != "untouched" {
		t.Errorf("wrote through a link at the temporary path: %q", b)
	}
	if b, _ := os.ReadFile(path); string(b) != "secret2" {
		t.Errorf("file holds %q", b)
	}
}

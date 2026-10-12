package firecracker

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// The host grows the scratch file and nothing else: its contents are the
// guest's filesystem, which the host does not parse (the guest grows it as
// it boots). It is never shrunk.
func TestGrowDiskLengthensTheFileOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scratch.ext4")
	head := bytes.Repeat([]byte("guest-written "), 1000)
	if err := os.WriteFile(path, head, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Truncate(path, 64<<20); err != nil {
		t.Fatal(err)
	}
	if err := growDisk(path, 256); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(path); fi.Size() != 256<<20 {
		t.Fatalf("the file is %d bytes; want %d", fi.Size(), 256<<20)
	}
	got, _ := os.ReadFile(path)
	if !bytes.Equal(got[:len(head)], head) {
		t.Fatal("growing changed what the guest wrote")
	}
	if err := growDisk(path, 128); err != nil {
		t.Fatal(err)
	}
	if fi, _ := os.Stat(path); fi.Size() != 256<<20 {
		t.Fatalf("a smaller size shrank the file to %d", fi.Size())
	}
}

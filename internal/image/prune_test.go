package image

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPruneRootFS(t *testing.T) {
	dir := t.TempDir()
	agent := filepath.Join(t.TempDir(), "agent")
	_ = os.WriteFile(agent, []byte("current agent"), 0o755)
	cur, _ := fileSHA(agent)
	disk := func(key, label string) string {
		d := filepath.Join(dir, "rootfs", key)
		_ = os.MkdirAll(d, 0o755)
		_ = os.WriteFile(filepath.Join(d, "rootfs.ext4"), make([]byte, 64<<10), 0o644)
		if label != "" {
			_ = os.WriteFile(filepath.Join(d, agentFile), []byte(label), 0o644)
		}
		return d
	}
	current := disk("aaaa", agentLabel(cur))
	old := disk("bbbb", agentLabel("0123"))
	unlabelled := disk("cccc", "")
	oldInUse := disk("dddd", agentLabel("0123"))
	otherFormat := disk("eeee", "0:"+cur+"\n") // the right agent in a format no longer built
	staging := filepath.Join(dir, "rootfs", ".stage-1")
	_ = os.MkdirAll(staging, 0o700)

	n, freed, err := PruneRootFS(dir, agent, func(p string) bool { return p == filepath.Join(oldInUse, "rootfs.ext4") })
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{current, oldInUse, staging} {
		if _, err := os.Stat(d); err != nil {
			t.Errorf("%s was removed: %v", filepath.Base(d), err)
		}
	}
	for _, d := range []string{old, unlabelled, otherFormat} {
		if _, err := os.Stat(d); !os.IsNotExist(err) {
			t.Errorf("%s was kept", filepath.Base(d))
		}
	}
	if n != 3 || freed <= 0 {
		t.Fatalf("removed %d, freed %d", n, freed)
	}
	// Nothing to prune is not an error.
	if n, _, err := PruneRootFS(t.TempDir(), agent, nil); n != 0 || err != nil {
		t.Fatalf("an empty cache: %d, %v", n, err)
	}
}

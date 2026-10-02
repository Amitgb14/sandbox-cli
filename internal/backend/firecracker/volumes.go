//go:build linux

package firecracker

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/image"
)

// A volume is a sparse ext4 file under <state-dir>/volumes, attached to a VM as
// one more drive and mounted by the guest agent at boot (sbx.volumes). The host
// never mounts one: it is a filesystem a guest wrote, and parsing it is a guest
// kernel's job, where a malformed one costs that VM and nothing else.
//
// Beside each image is a small JSON record of its size and creation time — the
// file's own times change with every use.

type volumeMeta struct {
	SizeMB    int       `json:"size_mb"`
	CreatedAt time.Time `json:"created_at"`
}

func (b *Backend) volumesDir() string { return filepath.Join(b.cfg.StateDir, "volumes") }

func (b *Backend) volumePath(name string) string {
	return filepath.Join(b.volumesDir(), name+".ext4")
}

// CreateVolume formats a new volume. The name has been validated by the server
// (lowercase letters, digits and dashes), so it is safe as a file name.
func (b *Backend) CreateVolume(ctx context.Context, name string, sizeMB int) error {
	if err := os.MkdirAll(b.volumesDir(), 0o700); err != nil {
		return err
	}
	p := b.volumePath(name)
	if _, err := os.Lstat(p); err == nil {
		return fmt.Errorf("volume %s exists", name)
	}
	if err := image.MakeScratch(ctx, p, sizeMB); err != nil {
		_ = os.Remove(p)
		return err
	}
	meta, _ := json.Marshal(volumeMeta{SizeMB: sizeMB, CreatedAt: time.Now().UTC()})
	if err := os.WriteFile(strings.TrimSuffix(p, ".ext4")+".json", meta, 0o600); err != nil {
		_ = os.Remove(p)
		return err
	}
	return nil
}

func (b *Backend) DeleteVolume(_ context.Context, name string) error {
	p := b.volumePath(name)
	if _, err := os.Lstat(p); err != nil {
		return backend.ErrNotFound
	}
	_ = os.Remove(strings.TrimSuffix(p, ".ext4") + ".json")
	return os.Remove(p)
}

func (b *Backend) Volumes(context.Context) ([]backend.VolumeInfo, error) {
	entries, err := os.ReadDir(b.volumesDir())
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []backend.VolumeInfo
	for _, e := range entries {
		name, ok := strings.CutSuffix(e.Name(), ".json")
		if !ok || !e.Type().IsRegular() {
			continue
		}
		if fi, err := os.Lstat(b.volumePath(name)); err != nil || !fi.Mode().IsRegular() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(b.volumesDir(), e.Name()))
		if err != nil {
			continue
		}
		var m volumeMeta
		if json.Unmarshal(raw, &m) != nil {
			continue
		}
		out = append(out, backend.VolumeInfo{Name: name, SizeMB: m.SizeMB, CreatedAt: m.CreatedAt})
	}
	return out, nil
}

// volumeBootArg tells the guest agent which drive goes where: the volumes are
// the drives after the root and scratch disks, vdc onwards, in the spec's
// order. Paths were validated to letters, digits and . _ - /, so nothing in
// them can split a kernel argument.
func volumeBootArg(s backend.Spec) string {
	if len(s.Volumes) == 0 {
		return ""
	}
	parts := make([]string, len(s.Volumes))
	for i, m := range s.Volumes {
		mode := "rw"
		if m.ReadOnly {
			mode = "ro"
		}
		parts[i] = fmt.Sprintf("vd%c:%s:%s", 'c'+i, m.Path, mode)
	}
	return "sbx.volumes=" + strings.Join(parts, ",")
}

// flushVolumes asks the guest to write its page cache out before the VMM is
// stopped. Killing a VMM takes the guest's unwritten pages with it; for the
// scratch disk that does not matter, since it is discarded, but a volume is
// meant to keep what was written. Best-effort, and logged when it fails.
func (b *Backend) flushVolumes(v *vm, final bool) {
	if len(v.spec.Volumes) == 0 || v.client == nil || v.suspended {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := v.client.Sync(ctx, final); err != nil {
		b.cfg.Logf("sandbox %s: flushing volumes before stopping: %v", v.id, err)
	}
}

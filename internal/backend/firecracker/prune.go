//go:build linux

package firecracker

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/Amitgb14/sandbox-cli/internal/image"
)

// pruneRootDisks removes the cached root disks no guest agent of this build
// will use and no VM still needs (image.PruneRootFS), so upgrades do not fill
// the state directory with a full set of disks each.
//
// A VM under the jailer has its disk hard-linked into its jail, so the cached
// name can go while it runs, and a resume uses the jail's link. Without the
// jailer a VM names the cached file itself, and a suspended one is resumed
// from it, so those are kept: the paths its vm.json gives. A VM forked from a
// snapshot has no vm.json to say; if one is kept without the jailer, nothing
// is pruned this time rather than guessed.
func (b *Backend) pruneRootDisks(kept []candidate) {
	inUse := map[string]bool{}
	for _, c := range kept {
		if b.cfg.Jailer != nil {
			continue
		}
		data, err := os.ReadFile(c.v.hostPath("vm.json"))
		if err != nil {
			b.cfg.Logf("root disks: not pruned: sandbox %s's disk is not known", c.v.id)
			return
		}
		var vc VMConfig
		if json.Unmarshal(data, &vc) != nil {
			b.cfg.Logf("root disks: not pruned: sandbox %s's vm.json is unreadable", c.v.id)
			return
		}
		for _, d := range vc.Drives {
			if d.IsRootDevice {
				inUse[filepath.Clean(d.PathOnHost)] = true
			}
		}
	}
	n, freed, err := image.PruneRootFS(b.cfg.ImageDir, b.cfg.Agent, func(p string) bool { return inUse[filepath.Clean(p)] })
	if err != nil {
		b.cfg.Logf("root disks: pruning: %v", err)
		return
	}
	if n > 0 {
		size := fmt.Sprintf("%.1f GiB", float64(freed)/(1<<30))
		if freed < 1<<30 {
			size = fmt.Sprintf("%d MiB", freed>>20)
		}
		b.cfg.Logf("root disks: removed %d built for another guest agent, freeing %s", n, size)
	}
}

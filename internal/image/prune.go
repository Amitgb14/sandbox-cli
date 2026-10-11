package image

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// Root disks are cached by image and guest agent, and every build of sandboxd
// carries its own guest agent: a disk is put on every disk image's root, and
// the agent must match the server. So each upgrade builds a fresh disk for
// every image the first time it is used, a few gigabytes each, and the disks
// for the agent before it are never used again. Nothing removed them, and a
// state directory filled its filesystem after a handful of upgrades.
//
// Each disk now records, in agentFile beside it, the guest agent it was built
// for, and PruneRootFS removes at start the ones for any other agent — except
// those a VM still running, or suspended, was booted from, which the caller
// names. A disk from before the label, which says nothing, is treated as one
// for another agent: the current agent's disks are labelled as they are used.

// agentFile holds the format and the guest agent a disk was built for.
const agentFile = "agent"

func agentLabel(agentSum string) string { return rootfsFormat + ":" + agentSum + "\n" }

// labelAgent records which agent the disk in dir holds. A failure costs only
// the disk being rebuilt after the next upgrade, so it is not reported.
func labelAgent(dir, agentSum string) {
	p := filepath.Join(dir, agentFile)
	if b, err := os.ReadFile(p); err == nil && string(b) == agentLabel(agentSum) {
		return
	}
	_ = os.WriteFile(p, []byte(agentLabel(agentSum)), 0o644)
}

// PruneRootFS removes the root disks under dir not built for agent (the
// sandbox-guestd binary this server puts in new disks), keeping any whose
// path inUse reports. It returns how many it removed and the bytes freed.
// Call it before any disk is built or a VM booted from one, so nothing
// under the cache is being written.
func PruneRootFS(dir, agent string, inUse func(path string) bool) (removed int, freed int64, err error) {
	agentSum, err := fileSHA(agent)
	if err != nil {
		return 0, 0, err
	}
	base := filepath.Join(dir, "rootfs")
	des, err := os.ReadDir(base)
	if os.IsNotExist(err) {
		return 0, 0, nil
	}
	if err != nil {
		return 0, 0, err
	}
	want := agentLabel(agentSum)
	for _, de := range des {
		if !de.IsDir() || strings.HasPrefix(de.Name(), ".") {
			continue
		}
		d := filepath.Join(base, de.Name())
		if label, err := os.ReadFile(filepath.Join(d, agentFile)); err == nil && string(label) == want {
			continue
		}
		if inUse != nil && inUse(filepath.Join(d, "rootfs.ext4")) {
			continue
		}
		size := dirBytes(d)
		if err := os.RemoveAll(d); err != nil {
			continue
		}
		removed++
		freed += size
	}
	return removed, freed, nil
}

// dirBytes is the space the files under d take on disk.
func dirBytes(d string) int64 {
	var n int64
	_ = filepath.WalkDir(d, func(_ string, e fs.DirEntry, err error) error {
		if err == nil && !e.IsDir() {
			if fi, err := e.Info(); err == nil {
				n += diskUsage(fi)
			}
		}
		return nil
	})
	return n
}

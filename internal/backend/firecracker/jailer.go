//go:build linux

package firecracker

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Jailer runs each VMM under Firecracker's jailer: a chroot holding only that
// sandbox's files, its own mount and PID namespaces, and an unprivileged uid of
// its own. Measured in M3 at no cost worth counting (a guest ready in 60 ms).
//
// One uid per sandbox, not one for all of them: two VMMs sharing a uid could
// signal or trace each other, and a VMM is exactly what a guest escape would
// land in.
type Jailer struct {
	Path       string // the jailer binary
	ChrootBase string // must be on the same filesystem as the image cache: disks are hard-linked in
	UIDBase    int    // sandbox n runs as UIDBase+n

	mu   sync.Mutex
	used map[int]bool
	uids map[string]int
}

// jailID is a sandbox id in the form the jailer accepts: alphanumerics and dashes.
func jailID(id string) string { return strings.ReplaceAll(id, "_", "-") }

func (j *Jailer) root(id string) string {
	return filepath.Join(j.ChrootBase, "firecracker", jailID(id), "root")
}

func (j *Jailer) chrootPath(id, name string) string { return filepath.Join(j.root(id), name) }

func (j *Jailer) uid(id string) int {
	j.mu.Lock()
	defer j.mu.Unlock()
	if u, ok := j.uids[id]; ok {
		return u
	}
	if j.used == nil {
		j.used, j.uids = map[int]bool{}, map[string]int{}
	}
	n := 0
	for j.used[n] {
		n++
	}
	j.used[n] = true
	j.uids[id] = j.UIDBase + n
	return j.UIDBase + n
}

// prepare puts the kernel, the shared root disk and the scratch disk into the
// chroot and returns the paths as the VMM will see them.
func (j *Jailer) prepare(id, kernel, rootfs, scratch string, volumes []string) (vmPaths, error) {
	if os.Geteuid() != 0 {
		return vmPaths{}, errors.New("the jailer needs root")
	}
	root := j.root(id)
	if err := os.MkdirAll(root, 0o700); err != nil {
		return vmPaths{}, err
	}
	uid := j.uid(id)
	for name, src := range map[string]string{"vmlinux": kernel, "rootfs.ext4": rootfs} {
		dst := filepath.Join(root, name)
		// A hard link, because the root disk is gigabytes and shared: copying it
		// per sandbox is the cost the overlay design exists to avoid. It stays
		// owned by root and read-only to the VMM's uid.
		if err := os.Link(src, dst); err != nil {
			return vmPaths{}, fmt.Errorf("linking %s into the jail (the jail must be on the same filesystem as the image cache): %w", name, err)
		}
	}
	if scratch != "" {
		if err := os.Rename(scratch, filepath.Join(root, "scratch.ext4")); err != nil {
			return vmPaths{}, fmt.Errorf("moving the scratch disk into the jail: %w", err)
		}
		if err := os.Chown(filepath.Join(root, "scratch.ext4"), uid, uid); err != nil {
			return vmPaths{}, err
		}
	}
	// Volumes are hard links too — they outlive the jail — owned by this VM's
	// uid while it runs. destroy hands them back to sandboxd.
	var vols []string
	for i, src := range volumes {
		name := fmt.Sprintf("vol%d.ext4", i)
		dst := filepath.Join(root, name)
		if err := os.Link(src, dst); err != nil {
			return vmPaths{}, fmt.Errorf("linking a volume into the jail (the jail must be on the same filesystem as the state directory): %w", err)
		}
		if err := os.Chown(dst, uid, uid); err != nil {
			return vmPaths{}, err
		}
		vols = append(vols, "/"+name)
	}
	return vmPaths{Kernel: "/vmlinux", RootFS: "/rootfs.ext4", Scratch: "/scratch.ext4", VsockUDS: "/v.sock", Volumes: vols}, nil
}

func (j *Jailer) own(id, path string) {
	u := j.uid(id)
	_ = os.Chown(path, u, u)
}

// command is the jailer invocation; the VMM's own arguments follow "--".
func (j *Jailer) command(id, firecracker string, args ...string) *exec.Cmd {
	u := fmt.Sprint(j.uid(id))
	return exec.Command(j.Path, append([]string{
		"--id", jailID(id),
		"--exec-file", firecracker,
		"--uid", u, "--gid", u,
		"--chroot-base-dir", j.ChrootBase,
		"--", "--api-sock", "api.sock",
	}, args...)...)
}

// adopt records the uid of a VM taken back from an earlier sandboxd, so it is
// neither handed to another VM nor lost when this one ends.
func (j *Jailer) adopt(id string, uid int) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.used == nil {
		j.used, j.uids = map[int]bool{}, map[string]int{}
	}
	j.used[uid-j.UIDBase] = true
	j.uids[id] = uid
}

func (j *Jailer) cleanup(id string) {
	_ = os.RemoveAll(filepath.Join(j.ChrootBase, "firecracker", jailID(id)))
	j.mu.Lock()
	if u, ok := j.uids[id]; ok {
		delete(j.used, u-j.UIDBase)
		delete(j.uids, id)
	}
	j.mu.Unlock()
}

// ownerFor is the uid the VMM runs as, for the tap device's owner; -1 without a jailer.
func (b *Backend) ownerFor(id string) int {
	if b.cfg.Jailer == nil {
		return -1
	}
	return b.cfg.Jailer.uid(id)
}

// cleanupProcess removes what a previous run of the jailer created in the
// chroot — device nodes and sockets — so it can start the same jail again on
// resume. The sandbox's files (disks, snapshots) stay.
func (j *Jailer) cleanupProcess(id string) {
	root := j.root(id)
	for _, p := range []string{"dev", "run", "api.sock", "v.sock"} {
		_ = os.RemoveAll(filepath.Join(root, p))
	}
}

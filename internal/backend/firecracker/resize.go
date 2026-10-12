//go:build linux

package firecracker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
)

// Resizing a sandbox.
//
// Firecracker cannot add vCPUs or memory to a running VM, so a resize is a
// restart in place: the guest flushes its disks, the VMM is stopped, the
// scratch file is lengthened if asked (the guest grows its filesystem into
// it as it boots), and a new VMM boots from the same disks,
// tap and jail with the new machine configuration. The sandbox keeps its
// id, its network, the files it wrote and its volumes; its processes end,
// which is why the server refuses a resize while any run.
//
// Fail closed: if the guest does not flush, nothing is stopped. If the new
// size does not boot (the host has not the memory, say), the old CPUs and
// memory are booted again, so a refused resize does not cost the sandbox.

// ResizeSandbox is backend.SandboxResizer.
func (b *Backend) ResizeSandbox(ctx context.Context, id string, size api.NodeResources) (api.NodeResources, error) {
	// The restart outlives the request: a client that goes away halfway
	// must not leave the VM stopped.
	ctx = context.WithoutCancel(ctx)
	v, err := b.get(id)
	if err != nil {
		return api.NodeResources{}, err
	}
	v.restart.Lock()
	defer v.restart.Unlock()
	if v, err = b.get(id); err != nil { // terminated while waiting
		return api.NodeResources{}, err
	}
	data, err := os.ReadFile(v.hostPath("vm.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return sizeOf(v.spec), fmt.Errorf("%w: a sandbox started from a memory snapshot keeps the snapshot's size", backend.ErrUnsupported)
	}
	if err != nil {
		return sizeOf(v.spec), err
	}
	var cfg VMConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return sizeOf(v.spec), fmt.Errorf("vm.json: %w", err)
	}
	old := v.spec
	was := sizeOf(old)
	if size.DiskMB < old.DiskMB {
		return was, fmt.Errorf("%w: a disk only grows", backend.ErrUnsupported)
	}

	// Its scratch disk is kept this time, so everything the guest has
	// written goes out first; a guest that cannot is not stopped.
	sctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	err = v.client.Sync(sctx, false)
	cancel()
	if err != nil {
		b.cfg.Logf("sandbox %s: resize: the guest did not flush its disks: %v", id, err)
		return was, fmt.Errorf("%w: the guest did not flush its disks, so it was not restarted", backend.ErrBusy)
	}
	b.flushVolumes(v, false)
	stopVMM(v)

	next := old
	next.CPUs, next.MemoryMB = size.CPUs, size.MemoryMB
	if size.DiskMB > old.DiskMB {
		if err := growDisk(filepath.Join(v.dir, "scratch.ext4"), size.DiskMB); err != nil {
			b.cfg.Logf("sandbox %s: resize: growing its disk: %v", id, err)
		} else {
			next.DiskMB = size.DiskMB
		}
	}

	t0 := time.Now()
	berr := b.boot(ctx, v, cfg, next)
	if berr == nil {
		b.cfg.Logf("sandbox %s: resized to %v CPUs, %d MiB, disk %d MiB; ready in %v", id, next.CPUs, next.MemoryMB, next.DiskMB, time.Since(t0).Round(time.Millisecond))
		b.setSpec(v, next)
		if next.DiskMB != size.DiskMB {
			return sizeOf(next), errors.New("the disk could not be grown; the CPUs and memory were changed")
		}
		return sizeOf(next), nil
	}
	b.cfg.Logf("sandbox %s: resize: did not boot at %v CPUs, %d MiB: %v; booting it at its old size", id, next.CPUs, next.MemoryMB, berr)
	back := old
	back.DiskMB = next.DiskMB // a grown disk stays grown
	if err := b.boot(ctx, v, cfg, back); err != nil {
		b.cfg.Logf("sandbox %s: resize: did not boot at its old size either: %v", id, err)
		return sizeOf(back), fmt.Errorf("the sandbox did not start at the new size or its old one: %w", err)
	}
	b.setSpec(v, back)
	return sizeOf(back), fmt.Errorf("the sandbox did not start at the new size and is back at its old one: %w", berr)
}

func sizeOf(s backend.Spec) api.NodeResources {
	return api.NodeResources{CPUs: s.CPUs, MemoryMB: s.MemoryMB, DiskMB: s.DiskMB}
}

// stopVMM ends a VMM and waits for it.
func stopVMM(v *vm) {
	killVMM(v)
	select {
	case <-v.exited:
	case <-time.After(10 * time.Second):
	}
}

// boot starts v's VMM from cfg with s's machine and waits for its guest.
// One that does not come up is stopped again.
func (b *Backend) boot(ctx context.Context, v *vm, cfg VMConfig, s backend.Spec) error {
	cfg.MachineConfig = machineFor(s)
	out, _ := json.MarshalIndent(cfg, "", "  ")
	if err := os.WriteFile(v.hostPath("vm.json"), out, 0o600); err != nil {
		return err
	}
	b.own(v.id, v.hostPath("vm.json"))
	if b.cfg.Jailer != nil {
		b.cfg.Jailer.cleanupProcess(v.id) // the jailer recreates what it needs on each start
	}
	if err := b.vmm(v, "--config-file", "vm.json"); err != nil {
		return err
	}
	if err := b.waitReady(ctx, v); err != nil {
		stopVMM(v)
		return err
	}
	return nil
}

func (b *Backend) setSpec(v *vm, s backend.Spec) {
	b.mu.Lock()
	v.spec = s
	b.mu.Unlock()
	b.saveKeep(v)
}

// growDisk makes the scratch file mib MiB. Only the file: the filesystem
// in it is the guest's, written by it, and the host does not parse it — the
// guest agent grows it to fill the larger device when it next boots
// (sandbox-guestd, growToDevice). A file is never shrunk.
func growDisk(path string, mib int) error {
	fi, err := os.Stat(path)
	if err != nil {
		return err
	}
	if want := int64(mib) << 20; want > fi.Size() {
		return os.Truncate(path, want)
	}
	return nil
}

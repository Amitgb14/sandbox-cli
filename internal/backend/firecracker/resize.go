//go:build linux

package firecracker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/guestproto"
)

// Resizing a sandbox.
//
// A disk that only grows is grown live (growLive): the VMM rereads a
// lengthened file under a running VM, and the guest grows its filesystem
// online, its processes running on. A guest agent older than that, or a
// live grow that fails, falls back to a restart where the caller allows one.
//
// Firecracker cannot add vCPUs or memory to a running VM, so any other
// resize is a restart in place: the guest flushes its disks, the VMM is stopped, the
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
func (b *Backend) ResizeSandbox(ctx context.Context, id string, size api.NodeResources, mayRestart bool) (api.NodeResources, bool, error) {
	// The resize outlives the request: a client that goes away halfway
	// must not leave the VM stopped.
	ctx = context.WithoutCancel(ctx)
	v, err := b.get(id)
	if err != nil {
		return api.NodeResources{}, false, err
	}
	if v.cloudHV {
		return sizeOf(v.spec), false, unsupportedGPU("resized")
	}
	v.restart.Lock()
	defer v.restart.Unlock()
	if v, err = b.get(id); err != nil { // terminated while waiting
		return api.NodeResources{}, false, err
	}
	old := v.spec
	was := sizeOf(old)
	if size.DiskMB < old.DiskMB {
		return was, false, fmt.Errorf("%w: a disk only grows", backend.ErrUnsupported)
	}
	if size.CPUs == old.CPUs && size.MemoryMB == old.MemoryMB {
		err := b.growLive(ctx, v, size.DiskMB)
		if err == nil {
			next := old
			next.DiskMB = size.DiskMB
			b.setSpec(v, next)
			b.cfg.Logf("sandbox %s: grew its disk to %d MiB live", id, size.DiskMB)
			return sizeOf(next), false, nil
		}
		b.cfg.Logf("sandbox %s: resize: the disk could not be grown live: %v", id, err)
		// A file already lengthened is lengthened again harmlessly by the
		// restart, whose boot grows the filesystem into it.
	}
	if !mayRestart {
		return was, false, fmt.Errorf("%w: this resize restarts the sandbox, and processes are running", backend.ErrBusy)
	}
	now, err := b.restartAt(ctx, v, size)
	return now, true, err
}

// growLive grows a running sandbox's scratch disk to mib MiB without
// stopping it: the file is lengthened, the VMM told to look at it again —
// which tells the guest's kernel — and the guest asked to grow its
// filesystem into the larger device. A guest agent too old to do the last
// is found out first, by its protocol version, so nothing is changed for
// one that cannot finish.
func (b *Backend) growLive(ctx context.Context, v *vm, mib int) error {
	pctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	ver, err := v.client.Ping(pctx)
	cancel()
	if err != nil {
		return fmt.Errorf("the guest agent: %w", err)
	}
	if n, err := strconv.Atoi(ver); err != nil || n < guestproto.GrowScratchSince {
		return fmt.Errorf("the guest agent speaks protocol %q, older than live growth", ver)
	}
	// hostPath: under a jailer the scratch disk was moved into the jail.
	if err := growDisk(v.hostPath("scratch.ext4"), mib); err != nil {
		return err
	}
	// The drive's path as the VMM was given it: in the jail's root under a
	// jailer, beside it otherwise (vmPaths). Setting it again makes the
	// VMM reread the file's size and signal the guest.
	path := "scratch.ext4"
	if v.root != "" {
		path = "/scratch.ext4"
	}
	actx, cancel := context.WithTimeout(ctx, 10*time.Second)
	err = fcAPI(actx, v.hostPath("api.sock"), http.MethodPatch, "/drives/scratch", map[string]string{"drive_id": "scratch", "path_on_host": path})
	cancel()
	if err != nil {
		return fmt.Errorf("telling the VMM: %w", err)
	}
	gctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := v.client.GrowScratch(gctx, int64(mib)<<20); err != nil {
		return fmt.Errorf("the guest growing its filesystem: %w", err)
	}
	return nil
}

// restartAt is a resize by restart: see the top of this file.
func (b *Backend) restartAt(ctx context.Context, v *vm, size api.NodeResources) (api.NodeResources, error) {
	id := v.id
	old := v.spec
	was := sizeOf(old)
	data, err := os.ReadFile(v.hostPath("vm.json"))
	if errors.Is(err, fs.ErrNotExist) {
		return was, fmt.Errorf("%w: a sandbox started from a memory snapshot keeps the snapshot's size", backend.ErrUnsupported)
	}
	if err != nil {
		return was, err
	}
	var cfg VMConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return was, fmt.Errorf("vm.json: %w", err)
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
		// hostPath: under a jailer the scratch disk was moved into the jail.
		if err := growDisk(v.hostPath("scratch.ext4"), size.DiskMB); err != nil {
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

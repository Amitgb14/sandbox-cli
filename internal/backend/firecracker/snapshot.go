//go:build linux

package firecracker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/image"
)

// Suspend, resume and fork use Firecracker's full snapshots: guest memory and
// device state in two files, restored into a fresh VMM. Measured in M3: a
// 1 GiB guest snapshots in ~230 ms and restores in ~6 ms, with the guest
// answering ~9 ms after the load starts.
//
// A fork with a network is not offered. A snapshot carries the guest's address
// and its tap's name, so two forks would claim one network identity; giving
// each its own needs a network namespace per VM, which is a later step. Until
// then a networked sandbox can be suspended and resumed (it keeps its tap), but
// not snapshotted — refused, not half-done.

// fcAPI calls the VMM's API over its unix socket.
func fcAPI(ctx context.Context, sock, method, path string, body any) error {
	tr := &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}}
	defer tr.CloseIdleConnections()
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, "http://localhost"+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Transport: tr, Timeout: 2 * time.Minute}).Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("firecracker %s %s: %s: %s", method, path, resp.Status, bytes.TrimSpace(msg))
	}
	return nil
}

// waitSock waits for a VMM's API socket to accept connections.
func waitSock(ctx context.Context, sock string, exited <-chan struct{}) error {
	for {
		if c, err := net.Dial("unix", sock); err == nil {
			c.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-exited:
			return errors.New("the VMM exited before its API came up")
		case <-time.After(2 * time.Millisecond):
		}
	}
}

func (b *Backend) find(id string) (*vm, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	v, ok := b.vms[id]
	if !ok {
		return nil, backend.ErrNotFound
	}
	return v, nil
}

// snapshotTo pauses the VM and writes its state and memory to the two files
// (named relative to the VMM's directory, or absolute).
func (b *Backend) snapshotTo(ctx context.Context, v *vm, state, mem string) error {
	sock := v.hostPath("api.sock")
	if err := fcAPI(ctx, sock, http.MethodPatch, "/vm", map[string]string{"state": "Paused"}); err != nil {
		return err
	}
	return fcAPI(ctx, sock, http.MethodPut, "/snapshot/create", map[string]string{
		"snapshot_type": "Full", "snapshot_path": state, "mem_file_path": mem,
	})
}

// Suspend snapshots the VM into its own directory and stops the VMM. Its tap,
// its scratch disk and its snapshot stay; nothing runs.
func (b *Backend) Suspend(ctx context.Context, id string) error {
	v, err := b.get(id)
	if err != nil {
		return err
	}
	// Volumes are written out first, so what is on their disks while the VM
	// is suspended is complete: another process may read the image, and a
	// suspended sandbox may be terminated without ever running again.
	b.flushVolumes(v, false)
	if err := b.snapshotTo(ctx, v, "suspend.state", "suspend.mem"); err != nil {
		return err
	}
	_ = syscall.Kill(-v.cmd.Process.Pid, syscall.SIGKILL)
	<-v.exited
	b.mu.Lock()
	v.suspended = true
	b.mu.Unlock()
	return nil
}

// Resume restores the VM from its suspend snapshot into a fresh VMM.
func (b *Backend) Resume(ctx context.Context, id string) error {
	v, err := b.find(id)
	if err != nil {
		return err
	}
	if !v.suspended {
		return backend.ErrBusy
	}
	if err := b.restore(ctx, v, "suspend.state", "suspend.mem"); err != nil {
		return err
	}
	// The memory file is mapped by the running VMM; removing its name frees the
	// space once the mapping goes, and the state file is spent.
	_ = os.Remove(v.hostPath("suspend.state"))
	_ = os.Remove(v.hostPath("suspend.mem"))
	b.mu.Lock()
	v.suspended = false
	b.mu.Unlock()
	return nil
}

// restore starts a VMM with only its API, loads a snapshot and resumes it.
func (b *Backend) restore(ctx context.Context, v *vm, state, mem string) error {
	if b.cfg.Jailer != nil {
		b.cfg.Jailer.cleanupProcess(v.id) // the jailer recreates what it needs on each start
	}
	if err := b.vmm(v); err != nil {
		return err
	}
	sctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	if err := waitSock(sctx, v.hostPath("api.sock"), v.exited); err != nil {
		return err
	}
	err := fcAPI(ctx, v.hostPath("api.sock"), http.MethodPut, "/snapshot/load", map[string]any{
		"snapshot_path": state,
		"mem_backend":   map[string]string{"backend_type": "File", "backend_path": mem},
		"resume_vm":     true,
	})
	if err != nil {
		return err
	}
	return b.waitReady(ctx, v)
}

func (b *Backend) snapshotDir(id string) string {
	return filepath.Join(b.cfg.StateDir, "snapshots", id)
}

// Snapshot captures a running sandbox — memory, device state and its scratch
// disk — without stopping it: the VM is paused for the capture and resumed.
func (b *Backend) Snapshot(ctx context.Context, id, snapshotID string) (backend.SnapshotInfo, error) {
	v, err := b.get(id)
	if err != nil {
		return backend.SnapshotInfo{}, err
	}
	if v.net != nil {
		return backend.SnapshotInfo{}, fmt.Errorf("%w: a snapshot of a networked sandbox would give every fork its network identity; snapshot sandboxes with network mode none", backend.ErrUnsupported)
	}
	dir := b.snapshotDir(snapshotID)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return backend.SnapshotInfo{}, err
	}
	// Written inside the VM's own directory (the jail, when there is one), then
	// moved out, so the jailed VMM never needs a path outside its root.
	err = b.snapshotTo(ctx, v, "snap.state", "snap.mem")
	if err == nil {
		err = reflinkCopy(v.hostPath("scratch.ext4"), filepath.Join(dir, "scratch.ext4"))
	}
	if rerr := fcAPI(ctx, v.hostPath("api.sock"), http.MethodPatch, "/vm", map[string]string{"state": "Resumed"}); err == nil {
		err = rerr
	}
	for _, f := range []string{"snap.state", "snap.mem"} {
		if err == nil {
			err = os.Rename(v.hostPath(f), filepath.Join(dir, f))
		} else {
			_ = os.Remove(v.hostPath(f))
		}
	}
	if err != nil {
		_ = os.RemoveAll(dir)
		return backend.SnapshotInfo{}, err
	}
	meta, _ := json.Marshal(v.spec)
	_ = os.WriteFile(filepath.Join(dir, "spec.json"), meta, 0o600)
	return backend.SnapshotInfo{
		ID: snapshotID, Bytes: diskUsage(dir), Image: v.spec.Image,
		CPUs: v.spec.CPUs, MemoryMB: v.spec.MemoryMB, DiskMB: v.spec.DiskMB,
	}, nil
}

func (b *Backend) DeleteSnapshot(_ context.Context, snapshotID string) error {
	return os.RemoveAll(b.snapshotDir(snapshotID))
}

// createFromSnapshot starts a sandbox from a snapshot: its own copy of the
// scratch disk and of the memory file (reflinks where the filesystem shares
// blocks), restored into a fresh VMM in its own directory.
func (b *Backend) createFromSnapshot(ctx context.Context, s backend.Spec) (err error) {
	if s.Network.Mode != "" && s.Network.Mode != "none" {
		return fmt.Errorf("%w: a sandbox started from a snapshot has no network on this backend", backend.ErrUnsupported)
	}
	src := b.snapshotDir(s.FromSnapshot)
	var orig backend.Spec
	if data, rerr := os.ReadFile(filepath.Join(src, "spec.json")); rerr != nil || json.Unmarshal(data, &orig) != nil {
		return backend.ErrNotFound
	}
	v, err := b.newVM(s)
	if err != nil {
		return err
	}
	defer func() {
		if err != nil {
			b.destroy(v)
		}
	}()
	if b.cfg.Jailer != nil {
		rootfs, rerr := b.rootfsFor(ctx, orig.Image)
		if rerr != nil {
			return rerr
		}
		if _, err = b.cfg.Jailer.prepare(s.ID, b.cfg.Kernel, rootfs, "", nil); err != nil {
			return err
		}
	}
	for _, f := range []string{"scratch.ext4", "snap.state", "snap.mem"} {
		if err = reflinkCopy(filepath.Join(src, f), v.hostPath(f)); err != nil {
			return err
		}
		b.own(s.ID, v.hostPath(f))
	}
	t0 := time.Now()
	if err = b.restore(ctx, v, "snap.state", "snap.mem"); err != nil {
		return err
	}
	_ = os.Remove(v.hostPath("snap.state"))
	b.cfg.Logf("sandbox %s: forked from %s in %v", s.ID, s.FromSnapshot, time.Since(t0).Round(time.Millisecond))
	b.mu.Lock()
	b.vms[s.ID] = v
	b.mu.Unlock()
	return nil
}

// rootfsFor finds an image's root disk again (from the cache), for linking
// into a fork's jail.
func (b *Backend) rootfsFor(ctx context.Context, ref string) (string, error) {
	r, err := image.BuildRootFS(ctx, b.cfg.Puller, ref, b.cfg.Agent, b.cfg.ImageDir)
	if err != nil {
		return "", err
	}
	return r.Path, nil
}

// reflinkCopy copies a file, sharing blocks where the filesystem can (xfs,
// btrfs): a gigabyte in milliseconds there, a plain copy elsewhere.
func reflinkCopy(src, dst string) error {
	out, err := exec.Command("cp", "--reflink=auto", "--sparse=always", src, dst).CombinedOutput()
	if err != nil {
		return fmt.Errorf("copying %s: %v: %s", filepath.Base(src), err, bytes.TrimSpace(out))
	}
	return nil
}

func diskUsage(dir string) int64 {
	var total int64
	_ = filepath.Walk(dir, func(_ string, fi os.FileInfo, err error) error {
		if err == nil && !fi.IsDir() {
			if st, ok := fi.Sys().(*syscall.Stat_t); ok {
				total += st.Blocks * 512
			}
		}
		return nil
	})
	return total
}

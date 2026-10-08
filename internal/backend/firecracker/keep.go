//go:build linux

package firecracker

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/backend"
)

// Keeping VMs across a restart of sandboxd (Config.Keep, backend.Keeper).
//
// Each VMM runs in a session of its own, so it does not share sandboxd's
// fate: a signal to sandboxd's process group, or sandboxd's exit, leaves it
// running. (Under systemd the unit must also say KillMode=process, or the
// whole control group is killed on stop.) Beside each VM's other host-side
// files, keep.json records what a later sandboxd needs to serve it again: the
// spec without its environment values — which, like vm.json, this backend
// never writes — the tap, the jail's uid, the VMM's pid with its start time,
// and whether it is suspended. It is rewritten whenever one of those changes.
//
// On start a VM is taken back only when everything agrees: its keep.json is
// readable, its VMM is the same process (pid and start time, so a recycled pid
// is never taken for it), its tap still exists, and its guest agent answers.
// A suspended VM has no process; its snapshot files must be there instead.
// Anything else is removed exactly as reapLeftovers removes it. The
// directory is the host's own, under the state directory and outside the
// jail, so nothing in the guest can write a keep.json.

const keepFile = "keep.json"

// keepState is keep.json.
type keepState struct {
	Spec      backend.Spec `json:"spec"` // Env always empty
	Tap       int          `json:"tap"`  // tap index, -1 without networking
	UID       int          `json:"uid"`  // the jail's uid, -1 without the jailer
	PID       int          `json:"pid"`
	Start     uint64       `json:"start"` // the VMM's start time, in clock ticks since boot
	Suspended bool         `json:"suspended"`
}

// saveKeep writes v's keep.json, when VMs are kept. Called with whatever lock
// guards the change it records already released.
func (b *Backend) saveKeep(v *vm) {
	if !b.cfg.Keep {
		return
	}
	b.mu.Lock()
	ks := keepState{Spec: v.spec, Tap: -1, UID: -1, PID: v.pid, Start: v.start, Suspended: v.suspended}
	b.mu.Unlock()
	ks.Spec.Env = nil
	if v.net != nil {
		ks.Tap = v.net.idx
		ks.Spec.Network = *v.net.policy.Load()
	}
	if b.cfg.Jailer != nil {
		ks.UID = b.cfg.Jailer.uid(v.id)
	}
	data, _ := json.Marshal(ks)
	tmp := filepath.Join(v.dir, keepFile+".tmp")
	if err := os.WriteFile(tmp, data, 0o600); err == nil {
		err = os.Rename(tmp, filepath.Join(v.dir, keepFile))
		if err != nil {
			b.cfg.Logf("sandbox %s: recording it for a restart: %v", v.id, err)
		}
	}
}

// procStart is a process's start time in clock ticks since boot, field 22 of
// /proc/<pid>/stat. With the pid it names one process for the life of the
// machine: a pid can be reused, a pid and a start time cannot.
func procStart(pid int) (uint64, error) {
	data, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		return 0, err
	}
	return parseStatStart(data)
}

// parseStatStart reads the start time from a /proc/<pid>/stat line. The
// command name (field 2) is in parentheses and may itself hold spaces and
// parentheses, so the fields are counted from the last ')'.
func parseStatStart(stat []byte) (uint64, error) {
	i := bytes.LastIndexByte(stat, ')')
	if i < 0 {
		return 0, errors.New("malformed stat")
	}
	fields := strings.Fields(string(stat[i+1:]))
	// fields[0] is field 3 (state), so field 22 is fields[19].
	if len(fields) < 20 {
		return 0, errors.New("malformed stat")
	}
	return strconv.ParseUint(fields[19], 10, 64)
}

// sameProcess reports whether pid is still the process that started at start.
func sameProcess(pid int, start uint64) bool {
	if pid <= 1 || start == 0 {
		return false
	}
	got, err := procStart(pid)
	return err == nil && got == start
}

// watch closes v.exited when a VMM this process did not start ends. A child is
// reaped by cmd.Wait instead; a VMM taken back is not a child, so it is
// watched by pid and start time.
func watch(pid int, start uint64, exited chan struct{}) {
	go func() {
		for sameProcess(pid, start) {
			time.Sleep(500 * time.Millisecond)
		}
		close(exited)
	}()
}

// killVMM kills v's VMM and its process group, unless it is already gone —
// checked by start time, so a pid since given to some other process is never
// signalled.
func killVMM(v *vm) {
	if v.pid <= 1 || v.exited == nil {
		return
	}
	select {
	case <-v.exited:
		return
	default:
	}
	if !sameProcess(v.pid, v.start) {
		return
	}
	_ = syscall.Kill(-v.pid, syscall.SIGKILL)
}

// candidate is a VM an earlier sandboxd left, checked up to its network.
type candidate struct {
	v  *vm
	ks keepState
}

// findKept checks what an earlier sandboxd left in the sandboxes directory and
// returns the VMs that can be taken back, removing the rest. The network is
// not touched here: the taps of the VMs returned are taken back when the
// network starts, with the rules that let them out.
//
// Every VM is checked at once, and each guest is given the boot timeout to
// answer: a guest slow to answer on a loaded host is not a broken one, and
// removing it would destroy a sandbox that was fine.
func (b *Backend) findKept() []candidate {
	des, _ := os.ReadDir(b.sandboxesDir())
	type result struct {
		id, dir string
		c       candidate
		why     string
	}
	results := make([]result, len(des))
	var wg sync.WaitGroup
	for i, de := range des {
		id := de.Name()
		dir := filepath.Join(b.sandboxesDir(), id)
		wg.Add(1)
		go func() {
			defer wg.Done()
			c, why := b.checkKept(id, dir)
			results[i] = result{id: id, dir: dir, c: c, why: why}
		}()
	}
	wg.Wait()
	var out []candidate
	for _, r := range results {
		if r.why != "" {
			b.removeLeftover(r.id, r.dir)
			b.cfg.Logf("removed a sandbox left by an earlier run: %s (%s)", r.id, r.why)
			continue
		}
		out = append(out, r.c)
	}
	return out
}

// checkKept decides one leftover: a candidate, or why it cannot be one.
func (b *Backend) checkKept(id, dir string) (candidate, string) {
	data, err := os.ReadFile(filepath.Join(dir, keepFile))
	if err != nil {
		return candidate{}, "no record of it"
	}
	var ks keepState
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&ks); err != nil || ks.Spec.ID != id {
		return candidate{}, "its record is unreadable"
	}
	if (b.cfg.Network != nil) != (ks.Tap >= 0) {
		return candidate{}, "networking was turned on or off since it started"
	}
	if (b.cfg.Jailer != nil) != (ks.UID >= 0) {
		return candidate{}, "the jailer was turned on or off since it started"
	}
	if b.cfg.Jailer != nil && ks.UID < b.cfg.Jailer.UIDBase {
		return candidate{}, "its jail uid is not one the jailer hands out"
	}
	v := &vm{id: id, dir: dir, spec: ks.Spec, pid: ks.PID, start: ks.Start, suspended: ks.Suspended, adopted: true}
	if b.cfg.Jailer != nil {
		v.root = b.cfg.Jailer.root(id)
	}
	v.exited = make(chan struct{})
	if ks.Suspended {
		for _, f := range []string{"suspend.state", "suspend.mem"} {
			if _, err := os.Stat(v.hostPath(f)); err != nil {
				return candidate{}, "its suspend snapshot is missing"
			}
		}
		close(v.exited) // nothing runs while it is suspended
	} else if !sameProcess(ks.PID, ks.Start) {
		return candidate{}, "its VMM is no longer running"
	}
	if ks.Tap >= 0 {
		if _, err := os.Stat(filepath.Join("/sys/class/net", fmt.Sprintf("sbx%d", ks.Tap))); err != nil {
			return candidate{}, "its network device is gone"
		}
	}
	v.client = guestClient(v.hostPath("v.sock"))
	if !ks.Suspended {
		ctx, cancel := context.WithTimeout(context.Background(), b.cfg.BootTimeout)
		err := v.client.WaitReady(ctx)
		cancel()
		if err != nil {
			return candidate{}, "its guest agent does not answer"
		}
		watch(v.pid, v.start, v.exited)
	}
	return candidate{v: v, ks: ks}, ""
}

// removeLeftover kills a leftover VM — only if its pid is still the process
// keep.json describes, or, without a keep.json, the pid vmm.pid holds, as
// before keeping existed — and removes its files.
func (b *Backend) removeLeftover(id, dir string) {
	if data, err := os.ReadFile(filepath.Join(dir, keepFile)); err == nil {
		var ks keepState
		if json.Unmarshal(data, &ks) == nil && sameProcess(ks.PID, ks.Start) {
			_ = syscall.Kill(-ks.PID, syscall.SIGKILL)
		}
	} else if pgid, err := readInt(filepath.Join(dir, "vmm.pid")); err == nil && pgid > 1 {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
	if b.cfg.Network != nil {
		if data, err := os.ReadFile(filepath.Join(dir, keepFile)); err == nil {
			var ks keepState
			if json.Unmarshal(data, &ks) == nil && ks.Tap >= 0 {
				_ = run("ip", "link", "del", fmt.Sprintf("sbx%d", ks.Tap))
			}
		}
	}
	if b.cfg.Jailer != nil {
		b.cfg.Jailer.cleanup(id)
		// As destroy does: the jail's links to its volumes were chowned to
		// the VM's uid, and the volumes go back to sandboxd.
		if data, err := os.ReadFile(filepath.Join(dir, keepFile)); err == nil {
			var ks keepState
			if json.Unmarshal(data, &ks) == nil {
				for _, m := range ks.Spec.Volumes {
					_ = os.Chown(b.volumePath(m.Name), os.Geteuid(), os.Getegid())
				}
			}
		}
	}
	_ = os.RemoveAll(dir)
}

// sweepVMMs ends every VMM still running from this state directory that no
// VM taken back accounts for. findKept removes what its records describe, but
// a record can be wrong: a VMM started for a resume just before sandboxd
// crashed is not the pid its keep.json names, and without this it would run
// on with nothing to stop it. A process is this backend's only when it is a
// firecracker whose working directory is in this state directory — the
// sandbox's own directory, or its jail — so nothing else on the host is ever
// touched. It returns how many it ended.
func (b *Backend) sweepVMMs(keep map[int]bool) int {
	var roots []string
	roots = append(roots, b.sandboxesDir())
	if b.cfg.Jailer != nil {
		roots = append(roots, filepath.Join(b.cfg.Jailer.ChrootBase, "firecracker"))
	}
	procs, _ := os.ReadDir("/proc")
	n := 0
	for _, p := range procs {
		pid, err := strconv.Atoi(p.Name())
		if err != nil || pid <= 1 || pid == os.Getpid() || keep[pid] {
			continue
		}
		comm, _ := os.ReadFile(fmt.Sprintf("/proc/%d/comm", pid))
		cwd, _ := os.Readlink(fmt.Sprintf("/proc/%d/cwd", pid))
		if !ownedVMM(string(comm), cwd, roots) {
			continue
		}
		start, err := procStart(pid)
		if err != nil {
			continue
		}
		// Only the group leader: the VMM leads its own session and group.
		if pgid, err := syscall.Getpgid(pid); err != nil || pgid != pid {
			continue
		}
		if sameProcess(pid, start) {
			_ = syscall.Kill(-pid, syscall.SIGKILL)
			b.cfg.Logf("ended a VMM no sandbox accounts for: pid %d in %s", pid, strings.TrimSuffix(cwd, " (deleted)"))
			n++
		}
	}
	return n
}

// ownedVMM reports whether a process is a VMM of this state directory: named
// firecracker, working in one of roots. A directory removed under it reads
// back with " (deleted)" appended, and still counts.
func ownedVMM(comm, cwd string, roots []string) bool {
	if strings.TrimSpace(comm) != "firecracker" {
		return false
	}
	cwd = strings.TrimSuffix(cwd, " (deleted)")
	for _, r := range roots {
		if strings.HasPrefix(cwd, filepath.Clean(r)+"/") {
			return true
		}
	}
	return false
}

// sweepJails removes jails no VM taken back owns: what a VM removed above
// left of its jail, or a jail whose sandbox directory is gone.
func (b *Backend) sweepJails(kept map[string]bool) {
	if b.cfg.Jailer == nil {
		return
	}
	base := filepath.Join(b.cfg.Jailer.ChrootBase, "firecracker")
	des, _ := os.ReadDir(base)
	for _, de := range des {
		if !kept[de.Name()] {
			_ = os.RemoveAll(filepath.Join(base, de.Name()))
		}
	}
}

// Kept is backend.Keeper.
func (b *Backend) Kept() map[string]backend.Kept {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := map[string]backend.Kept{}
	for id, v := range b.vms {
		if v.adopted {
			out[id] = backend.Kept{Suspended: v.suspended}
		}
	}
	return out
}

// Detach is backend.Keeper: it lets go of every VM and leaves them, their
// taps and the nftables table as they are, for the next sandboxd. The
// egress proxy and resolver end with this process; a guest's connections
// through them drop, and new ones wait for the next sandboxd.
func (b *Backend) Detach() {
	b.mu.Lock()
	vms := make([]*vm, 0, len(b.vms))
	for _, v := range b.vms {
		vms = append(vms, v)
	}
	b.vms = map[string]*vm{}
	b.mu.Unlock()
	for _, v := range vms {
		b.saveKeep(v)
	}
}

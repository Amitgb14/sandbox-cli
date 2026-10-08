//go:build vm && linux

package firecracker

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/image"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// TestVMKeep is a restart of sandboxd with Keep on, in one process: a backend
// detaches from its VMs and a second one on the same state directory takes
// them back. A running VM must come back as the same VM — the same boot, the
// same files — a suspended one suspended and resumable, and one whose VMM died
// meanwhile must be removed, not served.
//
// SANDBOX_TEST_NETWORK=1 and SANDBOX_TEST_JAILER as for the other VM tests
// (root): then the taken-back VM's tap and jail uid are checked too.
func TestVMKeep(t *testing.T) {
	kernel := os.Getenv("SANDBOX_TEST_KERNEL")
	fc := os.Getenv("SANDBOX_TEST_FIRECRACKER")
	if kernel == "" || fc == "" {
		t.Skip("set SANDBOX_TEST_KERNEL and SANDBOX_TEST_FIRECRACKER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	state, err := os.MkdirTemp("", "fckeep") // short: the vsock socket path is limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(state) })
	agent := filepath.Join(state, "sandbox-guestd")
	build := exec.Command("go", "build", "-trimpath", "-o", agent, "../../../cmd/sandbox-guestd")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the guest agent: %v\n%s", err, out)
	}
	ref := "alpine:3.20"
	if r := os.Getenv("SANDBOX_TEST_IMAGE"); r != "" {
		ref = r
	}
	puller := &image.Puller{Cache: filepath.Join(os.TempDir(), "sandbox-test-images")}
	network := os.Getenv("SANDBOX_TEST_NETWORK") == "1"
	jailer := os.Getenv("SANDBOX_TEST_JAILER")
	config := func() Config { // fresh Network and Jailer each time: they hold a process's state
		cfg := Config{
			Firecracker: fc, Kernel: kernel, Agent: agent, StateDir: state, Puller: puller,
			ImageDir: filepath.Join(os.TempDir(), "sandbox-test-images"),
			Logf:     func(f string, a ...any) { t.Logf(f, a...) },
			Keep:     true,
		}
		if network {
			cfg.Network = &Network{Logf: cfg.Logf}
		}
		if jailer != "" {
			cfg.Jailer = &Jailer{Path: jailer, ChrootBase: filepath.Join(state, "jail"), UIDBase: 900000}
		}
		return cfg
	}
	var current *Backend
	t.Cleanup(func() {
		if current == nil {
			return
		}
		for id := range current.Kept() {
			_ = current.Terminate(context.Background(), id)
		}
		current.mu.Lock()
		ids := make([]string, 0, len(current.vms))
		for id := range current.vms {
			ids = append(ids, id)
		}
		current.mu.Unlock()
		for _, id := range ids {
			_ = current.Terminate(context.Background(), id)
		}
		if current.cfg.Network != nil {
			current.cfg.Network.Close()
		}
	})

	be1, err := New(config())
	if err != nil {
		t.Fatal(err)
	}
	current = be1
	mk := func(netMode string) backend.Spec {
		s := backend.Spec{ID: spec.NewIDFor(""), Image: ref, CPUs: 1, MemoryMB: 256, DiskMB: 512,
			Network: api.NetworkPolicy{Mode: api.NetworkNone}}
		if network && netMode == api.NetworkAllowlist {
			s.Network = api.NetworkPolicy{Mode: api.NetworkAllowlist, Allow: []string{"example.com"}}
		}
		if err := be1.Create(ctx, s); err != nil {
			t.Fatalf("create: %v", err)
		}
		return s
	}
	running, suspended, dies := mk(api.NetworkAllowlist), mk(api.NetworkNone), mk(api.NetworkNone)
	if err := be1.WriteFile(ctx, running.ID, "/tmp/kept", []byte("still here")); err != nil {
		t.Fatal(err)
	}
	bootID, err := be1.ReadFile(ctx, running.ID, "/proc/sys/kernel/random/boot_id")
	if err != nil {
		t.Fatal(err)
	}
	if err := be1.Suspend(ctx, suspended.ID); err != nil {
		t.Fatal(err)
	}
	deadPID := be1.vms[dies.ID].pid
	be1.Detach() // sandboxd exits
	current = nil

	// While no sandboxd runs, one VMM dies.
	if err := syscall.Kill(-deadPID, syscall.SIGKILL); err != nil {
		t.Fatal(err)
	}
	time.Sleep(200 * time.Millisecond)

	be2, err := New(config())
	if err != nil {
		t.Fatal(err)
	}
	current = be2
	kept := be2.Kept()
	if k, ok := kept[running.ID]; !ok || k.Suspended {
		t.Fatalf("running VM not taken back as running: %+v", kept)
	}
	if k, ok := kept[suspended.ID]; !ok || !k.Suspended {
		t.Fatalf("suspended VM not taken back as suspended: %+v", kept)
	}
	if _, ok := kept[dies.ID]; ok {
		t.Fatal("a VM whose VMM died was taken back")
	}
	if _, err := os.Stat(filepath.Join(state, "sandboxes", dies.ID)); !os.IsNotExist(err) {
		t.Fatalf("the dead VM's files were left: %v", err)
	}

	// The same VM: same boot, same files.
	got, err := be2.ReadFile(ctx, running.ID, "/proc/sys/kernel/random/boot_id")
	if err != nil {
		t.Fatalf("the taken-back VM does not answer: %v", err)
	}
	if string(got) != string(bootID) {
		t.Fatalf("the VM was rebooted: boot id %q, was %q", got, bootID)
	}
	if data, err := be2.ReadFile(ctx, running.ID, "/tmp/kept"); err != nil || string(data) != "still here" {
		t.Fatalf("file after the restart: %q, %v", data, err)
	}
	if network {
		v := be2.vms[running.ID]
		if v.net == nil || v.net.policy.Load().Mode != api.NetworkAllowlist {
			t.Fatalf("the taken-back VM's network: %+v", v.net)
		}
		out, err := exec.Command("nft", "list", "set", "inet", "sandboxd", "egress_on").CombinedOutput()
		if err != nil || !strings.Contains(string(out), v.net.guest.String()) {
			t.Fatalf("the taken-back VM is not in the egress set: %v\n%s", err, out)
		}
	}
	if err := be2.Resume(ctx, suspended.ID); err != nil {
		t.Fatalf("resuming a VM taken back suspended: %v", err)
	}
	if _, err := be2.ReadFile(ctx, suspended.ID, "/proc/uptime"); err != nil {
		t.Fatalf("the resumed VM does not answer: %v", err)
	}

	// Ending a taken-back VM ends its VMM, which this process did not start.
	v := be2.vms[running.ID]
	pid, start := v.pid, v.start
	if err := be2.Terminate(ctx, running.ID); err != nil {
		t.Fatal(err)
	}
	if sameProcess(pid, start) {
		t.Fatal("the VMM of a terminated taken-back VM is still running")
	}
}

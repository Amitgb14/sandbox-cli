//go:build vm

// The guest agent and the image pipeline against a real microVM — rewrite M4's
// proof, and the starting point of the M5 backend.
//
//	SANDBOX_TEST_KERNEL=/path/to/vmlinux \
//	SANDBOX_TEST_FIRECRACKER=/path/to/firecracker \
//	go test -tags vm -run TestVM -v ./internal/backend/firecracker
//
// Needs /dev/kvm, mkfs.ext4, and network access to pull the image: alpine by
// default, or SANDBOX_TEST_IMAGE (with SANDBOX_TEST_PLAIN_HTTP=host:port for a
// local registry).
package firecracker

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/guestproto"
	"github.com/Amitgb14/sandbox-cli/internal/image"
	"github.com/Amitgb14/sandbox-cli/internal/vsock"
)

func TestVMGuestAgent(t *testing.T) {
	kernel := os.Getenv("SANDBOX_TEST_KERNEL")
	fc := os.Getenv("SANDBOX_TEST_FIRECRACKER")
	if kernel == "" || fc == "" {
		t.Skip("set SANDBOX_TEST_KERNEL and SANDBOX_TEST_FIRECRACKER")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	work, err := os.MkdirTemp("", "fcvm") // short: the vsock socket path is limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(work) })

	agent := filepath.Join(work, "sandbox-guestd")
	build := exec.Command("go", "build", "-trimpath", "-o", agent, "../../../cmd/sandbox-guestd")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the guest agent: %v\n%s", err, out)
	}

	cache := filepath.Join(os.TempDir(), "sandbox-test-images")
	t0 := time.Now()
	ref := "alpine:3.20"
	if r := os.Getenv("SANDBOX_TEST_IMAGE"); r != "" {
		ref = r
	}
	puller := &image.Puller{Cache: cache}
	if h := os.Getenv("SANDBOX_TEST_PLAIN_HTTP"); h != "" {
		puller.PlainHTTP = map[string]bool{h: true}
	}
	rootfs, err := image.BuildRootFS(ctx, puller, ref, agent, cache)
	if err != nil {
		t.Fatalf("building the root disk: %v", err)
	}
	t.Logf("root disk %s in %v (owned by host: %v)", rootfs.Path, time.Since(t0), rootfs.OwnedByHost)

	scratch := filepath.Join(work, "scratch.ext4")
	t0 = time.Now()
	if err := image.MakeScratch(ctx, scratch, 2048); err != nil {
		t.Fatal(err)
	}
	t.Logf("scratch disk in %v", time.Since(t0))

	uds := filepath.Join(work, "v.sock")
	cfg := fmt.Sprintf(`{
  "boot-source": {"kernel_image_path": %q, "boot_args": "console=ttyS0 reboot=k panic=1 pci=off ro quiet i8042.noaux i8042.nomux i8042.nopnp i8042.dumbkbd init=%s sbx.overlay=/dev/vdb sbx.vsock=5000 sbx.hostname=sbx-test"},
  "drives": [
    {"drive_id": "rootfs", "path_on_host": %q, "is_root_device": true, "is_read_only": true},
    {"drive_id": "scratch", "path_on_host": %q, "is_root_device": false, "is_read_only": false}
  ],
  "machine-config": {"vcpu_count": 2, "mem_size_mib": 512},
  "vsock": {"guest_cid": 3, "uds_path": %q}
}`, kernel, image.GuestAgentPath, rootfs.Path, scratch, uds)
	cfgPath := filepath.Join(work, "vm.json")
	os.WriteFile(cfgPath, []byte(cfg), 0o600)

	var console bytes.Buffer
	vm := exec.Command(fc, "--api-sock", filepath.Join(work, "api.sock"), "--config-file", cfgPath)
	vm.Stdout, vm.Stderr = &console, &console
	t0 = time.Now()
	if err := vm.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		vm.Process.Kill()
		vm.Wait()
		if t.Failed() {
			t.Logf("console:\n%s", console.String())
		}
	})

	c := &guestproto.Client{Dial: func(ctx context.Context) (io.ReadWriteCloser, error) {
		return vsock.DialFirecracker(ctx, uds, 5000)
	}}
	rctx, rcancel := context.WithTimeout(ctx, 30*time.Second)
	defer rcancel()
	if err := c.WaitReady(rctx); err != nil {
		t.Fatalf("guest agent never answered: %v", err)
	}
	t.Logf("VMM start -> guest agent answering: %v", time.Since(t0))

	run := func(argv ...string) (string, int) {
		t.Helper()
		var out, errb bytes.Buffer
		p, err := c.Exec(ctx, argv, nil, "/workspace", &out, &errb)
		if err != nil {
			t.Fatalf("exec %v: %v", argv, err)
		}
		p.Stdin().Close()
		code := p.Wait()
		return strings.TrimSpace(out.String() + errb.String()), code
	}

	if out, code := run("cat", "/etc/os-release"); code != 0 || !strings.Contains(out, "ID=") {
		t.Errorf("os-release: %q exit %d", out, code)
	}
	if extra := os.Getenv("SANDBOX_TEST_COMMAND"); extra != "" {
		out, code := run("sh", "-c", extra)
		t.Logf("%s -> exit %d:\n%s", extra, code, out)
		if code != 0 {
			t.Errorf("SANDBOX_TEST_COMMAND failed")
		}
	}
	// Processes run as the sandbox user, not as the agent's root.
	if out, _ := run("id", "-u"); out != "1001" {
		t.Errorf("processes run as uid %q, want 1001", out)
	}
	if out, _ := run("hostname"); out != "sbx-test" {
		t.Errorf("hostname %q", out)
	}
	// The workspace is the sandbox user's, on the writable layer.
	if out, code := run("sh", "-c", "echo made-in-guest > /workspace/f && cat /workspace/f"); code != 0 || out != "made-in-guest" {
		t.Errorf("workspace write: %q exit %d", out, code)
	}
	// Root is an overlay: writable for the agent, while the image disk is opened
	// read-only by the VMM and never changes.
	before, _ := os.ReadFile(rootfs.Path)
	if err := c.WriteFile(ctx, "/etc/sbx-overlay-test", []byte("ok")); err != nil {
		t.Errorf("writing to the overlay root: %v", err)
	}
	if got, err := c.ReadFile(ctx, "/etc/sbx-overlay-test"); err != nil || string(got) != "ok" {
		t.Errorf("reading it back: %q %v", got, err)
	}
	after, _ := os.ReadFile(rootfs.Path)
	if !bytes.Equal(before, after) {
		t.Error("the shared image disk changed")
	}
	// The sandbox user cannot write the image's own files.
	if _, code := run("sh", "-c", "echo x > /etc/passwd"); code == 0 {
		t.Error("the sandbox user wrote /etc/passwd")
	}
	// Loopback is up.
	if out, code := run("sh", "-c", "ip link show lo | grep -c UP"); code != 0 || out != "1" {
		t.Errorf("loopback: %q exit %d", out, code)
	}
	// A large file round-trips over vsock.
	big := bytes.Repeat([]byte("0123456789abcdef"), 1<<20) // 16 MiB
	t0 = time.Now()
	if err := c.WriteFile(ctx, "/workspace/big", big); err != nil {
		t.Fatal(err)
	}
	got, err := c.ReadFile(ctx, "/workspace/big")
	if err != nil || !bytes.Equal(got, big) {
		t.Fatalf("16 MiB round trip: %v", err)
	}
	t.Logf("16 MiB write+read over vsock: %v", time.Since(t0))
}

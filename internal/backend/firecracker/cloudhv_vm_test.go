//go:build vm && linux

// A GPU sandbox's VM against real Cloud Hypervisor (docs/roadmap/task-9-gpu-sandboxes.md):
//
//	SANDBOX_TEST_CLOUDHV=/path/to/cloud-hypervisor \
//	SANDBOX_TEST_GPU_KIT=/path/to/kit \
//	go test -tags vm -run TestVMCloudHypervisor -v ./internal/backend/firecracker
//
// The kit is scripts/gpu-kit.sh's output. Without a GPU the VM boots the kit's
// kernel with the kit in its root disk and the guest agent answers over
// vsock; SANDBOX_TEST_GPU=0000:01:00.0, a GPU bound to vfio-pci (which needs
// root), also loads the driver and runs nvidia-smi: build the test with go
// test -c and run it as root with SANDBOX_TEST_AGENT, a built sandbox-guestd. The image is
// SANDBOX_TEST_IMAGE, debian:bookworm-slim by default: the driver needs glibc.
package firecracker

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/guestproto"
	"github.com/Amitgb14/sandbox-cli/internal/image"
)

func TestVMCloudHypervisor(t *testing.T) {
	ch, kit := os.Getenv("SANDBOX_TEST_CLOUDHV"), os.Getenv("SANDBOX_TEST_GPU_KIT")
	if ch == "" || kit == "" {
		t.Skip("set SANDBOX_TEST_CLOUDHV and SANDBOX_TEST_GPU_KIT")
	}
	gpu := os.Getenv("SANDBOX_TEST_GPU")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	work, err := os.MkdirTemp("", "chvm") // short: the vsock socket path is limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(work) })
	// SANDBOX_TEST_AGENT, a sandbox-guestd already built: what lets the test
	// binary run as root (go test -c), which a GPU needs, without go.
	agent := os.Getenv("SANDBOX_TEST_AGENT")
	if agent == "" {
		agent = filepath.Join(work, "sandbox-guestd")
		build := exec.Command("go", "build", "-trimpath", "-o", agent, "../../../cmd/sandbox-guestd")
		build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("building the guest agent: %v\n%s", err, out)
		}
	}

	sum, err := image.TreeSum(filepath.Join(kit, "rootfs"))
	if err != nil {
		t.Fatal(err)
	}
	ref := "debian:bookworm-slim"
	if r := os.Getenv("SANDBOX_TEST_IMAGE"); r != "" {
		ref = r
	}
	cache := filepath.Join(os.TempDir(), "sandbox-test-images")
	t0 := time.Now()
	rootfs, err := image.BuildRootFSWithKit(ctx, &image.Puller{Cache: cache}, ref, agent, cache,
		&image.Kit{Dir: filepath.Join(kit, "rootfs"), Sum: sum})
	if err != nil {
		t.Fatalf("building the root disk: %v", err)
	}
	t.Logf("root disk with the kit in %v", time.Since(t0))

	boot := func(t *testing.T, name string, gpuArg bool, devices []string) (*guestproto.Client, *exec.Cmd, *bytes.Buffer, chan struct{}) {
		dir := filepath.Join(work, name)
		os.MkdirAll(dir, 0o700)
		scratch := filepath.Join(dir, "scratch.ext4")
		if err := image.MakeScratch(ctx, scratch, 1024); err != nil {
			t.Fatal(err)
		}
		fc := []string{"console=ttyS0", "reboot=k", "panic=1", "pci=off", "ro", FastBootArgs,
			"init=" + image.GuestAgentPath, "sbx.overlay=/dev/vdb", fmt.Sprintf("sbx.vsock=%d", guestPort), "sbx.hostname=sbx-test"}
		bootArgs := cloudHypervisorBootArgs(fc, gpuArg)
		p := vmPaths{Kernel: filepath.Join(kit, "vmlinux"), RootFS: rootfs.Path, Scratch: scratch,
			VsockUDS: filepath.Join(dir, "v.sock"), APISock: filepath.Join(dir, "api.sock")}
		args, err := CloudHypervisorArgs(backend.Spec{ID: "sbx_test", CPUs: 2, MemoryMB: 2048}, p, strings.Join(bootArgs, " "), nil, devices)
		if err != nil {
			t.Fatal(err)
		}
		var console bytes.Buffer
		vm := exec.Command(ch, args...)
		vm.Stdout, vm.Stderr = &console, &console
		if err := vm.Start(); err != nil {
			t.Fatal(err)
		}
		exited := make(chan struct{})
		go func() { vm.Wait(); close(exited) }()
		t.Cleanup(func() {
			vm.Process.Kill()
			<-exited
			if t.Failed() {
				t.Logf("console:\n%s", console.String())
			}
		})
		return guestClient(p.VsockUDS), vm, &console, exited
	}
	exec1 := func(t *testing.T, c *guestproto.Client, argv ...string) (string, int) {
		t.Helper()
		var out bytes.Buffer
		p, err := c.Exec(ctx, argv, nil, "/sandbox/home", &out, &out)
		if err != nil {
			t.Fatalf("exec %v: %v", argv, err)
		}
		p.Stdin().Close()
		code := p.Wait()
		return out.String(), code
	}
	run := func(t *testing.T, c *guestproto.Client, argv ...string) string {
		t.Helper()
		out, code := exec1(t, c, argv...)
		if code != 0 {
			t.Fatalf("%v: exit %d\n%s", argv, code, out)
		}
		return out
	}

	t.Run("boots the kit", func(t *testing.T) {
		c, _, _, exited := boot(t, "plain", false, nil)
		ready := make(chan error, 1)
		t0 := time.Now()
		go func() {
			rctx, rcancel := context.WithTimeout(ctx, time.Minute)
			defer rcancel()
			ready <- c.WaitReady(rctx)
		}()
		select {
		case err := <-ready:
			if err != nil {
				t.Fatalf("the guest agent did not answer: %v", err)
			}
		case <-exited:
			t.Fatal("cloud-hypervisor exited before the guest came up")
		}
		t.Logf("guest agent ready in %v", time.Since(t0))
		if got := strings.TrimSpace(run(t, c, "uname", "-r")); !strings.HasSuffix(got, "-sandbox-gpu") {
			t.Errorf("kernel %q, want the kit's", got)
		}
		run(t, c, "test", "-x", "/usr/bin/nvidia-smi")
		if got := run(t, c, "cat", "/usr/lib/sandbox-gpu/modules.list"); got != "nvidia.ko\nnvidia-uvm.ko\n" {
			t.Errorf("modules.list: %q", got)
		}
		// The kit is in the read-only root disk: the sandbox user cannot
		// replace what sandbox-guestd loads as root.
		if _, code := exec1(t, c, "sh", "-c", "echo x > /usr/lib/sandbox-gpu/nvidia.ko"); code == 0 {
			t.Error("the sandbox user rewrote the kit's module")
		}
	})

	t.Run("a driver that cannot load fails the boot", func(t *testing.T) {
		// sbx.gpu=1 with no GPU: the driver loads and finds nothing.
		_, _, console, exited := boot(t, "nogpu", true, nil)
		select {
		case <-exited:
			t.Logf("cloud-hypervisor exited")
		case <-time.After(90 * time.Second):
			t.Logf("still running after 90s")
		}
		if !strings.Contains(console.String(), "sandbox-guestd: FATAL: GPU:") {
			t.Errorf("no GPU failure on the console")
		}
		for _, l := range strings.Split(console.String(), "\n") {
			if strings.Contains(l, "FATAL") || strings.Contains(l, "NVRM") {
				t.Logf("console: %s", strings.TrimSpace(l))
			}
		}
	})

	if gpu == "" {
		return
	}
	t.Run("nvidia-smi", func(t *testing.T) {
		c, _, _, _ := boot(t, "gpu", true, []string{gpu})
		rctx, rcancel := context.WithTimeout(ctx, 2*time.Minute)
		defer rcancel()
		if err := c.WaitReady(rctx); err != nil {
			t.Fatalf("the guest agent did not answer: %v", err)
		}
		t.Logf("%s", run(t, c, "nvidia-smi", "-L"))
		t.Logf("%s", run(t, c, "nvidia-smi"))
	})
}

// A GPU sandbox through the backend, as sandboxd makes one: Create, a process,
// files, the refusals, Terminate. Without SANDBOX_TEST_GPU every sandbox runs
// on Cloud Hypervisor with the kit and no device (cloudHVAll); as root it runs
// under the jailer, which takes the setpriv path, and with SANDBOX_TEST_GPU
// the sandbox is given the GPU and runs nvidia-smi.
func TestVMCloudHypervisorBackend(t *testing.T) {
	ch, kit := os.Getenv("SANDBOX_TEST_CLOUDHV"), os.Getenv("SANDBOX_TEST_GPU_KIT")
	if ch == "" || kit == "" {
		t.Skip("set SANDBOX_TEST_CLOUDHV and SANDBOX_TEST_GPU_KIT")
	}
	gpu := os.Getenv("SANDBOX_TEST_GPU")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	state, err := os.MkdirTemp("", "chb") // short: the vsock socket path is limited
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(state) })
	agent := os.Getenv("SANDBOX_TEST_AGENT")
	if agent == "" {
		agent = filepath.Join(state, "sandbox-guestd")
		build := exec.Command("go", "build", "-trimpath", "-o", agent, "../../../cmd/sandbox-guestd")
		build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("building the guest agent: %v\n%s", err, out)
		}
	}
	ref := "debian:bookworm-slim"
	if r := os.Getenv("SANDBOX_TEST_IMAGE"); r != "" {
		ref = r
	}
	cfg := Config{
		// Firecracker is not run; the backend only needs it to exist.
		Firecracker: ch, Kernel: filepath.Join(kit, "vmlinux"), Agent: agent, StateDir: state,
		Puller:          &image.Puller{Cache: filepath.Join(os.TempDir(), "sandbox-test-images")},
		CloudHypervisor: ch, GPUKit: kit, BootTimeout: time.Minute, Logf: t.Logf,
	}
	if os.Geteuid() == 0 {
		cfg.Jailer = &Jailer{ChrootBase: filepath.Join(state, "jail"), UIDBase: 990000}
	}
	b, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(b.Close)
	s := backend.Spec{ID: "sbx_0123456789abcdef", Image: ref, CPUs: 2, MemoryMB: 2048, DiskMB: 1024}
	if gpu != "" {
		s.GPUs = []string{gpu}
	} else {
		b.cloudHVAll = true
	}

	t0 := time.Now()
	if err := b.Create(ctx, s); err != nil {
		t.Fatalf("create: %v\nconsole:\n%s", err, tail(filepath.Join(b.sandboxesDir(), s.ID, "console.log"), 4096))
	}
	t.Logf("created in %v", time.Since(t0))
	run := func(argv ...string) string {
		t.Helper()
		var out bytes.Buffer
		p, err := b.Start(ctx, s.ID, backend.ProcSpec{Argv: argv, Cwd: "/sandbox/home"}, &out, &out)
		if err != nil {
			t.Fatalf("%v: %v", argv, err)
		}
		p.Stdin().Close()
		if code := p.Wait(); code != 0 {
			t.Fatalf("%v: exit %d\n%s", argv, code, out.String())
		}
		return out.String()
	}
	if got := strings.TrimSpace(run("uname", "-r")); !strings.HasSuffix(got, "-sandbox-gpu") {
		t.Errorf("kernel %q, want the kit's", got)
	}
	if err := b.WriteFile(ctx, s.ID, "/sandbox/home/x", []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if got, err := b.ReadFile(ctx, s.ID, "/sandbox/home/x"); err != nil || string(got) != "hello" {
		t.Errorf("read back %q, %v", got, err)
	}
	if gpu != "" {
		t.Logf("%s", run("nvidia-smi", "-L"))
		t.Logf("%s", run("ls", "-l", "/dev/nvidia0", "/dev/nvidiactl", "/dev/nvidia-uvm"))
	}
	if cfg.Jailer != nil {
		// The VMM is the jail's uid, not root.
		v, _ := b.get(s.ID)
		status, _ := os.ReadFile(fmt.Sprintf("/proc/%d/status", v.pid))
		if !strings.Contains(string(status), fmt.Sprintf("\nUid:\t%d\t", cfg.Jailer.uid(s.ID))) {
			t.Errorf("the VMM is not the jail's uid:\n%s", status)
		}
	}
	if err := b.Suspend(ctx, s.ID); !errors.Is(err, backend.ErrUnsupported) {
		t.Errorf("suspend: %v", err)
	}
	if _, err := b.Snapshot(ctx, s.ID, "snap_test"); !errors.Is(err, backend.ErrUnsupported) {
		t.Errorf("snapshot: %v", err)
	}
	v, _ := b.get(s.ID)
	if err := b.Terminate(ctx, s.ID); err != nil {
		t.Fatal(err)
	}
	select {
	case <-v.exited:
	case <-time.After(10 * time.Second):
		t.Error("the VMM is still running after terminate")
	}
	if _, err := os.Stat(v.dir); !os.IsNotExist(err) {
		t.Errorf("the sandbox's directory is left: %v", err)
	}
	if gpu != "" && cfg.Jailer != nil {
		node, _ := vfioGroup(gpu)
		if fi, err := os.Stat(node); err != nil || fi.Sys().(*syscall.Stat_t).Uid != 0 {
			t.Errorf("%s not given back to root", node)
		}
	}
}

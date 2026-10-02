//go:build vm && linux

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

	"net/http/httptest"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/api/conformance"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/guestproto"
	"github.com/Amitgb14/sandbox-cli/internal/image"
	"github.com/Amitgb14/sandbox-cli/internal/server"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
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

// TestVMConformance runs the API conformance suite against sandboxd serving
// this backend: every sandbox in it is a real microVM. Run unprivileged it has
// no network and no jailer, so the suite sees an endpoint that offers mode none.
func TestVMConformance(t *testing.T) {
	kernel := os.Getenv("SANDBOX_TEST_KERNEL")
	fc := os.Getenv("SANDBOX_TEST_FIRECRACKER")
	if kernel == "" || fc == "" {
		t.Skip("set SANDBOX_TEST_KERNEL and SANDBOX_TEST_FIRECRACKER")
	}
	state, err := os.MkdirTemp("", "fcst") // short: socket paths are limited
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
	puller := &image.Puller{Cache: filepath.Join(os.TempDir(), "sandbox-test-images")}
	if h := os.Getenv("SANDBOX_TEST_PLAIN_HTTP"); h != "" {
		puller.PlainHTTP = map[string]bool{h: true}
	}
	be := newTestBackend(t, fc, kernel, agent, state, puller)
	pol := spec.DefaultPolicy()
	pol.DefaultImage = "alpine:3.20"
	if r := os.Getenv("SANDBOX_TEST_IMAGE"); r != "" {
		pol.DefaultImage = r
	}
	pol, notes := pol.FitTo(be.Capabilities())
	for _, n := range notes {
		t.Log("policy: " + n)
	}
	srv := &server.Server{Backend: be, Policy: pol, Token: "vm-conformance"}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	conformance.Run(t, api.NewClientWithHTTP(ts.URL, "vm-conformance", ts.Client()))
}

// newTestBackend is the backend as the environment asks for it:
// SANDBOX_TEST_NETWORK=1 adds host networking (root, or a user+network
// namespace), SANDBOX_TEST_JAILER=/path/to/jailer adds the jailer (real root).
func newTestBackend(t *testing.T, fc, kernel, agent, state string, puller *image.Puller) *Backend {
	t.Helper()
	cfg := Config{
		Firecracker: fc, Kernel: kernel, Agent: agent, StateDir: state, Puller: puller,
		Logf: func(f string, a ...any) { t.Logf(f, a...) },
	}
	if os.Getenv("SANDBOX_TEST_NETWORK") == "1" {
		cfg.Network = &Network{Logf: cfg.Logf}
	}
	if j := os.Getenv("SANDBOX_TEST_JAILER"); j != "" {
		cfg.Jailer = &Jailer{Path: j, ChrootBase: filepath.Join(state, "jail"), UIDBase: 900000}
	}
	be, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		be.mu.Lock()
		ids := make([]string, 0, len(be.vms))
		for id := range be.vms {
			ids = append(ids, id)
		}
		be.mu.Unlock()
		for _, id := range ids {
			_ = be.Terminate(context.Background(), id)
		}
		if cfg.Network != nil {
			cfg.Network.Close()
		}
	})
	return be
}

// TestVMEgress checks egress enforcement from inside a real guest: names on the
// allowlist resolve and connect, everything else is refused — by DNS, by the
// proxy, or by the drop — and a live update to none cuts it off.
//
// Needs SANDBOX_TEST_NETWORK=1 and root (or `unshare -rn`, where there is no
// route out, so the allowed probes are only checked when SANDBOX_TEST_INTERNET=1),
// and an image with curl and getent (the base image).
func TestVMEgress(t *testing.T) {
	kernel := os.Getenv("SANDBOX_TEST_KERNEL")
	fc := os.Getenv("SANDBOX_TEST_FIRECRACKER")
	if kernel == "" || fc == "" || os.Getenv("SANDBOX_TEST_NETWORK") != "1" || os.Getenv("SANDBOX_TEST_IMAGE") == "" {
		t.Skip("set SANDBOX_TEST_KERNEL, SANDBOX_TEST_FIRECRACKER, SANDBOX_TEST_NETWORK=1 and SANDBOX_TEST_IMAGE")
	}
	internet := os.Getenv("SANDBOX_TEST_INTERNET") == "1"
	state, _ := os.MkdirTemp("", "fcnet")
	t.Cleanup(func() { os.RemoveAll(state) })
	agent := filepath.Join(state, "sandbox-guestd")
	build := exec.Command("go", "build", "-trimpath", "-o", agent, "../../../cmd/sandbox-guestd")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	puller := &image.Puller{Cache: filepath.Join(os.TempDir(), "sandbox-test-images")}
	if h := os.Getenv("SANDBOX_TEST_PLAIN_HTTP"); h != "" {
		puller.PlainHTTP = map[string]bool{h: true}
	}
	be := newTestBackend(t, fc, kernel, agent, state, puller)
	ctx := context.Background()
	id := spec.NewID()
	err := be.Create(ctx, backend.Spec{
		ID: id, Image: os.Getenv("SANDBOX_TEST_IMAGE"), CPUs: 1, MemoryMB: 512, DiskMB: 1024,
		Network: api.NetworkPolicy{Mode: api.NetworkAllowlist, Allow: []string{"github.com", "example.com"}, Deny: []string{"gist.github.com"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	sh := func(script string) (string, int) {
		var out bytes.Buffer
		p, err := be.Start(ctx, id, backend.ProcSpec{Argv: []string{"sh", "-c", script}}, &out, &out)
		if err != nil {
			t.Fatal(err)
		}
		p.Stdin().Close()
		code := p.Wait()
		return strings.TrimSpace(out.String()), code
	}
	probe := func(name, script string, wantOK bool) {
		t.Helper()
		out, code := sh(script)
		if (code == 0) != wantOK {
			t.Errorf("%s: exit %d, want success=%v\n%s", name, code, wantOK, out)
		} else {
			t.Logf("%s: exit %d as expected", name, code)
		}
	}
	probe("an allowed name resolves", "getent hosts github.com", true)
	probe("a denied name does not resolve", "getent hosts gist.github.com", false)
	probe("an unlisted name does not resolve", "getent hosts attacker.example", false)
	probe("TLS with no name is refused", "curl -sk -m 5 -o /dev/null https://1.1.1.1/", false)
	probe("a denied name is refused even when dialled by address", "curl -s -m 5 -o /dev/null --resolve gist.github.com:443:1.1.1.1 https://gist.github.com/", false)
	probe("other ports go nowhere", "timeout 5 bash -c 'echo > /dev/tcp/1.1.1.1/22'", false)
	if internet {
		probe("an allowed name connects", "curl -sS -m 10 -o /dev/null https://github.com/", true)
		probe("plain HTTP to an allowed name connects", "curl -sS -m 10 -o /dev/null http://example.com/", true)
	}
	// A live update to none: the same name that resolved now does not, and
	// nothing connects.
	if err := be.UpdateNetwork(ctx, id, api.NetworkPolicy{Mode: api.NetworkNone}); err != nil {
		t.Fatal(err)
	}
	probe("after the update to none, nothing resolves", "getent hosts github.com", false)
	probe("after the update to none, nothing connects", "curl -s -m 5 -o /dev/null --resolve github.com:443:1.1.1.1 https://github.com/", false)
}

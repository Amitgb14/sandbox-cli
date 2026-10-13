//go:build vm && linux

package firecracker

import (
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/image"
	"github.com/Amitgb14/sandbox-cli/internal/server"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// A resize restarts a real VM at its new size: the guest sees the vCPUs,
// the memory and the larger disk, and the file written before is there.
//
//	SANDBOX_TEST_KERNEL=… SANDBOX_TEST_FIRECRACKER=… go test -tags vm -run TestVMResize -v ./internal/backend/firecracker
func TestVMResize(t *testing.T) {
	kernel := os.Getenv("SANDBOX_TEST_KERNEL")
	fc := os.Getenv("SANDBOX_TEST_FIRECRACKER")
	if kernel == "" || fc == "" {
		t.Skip("set SANDBOX_TEST_KERNEL and SANDBOX_TEST_FIRECRACKER")
	}
	state, err := os.MkdirTemp("", "fcrz")
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
	be := newTestBackend(t, fc, kernel, agent, state, &image.Puller{Cache: filepath.Join(os.TempDir(), "sandbox-test-images")})
	pol := spec.DefaultPolicy()
	pol.DefaultImage = "alpine:3.20"
	if r := os.Getenv("SANDBOX_TEST_IMAGE"); r != "" {
		pol.DefaultImage = r
	}
	pol, _ = pol.FitTo(be.Capabilities())
	srv := &server.Server{Backend: be, Policy: pol, Token: "vm-resize"}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	c := api.NewClientWithHTTP(ts.URL, "vm-resize", ts.Client())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{CPUs: 1, MemoryMB: 256, DiskMB: 512})
	if err != nil {
		t.Fatal(err)
	}
	run := func(script string) string {
		t.Helper()
		res, err := c.Run(ctx, sb.ID, api.RunRequest{Argv: []string{"sh", "-c", script}})
		if err != nil || res.ExitCode != 0 {
			t.Fatalf("%s: %+v, %v", script, res, err)
		}
		return strings.TrimSpace(string(res.Stdout))
	}
	run("echo kept > \"$HOME/kept\" && sync")

	t0 := time.Now()
	got, err := c.Resize(ctx, sb.ID, api.ResizeSandboxRequest{CPUs: 2, MemoryMB: 512, DiskMB: 1024})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("resized in %v", time.Since(t0))
	if got.CPUs != 2 || got.MemoryMB != 512 || got.DiskMB != 1024 {
		t.Fatalf("resized to %+v", got)
	}
	if n := run("grep -c ^processor /proc/cpuinfo"); n != "2" {
		t.Errorf("the guest sees %s CPUs; want 2", n)
	}
	kb, _ := strconv.Atoi(run("awk '/MemTotal/ {print $2}' /proc/meminfo"))
	if kb < 400<<10 {
		t.Errorf("the guest has %d kB of memory; want about 512 MiB", kb)
	}
	if v := run("cat \"$HOME/kept\""); v != "kept" {
		t.Errorf("the file written before: %q", v)
	}
	blocks, _ := strconv.Atoi(run("df -k \"$HOME\" | awk 'NR==2 {print $2}'"))
	if blocks < 900<<10 {
		t.Errorf("the disk is %d kB; want about 1 GiB", blocks)
	}
	if !got.Restarted {
		t.Error("new CPUs and memory did not restart the sandbox")
	}

	// The disk alone grows live: a process running before is running after,
	// and the guest's filesystem fills the larger device.
	p, err := c.StartProcess(ctx, sb.ID, api.RunRequest{Argv: []string{"sleep", "60"}})
	if err != nil {
		t.Fatal(err)
	}
	t0 = time.Now()
	got, err = c.Resize(ctx, sb.ID, api.ResizeSandboxRequest{DiskMB: 2048})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("grew the disk live in %v", time.Since(t0))
	if got.Restarted || got.DiskMB != 2048 {
		t.Fatalf("a live disk grow: %d MB, restarted %v", got.DiskMB, got.Restarted)
	}
	if pr, err := c.Process(ctx, sb.ID, p.PID); err != nil || pr.State != api.ProcessRunning {
		t.Fatalf("the process after a live grow: %+v, %v; want running", pr, err)
	}
	blocks, _ = strconv.Atoi(run("df -k \"$HOME\" | awk 'NR==2 {print $2}'"))
	if blocks < 1900<<10 {
		t.Errorf("after a live grow the disk is %d kB; want about 2 GiB", blocks)
	}
	if v := run("cat \"$HOME/kept\""); v != "kept" {
		t.Errorf("the file written before, after a live grow: %q", v)
	}
}

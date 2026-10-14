//go:build vm && linux

package firecracker

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/image"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// TestVMSnapshotWithNetwork snapshots a networked sandbox and starts two
// sandboxes from it. Each fork is the original's memory — its file and its
// running process are there — in a namespace of its own, at a host address
// of its own, under the policy it asked for: one may reach example.com and
// not github.com, the other nothing. The original keeps its own.
//
// Needs root, SANDBOX_TEST_NETWORK=1 and SANDBOX_TEST_IMAGE (an image with
// getent: the base image); SANDBOX_TEST_JAILER too, to run it jailed.
func TestVMSnapshotWithNetwork(t *testing.T) {
	kernel := os.Getenv("SANDBOX_TEST_KERNEL")
	fc := os.Getenv("SANDBOX_TEST_FIRECRACKER")
	if kernel == "" || fc == "" || os.Getenv("SANDBOX_TEST_NETWORK") != "1" || os.Getenv("SANDBOX_TEST_IMAGE") == "" {
		t.Skip("set SANDBOX_TEST_KERNEL, SANDBOX_TEST_FIRECRACKER, SANDBOX_TEST_NETWORK=1 and SANDBOX_TEST_IMAGE")
	}
	state, err := os.MkdirTemp("", "fcsnapnet")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(state) })
	agent := filepath.Join(state, "sandbox-guestd")
	build := exec.Command("go", "build", "-trimpath", "-o", agent, "../../../cmd/sandbox-guestd")
	build.Env = append(os.Environ(), "CGO_ENABLED=0", "GOOS=linux", "GOARCH="+runtime.GOARCH)
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("%v\n%s", err, out)
	}
	be := newTestBackend(t, fc, kernel, agent, state, &image.Puller{Cache: filepath.Join(os.TempDir(), "sandbox-test-images")})
	ctx := context.Background()
	img := os.Getenv("SANDBOX_TEST_IMAGE")

	sh := func(id, script string) (string, int) {
		t.Helper()
		var out bytes.Buffer
		p, err := be.Start(ctx, id, backend.ProcSpec{Argv: []string{"sh", "-c", script}}, &out, &out)
		if err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		p.Stdin().Close()
		code := p.Wait()
		return strings.TrimSpace(out.String()), code
	}
	resolves := func(id, name string, want bool) {
		t.Helper()
		out, code := sh(id, "getent hosts "+name)
		if (code == 0) != want {
			t.Errorf("%s resolving %s: exit %d, want success=%v\n%s", id, name, code, want, out)
		}
	}

	orig := spec.NewID()
	if err := be.Create(ctx, backend.Spec{ID: orig, Image: img, CPUs: 1, MemoryMB: 512, DiskMB: 1024,
		Network: api.NetworkPolicy{Mode: api.NetworkAllowlist, Allow: []string{"github.com"}}}); err != nil {
		t.Fatal(err)
	}
	resolves(orig, "github.com", true)
	if out, code := sh(orig, "echo kept > /tmp/kept && (sleep 600 >/dev/null 2>&1 &) && sync"); code != 0 {
		t.Fatalf("setting up: %s", out)
	}

	snap := "snp_00000000000000aa"
	info, err := be.Snapshot(ctx, orig, snap)
	if err != nil {
		t.Fatalf("snapshotting a networked sandbox: %v", err)
	}
	t.Logf("snapshot: %d bytes", info.Bytes)

	forkA, forkB := spec.NewID(), spec.NewID()
	for id, pol := range map[string]api.NetworkPolicy{
		forkA: {Mode: api.NetworkAllowlist, Allow: []string{"example.com"}},
		forkB: {Mode: api.NetworkNone},
	} {
		if err := be.Create(ctx, backend.Spec{ID: id, Image: img, CPUs: 1, MemoryMB: 512, DiskMB: 1024, FromSnapshot: snap, Network: pol}); err != nil {
			t.Fatalf("a fork with network %s: %v", pol.Mode, err)
		}
	}
	for _, id := range []string{forkA, forkB} {
		if out, code := sh(id, "cat /tmp/kept && pgrep -f 'sleep 600'"); code != 0 || !strings.HasPrefix(out, "kept") {
			t.Errorf("%s: the original's file and process: %q, exit %d", id, out, code)
		}
	}
	// Each fork under its own policy; the original under its own still.
	resolves(forkA, "example.com", true)
	resolves(forkA, "github.com", false)
	resolves(forkB, "example.com", false)
	resolves(forkB, "github.com", false)
	resolves(orig, "github.com", true)
	resolves(orig, "example.com", false)

	// Three host slots, three namespaces: no two share a network identity.
	seen := map[string]bool{}
	for _, id := range []string{orig, forkA, forkB} {
		v, err := be.get(id)
		if err != nil || v.net == nil || v.net.ns == "" {
			t.Fatalf("%s: %v, %+v", id, err, v)
		}
		if seen[v.net.guest.String()] {
			t.Errorf("%s shares the host address %s", id, v.net.guest)
		}
		seen[v.net.guest.String()] = true
		if _, err := os.Stat(v.net.nsPath()); err != nil {
			t.Errorf("%s's namespace: %v", id, err)
		}
	}

	// Terminating one takes its namespace with it.
	v, _ := be.get(forkB)
	ns := v.net.nsPath()
	if err := be.Terminate(ctx, forkB); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(ns); !os.IsNotExist(err) {
		t.Errorf("%s is still there after its sandbox ended: %v", ns, err)
	}
}

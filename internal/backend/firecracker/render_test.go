//go:build linux

package firecracker

import (
	"encoding/json"
	"flag"
	"net"
	"os"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
)

// -update rewrites the golden files. Do it deliberately, and read the diff: a
// change here is a change to what every sandbox is.
var update = flag.Bool("update", false, "rewrite golden files")

func golden(t *testing.T, name string, got []byte) {
	t.Helper()
	path := "testdata/" + name
	if *update {
		if err := os.WriteFile(path, got, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update once, then review the file)", err)
	}
	if string(got) != string(want) {
		t.Errorf("%s changed:\n--- want\n%s\n--- got\n%s", path, want, got)
	}
}

// What a sandbox is, as the VMM is told: the root disk read-only, the scratch
// disk the only writable one, the network interface only when asked for, and
// vsock as the way in.
func TestBuildConfigGolden(t *testing.T) {
	s := backend.Spec{ID: "sbx_0123456789abcdef", Image: "img", CPUs: 1.5, MemoryMB: 2048, DiskMB: 10240,
		Network: api.NetworkPolicy{Mode: api.NetworkAllowlist, Allow: []string{"github.com"}}}
	p := vmPaths{Kernel: "/k/vmlinux", RootFS: "/i/rootfs.ext4", Scratch: "/s/scratch.ext4", VsockUDS: "/s/v.sock", APISock: "/s/api.sock"}
	tp := &tap{name: "sbx7", host: net.IPv4(172, 16, 0, 29).To4(), guest: net.IPv4(172, 16, 0, 30).To4(), mac: "06:00:ac:10:00:1e"}
	with, _ := json.MarshalIndent(BuildConfig(s, p, "console=ttyS0 ro", tp), "", "  ")
	without, _ := json.MarshalIndent(BuildConfig(s, p, "console=ttyS0 ro", nil), "", "  ")
	golden(t, "vmconfig-network.json", append(with, '\n'))
	golden(t, "vmconfig-no-network.json", append(without, '\n'))
}

// The host firewall, as text, because that is how it is reviewed.
func TestRulesetGolden(t *testing.T) {
	golden(t, "ruleset.nft", []byte(Ruleset("sandboxd", 3128, 5353)))
}

// Volumes are drives after the root and scratch disks, writable unless mounted
// read-only, never the root device — and the guest is told which drive goes
// where in the same order.
func TestBuildConfigWithVolumesGolden(t *testing.T) {
	s := backend.Spec{ID: "sbx_0123456789abcdef", Image: "img", CPUs: 1, MemoryMB: 1024, DiskMB: 4096,
		Network: api.NetworkPolicy{Mode: api.NetworkNone},
		Volumes: []api.VolumeMount{{Name: "cache", Path: "/sandbox/home/.cache"}, {Name: "models", Path: "/models", ReadOnly: true}}}
	p := vmPaths{Kernel: "/k/vmlinux", RootFS: "/i/rootfs.ext4", Scratch: "scratch.ext4", VsockUDS: "v.sock",
		Volumes: []string{"/state/volumes/cache.ext4", "/state/volumes/models.ext4"}}
	got, _ := json.MarshalIndent(BuildConfig(s, p, "console=ttyS0 ro "+volumeBootArg(s), nil), "", "  ")
	golden(t, "vmconfig-volumes.json", append(got, '\n'))
	if arg := volumeBootArg(s); arg != "sbx.volumes=vdc:/sandbox/home/.cache:rw,vdd:/models:ro" {
		t.Errorf("boot arg %q", arg)
	}
	if volumeBootArg(backend.Spec{}) != "" {
		t.Error("no volumes should add no boot argument")
	}
}

// A sandbox's environment reaches the guest over the agent channel, never in
// the VMM's config or the kernel command line, both of which are files and
// process arguments other things on the host can read.
func TestBuildConfigCarriesNoEnvironmentValue(t *testing.T) {
	const secret = "s3cret-value-in-env"
	s := backend.Spec{ID: "sbx_0123456789abcdef", Image: "img", Env: map[string]string{"API_TOKEN": secret},
		Network: api.NetworkPolicy{Mode: api.NetworkNone}}
	p := vmPaths{Kernel: "/k/vmlinux", RootFS: "/i/rootfs.ext4", Scratch: "/s/scratch.ext4", VsockUDS: "/s/v.sock"}
	out, _ := json.Marshal(BuildConfig(s, p, "console=ttyS0 ro", nil))
	if strings.Contains(string(out), secret) || strings.Contains(string(out), "API_TOKEN") {
		t.Errorf("an environment variable reached the VM config: %s", out)
	}
}

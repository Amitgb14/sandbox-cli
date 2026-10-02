//go:build linux

package firecracker

import (
	"encoding/json"
	"flag"
	"net"
	"os"
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

package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/runtime"
)

// pinResolvers makes the host's real /etc/resolv.conf irrelevant to a test, and
// redirects the generated file into a scratch directory so a unit test never
// writes into the developer's own ~/.config/sandbox. Same reason pinTimezone
// exists: BuildSpec must render the same spec on every machine.
func pinResolvers(t *testing.T, servers ...string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	prev := hostResolvers
	hostResolvers = func() []string { return servers }
	t.Cleanup(func() { hostResolvers = prev })
}

// The cases that decide whether a container resolves anything. Each one is a
// shape a real host produces, and the loopback rows are the ones that matter:
// admitting them yields a container that starts, looks configured and resolves
// nothing.
func TestParseResolversKeepsOnlyRoutableAddresses(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   string
		want []string
	}{
		{
			name: "ordinary host",
			in:   "nameserver 75.75.75.75\nnameserver 75.75.76.76\n",
			want: []string{"75.75.75.75", "75.75.76.76"},
		},
		{
			name: "systemd-resolved stub is unreachable from a container",
			in:   "nameserver 127.0.0.53\noptions edns0\n",
			want: nil,
		},
		{
			name: "docker's embedded resolver is loopback too, so it drops out",
			in:   "nameserver 127.0.0.11\noptions ndots:0\n",
			want: nil,
		},
		{
			name: "mixed: the routable one survives",
			in:   "nameserver 127.0.0.53\nnameserver 8.8.8.8\n",
			want: []string{"8.8.8.8"},
		},
		{
			// Not because it is a bad address, but because it is unreachable from
			// this container: the sandbox network has no IPv6 and the firewall skips
			// IPv6 nameservers outright. Keeping it would satisfy "a resolver was
			// found" while leaving the container with nothing it can reach.
			name: "IPv6 resolvers cannot be reached from the sandbox network",
			in:   "nameserver 2001:558:feed::1\n",
			want: nil,
		},
		{
			// The shape an IPv6-first network produces, and the one that made the
			// refusal fire on a false positive: the v4 entry is a loopback stub and
			// the v6 one is real, so admitting IPv6 meant "found one" and a silent
			// no-DNS container.
			name: "loopback v4 plus real v6 leaves nothing usable",
			in:   "nameserver 127.0.0.53\nnameserver 2001:4860:4860::8888\n",
			want: nil,
		},
		{
			name: "a real v4 resolver alongside v6 still counts",
			in:   "nameserver 2001:4860:4860::8888\nnameserver 8.8.4.4\n",
			want: []string{"8.8.4.4"},
		},
		{
			name: "comments, search domains and options are not resolvers",
			in:   "# nameserver 1.2.3.4\nsearch corp.example\noptions ndots:2\n; nameserver 5.6.7.8\n",
			want: nil,
		},
		{
			name: "trailing comment on a real line",
			in:   "nameserver 9.9.9.9 # quad9\n",
			want: []string{"9.9.9.9"},
		},
		{
			name: "duplicates collapse",
			in:   "nameserver 8.8.8.8\nnameserver 8.8.8.8\n",
			want: []string{"8.8.8.8"},
		},
		{
			name: "a hostname is not an address and cannot be resolved without a resolver",
			in:   "nameserver dns.example.com\n",
			want: nil,
		},
		{
			name: "0.0.0.0 names no host",
			in:   "nameserver 0.0.0.0\n",
			want: nil,
		},
		{
			name: "empty file",
			in:   "",
			want: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := parseResolvers(tc.in)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("parseResolvers() = %v, want %v", got, tc.want)
			}
		})
	}
}

// The generated file is what the container actually reads, so its content is
// part of the contract rather than an implementation detail.
func TestWriteResolvConfEmitsOnlyNameservers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resolv.conf")
	if err := writeResolvConf(path, []string{"8.8.8.8", "1.1.1.1"}); err != nil {
		t.Fatalf("writeResolvConf: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	got := string(data)
	for _, want := range []string{"nameserver 8.8.8.8\n", "nameserver 1.1.1.1\n"} {
		if !strings.Contains(got, want) {
			t.Errorf("generated file missing %q:\n%s", want, got)
		}
	}
	// A search domain from the host names an internal network and the container
	// has no use for it, so none is written.
	if strings.Contains(got, "search ") {
		t.Errorf("generated file carries a search domain, which leaks a fact about the host:\n%s", got)
	}
	// The container reads it as the sandbox user, not as the host user who wrote it.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm()&0o044 == 0 {
		t.Errorf("mode = %v, want world-readable so the container can read it", info.Mode().Perm())
	}
}

// resolvMount finds the generated resolv.conf in a spec, or reports absence.
func resolvMount(spec runtime.RunSpec) (runtime.Mount, bool) {
	for _, m := range spec.Mounts {
		if m.Target == resolvConfTarget {
			return m, true
		}
	}
	return runtime.Mount{}, false
}

// The feature itself: a runtime that cannot reach the engine's embedded resolver
// gets real ones, and every other runtime is left exactly as it was.
func TestBuildSpecSuppliesResolversOnlyWhereTheyAreNeeded(t *testing.T) {
	for _, tc := range []struct {
		name      string
		runtime   string
		mode      string
		wantMount bool
	}{
		{name: "gVisor cannot reach 127.0.0.11", runtime: "runsc", mode: "default", wantMount: true},
		{name: "gVisor under the containerd shim name", runtime: "io.containerd.runsc.v1", mode: "default", wantMount: true},
		{name: "gVisor in allowlist mode too", runtime: "runsc", mode: "allowlist", wantMount: true},
		{name: "the host default is untouched", runtime: "", mode: "default", wantMount: false},
		{name: "runc is untouched", runtime: "runc", mode: "default", wantMount: false},
		// Kata boots a real kernel, so the question has a different answer and has
		// not been measured. Guessing would swap a working resolver for the host's.
		{name: "kata is not assumed to share the defect", runtime: "kata-fc", mode: "default", wantMount: false},
		// No interfaces, so no resolver is worth mounting.
		{name: "network none needs nothing", runtime: "runsc", mode: "none", wantMount: false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pinResolvers(t, "8.8.8.8")
			cfg := baseCfg()
			cfg.Network.Mode = tc.mode
			spec, err := BuildSpec(cfg, Options{
				Project: t.TempDir(), Runtime: tc.runtime, Command: []string{"sh"},
			})
			if err != nil {
				t.Fatalf("BuildSpec: %v", err)
			}
			m, ok := resolvMount(spec)
			if ok != tc.wantMount {
				t.Fatalf("resolv.conf mount present = %v, want %v", ok, tc.wantMount)
			}
			if !ok {
				return
			}
			if !m.RO {
				t.Error("resolv.conf mount is writable; the agent must not be able to redirect its own name resolution")
			}
			// BuildSpec decides; the launch path writes. Both halves are checked in
			// TestBuildSpecDoesNotWriteTheResolvConf; here we just need the content.
			if err := materializeResolvConf(spec); err != nil {
				t.Fatalf("materializeResolvConf: %v", err)
			}
			data, err := os.ReadFile(m.Source)
			if err != nil {
				t.Fatalf("generated file unreadable: %v", err)
			}
			if !strings.Contains(string(data), "nameserver 8.8.8.8") {
				t.Errorf("generated file does not carry the host's resolver:\n%s", data)
			}
		})
	}
}

// The regression the first version shipped: `network` is not final until the
// allowlist has had its say, because an allowlist needs bridge networking and
// promotes a configured `mode: none`. Deciding the resolver before that line
// meant this combination got a networked container with docker's unreachable
// 127.0.0.11 and no refusal — the exact silent no-DNS state the refusal exists
// to prevent, reached by never calling it.
func TestBuildSpecSuppliesResolversWhenTheAllowlistPromotesNetworkNone(t *testing.T) {
	pinResolvers(t, "8.8.8.8")
	cfg := baseCfg()
	cfg.Network.Mode = "none"
	spec, err := BuildSpec(cfg, Options{
		Project: t.TempDir(), Runtime: "runsc", Allow: []string{"api.example.com"},
		Command: []string{"sh"},
	})
	if err != nil {
		t.Fatalf("BuildSpec: %v", err)
	}
	if spec.Network == "none" {
		t.Fatal("an allowlist needs bridge networking; the premise of this test is gone")
	}
	if _, ok := resolvMount(spec); !ok {
		t.Error("allowlist promoted the network but no resolvers were supplied: " +
			"the container would reach the bridge and resolve nothing")
	}
}

// A user who mounted their own /etc/resolv.conf has answered the question. Two
// mounts on one target is a docker error rather than a decision.
func TestBuildSpecYieldsToAUserSuppliedResolvConf(t *testing.T) {
	pinResolvers(t, "8.8.8.8")
	dir := t.TempDir()
	own := filepath.Join(dir, "my-resolv.conf")
	if err := os.WriteFile(own, []byte("nameserver 9.9.9.9\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	spec, err := BuildSpec(baseCfg(), Options{
		Project: t.TempDir(), Runtime: "runsc", Command: []string{"sh"},
		ExtraMounts: []string{own + ":" + resolvConfTarget},
	})
	if err != nil {
		t.Fatalf("BuildSpec: %v", err)
	}
	var n int
	for _, m := range spec.Mounts {
		if m.Target == resolvConfTarget {
			n++
		}
	}
	if n != 1 {
		t.Fatalf("%d mounts on %s; docker refuses a duplicate mount point", n, resolvConfTarget)
	}
	m, _ := resolvMount(spec)
	if m.Source != own {
		t.Errorf("generated file won over the user's explicit mount: source = %q, want %q", m.Source, own)
	}
}

// BuildSpec is what --dry-run calls, and printing a command must not touch the
// filesystem — the rule ShareWithSandboxGroup and forwardedValues already keep.
func TestBuildSpecDoesNotWriteTheResolvConf(t *testing.T) {
	pinResolvers(t, "8.8.8.8")
	spec, err := BuildSpec(baseCfg(), Options{
		Project: t.TempDir(), Runtime: "runsc", Command: []string{"sh"},
	})
	if err != nil {
		t.Fatalf("BuildSpec: %v", err)
	}
	m, ok := resolvMount(spec)
	if !ok {
		t.Fatal("no resolver mount; the premise of this test is gone")
	}
	if _, err := os.Stat(m.Source); !os.IsNotExist(err) {
		t.Errorf("BuildSpec wrote %s; resolving a spec must not have side effects", m.Source)
	}
	// ...and the launch path does write it, or the mount would carry nothing.
	if err := materializeResolvConf(spec); err != nil {
		t.Fatalf("materializeResolvConf: %v", err)
	}
	data, err := os.ReadFile(m.Source)
	if err != nil {
		t.Fatalf("launch path did not write the file: %v", err)
	}
	if !strings.Contains(string(data), "nameserver 8.8.8.8") {
		t.Errorf("written file does not carry the resolver:\n%s", data)
	}
}

// A container with no resolvers resolves nothing, which is the state this whole
// mechanism exists to prevent — so it refuses instead of starting one.
func TestBuildSpecRefusesWhenNoResolverCanBeFound(t *testing.T) {
	pinResolvers(t) // e.g. a host whose only nameserver is systemd-resolved's 127.0.0.53
	_, err := BuildSpec(baseCfg(), Options{
		Project: t.TempDir(), Runtime: "runsc", Command: []string{"sh"},
	})
	if err == nil {
		t.Fatal("BuildSpec succeeded with no usable resolver; want a refusal")
	}
	// The message has to name the file to look at — the failure it replaces is an
	// agent reporting that every request failed, which points at nothing.
	if !strings.Contains(err.Error(), "/etc/resolv.conf") {
		t.Errorf("refusal does not say where to look: %v", err)
	}
	// And it must not tell the reader to drop a flag they never typed: the
	// runtime is as often a `runtime:` key in their config as a command-line
	// flag, and "run without --runtime runsc" then points at nothing they can edit.
	if strings.Contains(err.Error(), "without --runtime") {
		t.Errorf("refusal assumes the runtime came from a flag: %v", err)
	}
}

// Rewritten on every run, so writing over an existing file must not fail or
// append.
func TestWriteResolvConfIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "resolv.conf")
	if err := writeResolvConf(path, []string{"8.8.8.8"}); err != nil {
		t.Fatalf("first write: %v", err)
	}
	if err := writeResolvConf(path, []string{"1.1.1.1"}); err != nil {
		t.Fatalf("second write: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading back: %v", err)
	}
	if strings.Contains(string(data), "8.8.8.8") {
		t.Errorf("second write did not replace the first:\n%s", data)
	}
	if !strings.Contains(string(data), "nameserver 1.1.1.1") {
		t.Errorf("second write did not land:\n%s", data)
	}
}

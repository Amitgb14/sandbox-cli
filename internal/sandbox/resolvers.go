package sandbox

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
)

// Name resolution for runtimes that cannot reach the engine's embedded DNS
// server (runtime.EmbeddedResolverUnreachable explains which, and why).
//
// The container gets a resolv.conf sandbox-cli **generates**, mounted read-only
// over /etc/resolv.conf. Three properties decided this shape over the two
// alternatives:
//
//   - It is not the host's /etc/resolv.conf. timezone.go argues that a name is a
//     string and a mount is another host path, and that rule holds here: what is
//     mounted is a file this tool wrote into its own state directory from values
//     read on the host, the same pattern as the generated managed-settings.json.
//     The host's own file is read and never exposed — it commonly contains search
//     domains that name an employer's internal network.
//   - It works in **every** network mode. Passing the addresses in an environment
//     variable for the root entrypoint to write was the obvious alternative, and
//     it is wrong: that entrypoint runs in allowlist mode only, so
//     `--runtime runsc --network default` would have stayed silently broken.
//   - A hostile project config cannot reach it. Choosing a container's resolver is
//     a redirection primitive — point every name at a resolver you control and the
//     allowlist resolves addresses of your choosing while looking entirely correct
//     — and `mounts` is already refused from a project .sandbox.yaml
//     (config/trust.go), so this inherits that refusal instead of needing a new
//     reserved name.
//
// Docker's embedded resolver also answers *container* names on the network. That
// is lost here, and it is not a cost worth mitigating: sandboxes reaching each
// other by name is what enable_icc=false exists to prevent.

// hostResolvers is a var for the same reason hostTimezone and hostPrimaryGID
// are: BuildSpec must produce the same spec on every machine, and this is one of
// the few inputs that genuinely differs per host.
var hostResolvers = readHostResolvers

// resolvConfTarget is the file the container reads to find its resolvers.
const resolvConfTarget = "/etc/resolv.conf"

// hasMountTarget reports whether something is already mounted at target.
func hasMountTarget(mounts []runtime.Mount, target string) bool {
	for _, m := range mounts {
		if m.Target == target {
			return true
		}
	}
	return false
}

// resolvConfPath is where the generated file lives. One path shared by every
// run, rewritten each time, rather than one per session: the content is a
// property of the host and not of the run, and a per-run file would be state to
// clean up after a crash.
func resolvConfPath() string {
	root := config.ConfigRoot()
	if root == "" {
		return ""
	}
	return filepath.Join(root, "resolv.conf")
}

// hostResolverMount decides the mount, and deliberately does **not** write it.
//
// BuildSpec is what `--dry-run` calls, and printing a command must not touch the
// filesystem — the rule ShareWithSandboxGroup and forwardedValues already keep.
// Reading the host's resolv.conf here is fine and necessary, because the refusal
// below has to be reachable from a preview: a run that cannot resolve anything
// should say so when you ask what it would do, not only when you do it. The file
// is materialised later by materializeResolvConf, from Run and Start.
//
// It refuses rather than proceeding when no usable resolver was found. The
// alternative is a container that starts, looks healthy and resolves nothing —
// and an agent reporting that every request failed is a much worse diagnostic
// than a refusal that names the file to look at.
func hostResolverMount(runtimeName string) (runtime.Mount, error) {
	// Named without a leading `--`: the runtime is as often a `runtime:` key in
	// the user's config as a flag on the command line, and telling someone to
	// "run without --runtime runsc" when they never typed it points at nothing
	// they can edit.
	path := resolvConfPath()
	if path == "" {
		return runtime.Mount{}, fmt.Errorf(
			"runtime %q needs DNS resolvers supplied to the container, but the home "+
				"directory could not be determined, so there is nowhere to write them",
			runtimeName)
	}
	// The same check every other mount goes through. A home directory containing
	// a comma cannot be expressed in docker's --mount CSV, and the refusal that
	// says so beats docker's parse error.
	if err := ValidateMountPath("generated resolv.conf", path); err != nil {
		return runtime.Mount{}, err
	}

	servers := hostResolvers()
	if len(servers) == 0 {
		return runtime.Mount{}, fmt.Errorf(
			"runtime %q cannot use docker's embedded DNS server, and no resolver this "+
				"container could reach was found in /etc/resolv.conf on this host. "+
				"Loopback resolvers (systemd-resolved's 127.0.0.53, docker's own "+
				"127.0.0.11) and IPv6 resolvers are both unreachable from the sandbox "+
				"network, which is IPv4-only. The container would resolve no names at "+
				"all, so this run is refused rather than started. Add a routable IPv4 "+
				"`nameserver` line to /etc/resolv.conf, or select a different runtime "+
				"(--runtime, or `runtime:` in your config)",
			runtimeName)
	}
	return runtime.Mount{Source: path, Target: resolvConfTarget, RO: true}, nil
}

// materializeResolvConf writes the file the spec's mount points at, if it has
// one. Called from Run and Start and never from Prepare, for the reason given on
// hostResolverMount — the same split ShareWithSandboxGroup makes.
//
// It re-reads the host's resolvers rather than carrying them on the RunSpec.
// That costs one file read and keeps the spec a description of the container
// rather than a courier for host state; it also means a resolver list that
// changed between resolving the spec and starting the container is the fresher
// one, which is the answer you want when a VPN came up in between.
func materializeResolvConf(spec runtime.RunSpec) error {
	for _, m := range spec.Mounts {
		if m.Target != resolvConfTarget || m.Source != resolvConfPath() {
			continue
		}
		servers := hostResolvers()
		if len(servers) == 0 {
			// BuildSpec already refused on this, so reaching it means the host's
			// resolvers changed underneath us between resolving and starting.
			return fmt.Errorf("no usable DNS resolver on this host any more; "+
				"cannot write %s", m.Source)
		}
		if err := writeResolvConf(m.Source, servers); err != nil {
			return fmt.Errorf("writing %s: %w", m.Source, err)
		}
		return nil
	}
	return nil
}

// writeResolvConf writes the file atomically, because concurrent runs — a fleet,
// or two terminals — share this one path. The content is identical for all of
// them, so a rename race is harmless; a half-written file read by a container
// starting at that moment would not be.
func writeResolvConf(path string, servers []string) error {
	var b strings.Builder
	b.WriteString("# Generated by sandbox-cli. Do not edit; rewritten on every run.\n")
	b.WriteString("# The engine's embedded resolver is unreachable on this runtime, so the\n")
	b.WriteString("# host's own resolvers are used directly. See internal/sandbox/resolvers.go.\n")
	for _, s := range servers {
		b.WriteString("nameserver " + s + "\n")
	}
	// No `search` line, deliberately. Search domains come from the host's resolv.conf
	// and routinely name an internal network; the container has no use for them and
	// they would leak a fact about where the host sits.

	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), "resolv.conf.*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.WriteString(b.String()); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	// World-readable: the container reads this as the sandbox user, and unlike the
	// state directories there is nothing private in it — these are the addresses of
	// name servers, not credentials.
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

// readHostResolvers returns the host's routable nameserver addresses, in the
// order /etc/resolv.conf lists them.
//
// Three classes are dropped, and each would produce a container that resolves
// nothing while looking correctly configured:
//
//   - **Loopback.** A host running systemd-resolved lists 127.0.0.53, and a
//     container's 127.0.0.53 is its own loopback, where nothing is listening.
//     This is also what makes the docker case self-correcting: 127.0.0.11 is
//     loopback too, so a resolv.conf that has already been rewritten by an engine
//     contributes nothing here.
//   - **IPv6.** Not because the address is unusable in principle, but because it
//     is unusable *here*: the sandbox network is created without IPv6, and in
//     allowlist mode sandbox-egress-setup skips IPv6 nameservers outright
//     (`case "$ns" in *:*) continue`) since IPv6 egress is rejected wholesale.
//     Keeping one would satisfy the "some resolver was found" test while leaving
//     the container with nothing it can actually reach — the precise failure the
//     refusal exists to prevent, dressed as success. An IPv6-only host therefore
//     gets the refusal, which is the honest answer until the sandbox network has
//     IPv6 to offer.
//   - **Anything that is not an IP address.** resolv.conf takes addresses, not
//     names; a name here could only come from a malformed file, and resolving it
//     would need the resolver we are trying to configure.
func readHostResolvers() []string {
	data, err := os.ReadFile("/etc/resolv.conf")
	if err != nil {
		return nil
	}
	return parseResolvers(string(data))
}

// parseResolvers is the parsing half, split out so it can be tested against the
// shapes real hosts produce without one of them having to be the test machine.
func parseResolvers(data string) []string {
	var out []string
	seen := map[string]bool{}
	for _, line := range strings.Split(data, "\n") {
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		if i := strings.IndexByte(line, ';'); i >= 0 {
			line = line[:i]
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || fields[0] != "nameserver" {
			continue
		}
		ip := net.ParseIP(fields[1])
		if ip == nil || ip.To4() == nil || ip.IsLoopback() || ip.IsUnspecified() || ip.IsLinkLocalUnicast() {
			continue
		}
		if seen[fields[1]] {
			continue
		}
		seen[fields[1]] = true
		out = append(out, fields[1])
	}
	return out
}

package runtime

import (
	"context"
	"os/exec"
	"strings"
)

// Whether a container on a sandbox-shaped network can resolve a name.
//
// It is asked because sandbox-cli chooses that network. Under podman every
// sandbox gets its **own** network (netavark rejects docker's enable_icc, and
// its nearest option leaves same-network peers reachable — see networkCreateArgs),
// and a custom podman network resolves through aardvark-dns where the default
// rootless network does not. So the tool silently depends on a resolver it
// caused to be used.
//
// On the host that prompted this, that resolver did not work — nftables-backed
// firewalld on RHEL 10 — and every lookup timed out. What the user saw was four
// layers away from the cause:
//
//	OAuth error: getaddrinfo ETIMEOUT platform.claude.com
//
// an agent hanging at login. doctor already *tries* things rather than querying
// them; this is the same kind of question and nothing was asking it.

// DNSProbe is what a name-resolution probe found.
type DNSProbe int

const (
	// DNSUnknown: the question could not be put — no image, or the probe could
	// not be started. The zero value, deliberately, so a path that returns
	// without deciding reports "I do not know" rather than "this host is fine".
	DNSUnknown DNSProbe = iota
	// DNSOK: a container on a sandbox-shaped network resolved a name.
	DNSOK
	// DNSSandboxBroken: it could not, but a container on the engine's default
	// network could. That difference is the whole point — it isolates a resolver
	// broken on the network *sandbox-cli creates* from a host with no DNS at all,
	// and only the first is this tool's doing.
	DNSSandboxBroken
	// DNSNoResolver: neither network could resolve. Nothing here is
	// sandbox-specific; the host cannot look up names, and an agent will fail at
	// login whatever sandbox-cli does.
	DNSNoResolver
)

// probeName is looked up rather than fetched. `getent hosts` uses the resolver
// and nothing else — no TLS, no HTTP, no dependency on the name being reachable,
// so a host that resolves but cannot connect is not reported as a DNS failure.
//
// A domain from the egress baseline, so a reader who sees it in `docker ps` or a
// firewall log recognises it as ours rather than as the agent reaching out.
const probeName = "registry.npmjs.org"

// ResolvesNames reports whether a container on a sandbox-shaped network can
// resolve a name, and distinguishes that from a host with no DNS at all.
func (d *DockerCLI) ResolvesNames(ctx context.Context, image string) (DNSProbe, string) {
	if image == "" {
		return DNSUnknown, "no base image to probe with"
	}

	// The sandbox-shaped network first, since it is the one under suspicion.
	// Under podman that means a throwaway of the same shape a run would create;
	// under docker the shared one, which is what every run joins.
	network := SandboxNetwork
	if d.PerRunNetwork() {
		network = SandboxNetwork + "-dnsprobe"
		if err := exec.CommandContext(ctx, d.bin(), d.networkCreateArgs(network)...).Run(); err != nil {
			return DNSUnknown, "could not create a network to probe: " + err.Error()
		}
		defer d.RemoveNetwork(context.WithoutCancel(ctx), network)
	} else if err := d.EnsureNetwork(ctx, network); err != nil {
		return DNSUnknown, "could not prepare the sandbox network: " + err.Error()
	}

	if ok, reason := d.resolvesOn(ctx, image, network); ok {
		return DNSOK, ""
	} else if onDefault, _ := d.resolvesOn(ctx, image, ""); onDefault {
		return DNSSandboxBroken, reason
	} else {
		return DNSNoResolver, reason
	}
}

// resolvesOn runs one lookup in a throwaway container on the given network, or
// on the engine's default when it is empty.
func (d *DockerCLI) resolvesOn(ctx context.Context, image, network string) (bool, string) {
	args := []string{"run", "--rm", "--entrypoint", "sh"}
	if network != "" {
		args = append(args, "--network", network)
	}
	args = append(args, image, "-c", "getent hosts "+probeName+" >/dev/null")
	out, err := exec.CommandContext(ctx, d.bin(), args...).CombinedOutput()
	if err == nil {
		return true, ""
	}
	reason := strings.TrimSpace(lastLine(string(out)))
	if reason == "" {
		// getent exits non-zero and says nothing when the name does not resolve,
		// which is the ordinary shape of this failure rather than an odd one.
		reason = "could not resolve " + probeName
	}
	return false, reason
}

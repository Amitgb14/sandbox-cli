package runtime

import (
	"context"
	"os/exec"
	"strings"
)

// Reaping the per-run networks podman leaves behind.
//
// Under podman each sandbox gets a network of its own (see networkCreateArgs for
// why), and a **detached** run deliberately leaves that network with machine
// lifetime rather than the process's — nothing is left to remove it when the
// launcher exits. `clean` is the collector.
//
// The cost of not collecting is worse than untidiness, which is what the first
// version of this reaper assumed. A network whose container is gone but whose
// IPAM entry survives makes `podman network reload --all` fail **for every
// network on the host**:
//
//	ERRO IPAM error: failed to get ips for container 08cbb630… on network sandbox-cli-…
//	Error: netavark: setns: IO error: Invalid argument (os error 22)
//
// That command is the documented repair after firewalld drops netavark's rules,
// so a leaked sandbox network blocks the fix for an unrelated, host-wide problem.
// Issue #77.

// NetworkReaper is the optional capability of collecting the per-run networks an
// engine leaves behind. Named rather than asserted inline at the call site: an
// unchecked assertion turns a signature change into a silent no-op — `clean`
// reporting success while leaving the network, which is the exact failure this
// file exists to remove — and it leaves the wiring untestable, since a fake that
// does not implement it simply takes the "not supported" path.
type NetworkReaper interface {
	// ownerFilter is a `label=value` selector for the containers this tool owns,
	// passed in rather than named here: it is defined in internal/sandbox, which
	// imports this package, and a second copy of the constant that decides what
	// may be force-removed is the kind of duplication that drifts quietly.
	ReapPerRunNetworks(ctx context.Context, ownerFilter string) []NetworkReap
}

// The engine implements it; asserted here so a signature drift is a build
// failure rather than a `clean` that silently stops reaping.
var _ NetworkReaper = (*DockerCLI)(nil)

// NetworkReap is what happened to one network, so a caller can say so. A reaper
// that fails silently is how the leak survived a `clean` that reported success.
type NetworkReap struct {
	Name    string
	Removed bool
	// Reason is why it was left, empty when Removed. Prose for a human.
	Reason string
}

// attachment is one container on a network: what state it is in, and whether it
// is ours. Both are needed — see planForNetwork.
type attachment struct {
	Name    string
	State   string
	Sandbox bool
}

// networkPlan is the decision for one network, taken from what is attached to
// it. Pure, so every case below is testable without an engine.
type networkPlan int

const (
	// planSkip: something live is on it. Removing a network out from under a
	// running agent takes its networking away mid-run.
	planSkip networkPlan = iota
	// planRemove: nothing is attached. This is the state that wedges
	// `network reload --all`, and a plain `network rm` clears it.
	planRemove
	// planForce: only finished containers are attached. `network rm` **refuses**
	// this case — measured on podman 6.0.2:
	//
	//	Error: "sandbox-cli-x" has associated containers with it.
	//	Use -f to forcibly delete containers and pods: network is being used
	//
	// which is precisely what the old reaper mistook for "a running container
	// holds it open, so this moves on". The husk is a container `clean` would
	// remove anyway; -f removes it with the network.
	planForce
)

// planForNetwork decides what to do with one per-run network given the states of
// the containers attached to it.
//
// "Live" is the same judgement `clean` makes about a session, and deliberately
// the same shape: anything not clearly finished counts as live, because not
// knowing is not a licence to remove somebody's network. `created` counts as
// finished for the reason it does there — a husk from a launch that never
// started holds nothing.
func planForNetwork(on []attachment) networkPlan {
	if len(on) == 0 {
		return planRemove
	}
	for _, a := range on {
		switch strings.TrimSpace(strings.ToLower(a.State)) {
		case "exited", "dead", "created", "stopped":
		default:
			return planSkip
		}
		// Finished, but not ours. `-f` removes the containers as well as the
		// network, and somebody else's stopped container is not this command's to
		// delete — even when it is sitting on a network sandbox-cli made. Leaving
		// the network is the smaller cost, and the printed reason says whose it
		// is.
		if !a.Sandbox {
			return planSkip
		}
	}
	return planForce
}

// ReapPerRunNetworks removes the per-run networks nothing needs any more.
//
// Two engine calls and then arithmetic: one `network ls`, one `ps -a` carrying
// each container's networks, so a host with twenty sandboxes costs the same as
// one with a single sandbox rather than a query per network.
//
// Scoped to the per-run prefix and to engines that use per-run networks at all.
// Docker shares one network, so nothing there can be attributed to a finished
// run, and sweeping `sandbox-cli-*` names on an engine this run was never
// configured for is a broader licence than the docker path takes.
//
// One window is not closed, and cannot be without a clock this can trust: a
// network created by a launch that has not yet started its container looks
// exactly like a leaked one. `network ls` reports a creation time, but podman
// prints it with a zone *abbreviation* ("PDT") that Go cannot resolve back to a
// location, so an age guard here would be a guess dressed as a measurement. The
// window is milliseconds inside an explicit `clean`, and the loser gets a run
// that refuses to start rather than one that runs unisolated.
func (d *DockerCLI) ReapPerRunNetworks(ctx context.Context, ownerFilter string) []NetworkReap {
	if !d.PerRunNetwork() {
		return nil
	}
	out, err := exec.CommandContext(ctx, d.bin(), "network", "ls", "--format", "{{.Name}}").Output()
	if err != nil {
		return nil
	}
	attached, known := d.networkAttachments(ctx, ownerFilter)
	if !known {
		// Unknown attachments must not read as "nothing is attached": every
		// network would look unused and a live sandbox would lose its networking
		// mid-run. Not knowing is not a licence — the same rule `clean` applies to
		// a container whose state it cannot read.
		return nil
	}
	var reaped []NetworkReap
	for _, name := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name = strings.TrimSpace(name)
		if !strings.HasPrefix(name, SandboxNetwork+"-") {
			continue
		}
		args := []string{"network", "rm", name}
		switch on := attached[name]; planForNetwork(on) {
		case planSkip:
			reaped = append(reaped, NetworkReap{Name: name, Reason: skipReason(on)})
			continue
		case planForce:
			args = []string{"network", "rm", "-f", name}
		}
		if msg, err := exec.CommandContext(ctx, d.bin(), args...).CombinedOutput(); err != nil {
			reaped = append(reaped, NetworkReap{Name: name, Reason: refusalReason(string(msg))})
			continue
		}
		reaped = append(reaped, NetworkReap{Name: name, Removed: true})
	}
	return reaped
}

// refusalReason is what to print when the engine refused to remove a network.
//
// An engine that exits non-zero having said nothing still has to produce a
// sentence: `lastLine("")` is `""`, so the output became "kept network x ()",
// which reads as a truncated bug rather than an explanation — and saying what
// could not be reaped is the whole promise of this reaper.
func refusalReason(engineOutput string) string {
	if why := lastLine(engineOutput); strings.TrimSpace(why) != "" {
		return why
	}
	return "the engine refused and gave no reason"
}

// skipReason says which container is holding a network, because "still in use"
// sends somebody looking through every container on the host.
func skipReason(on []attachment) string {
	for _, a := range on {
		if !a.Sandbox {
			return "a container this command does not own is on it: " + a.Name
		}
	}
	for _, a := range on {
		switch strings.TrimSpace(strings.ToLower(a.State)) {
		case "exited", "dead", "created", "stopped":
		default:
			return "a sandbox is still on it: " + a.Name
		}
	}
	return "something is still on it"
}

// networkAttachments maps each network to the containers on it.
//
// Every container, not only sandbox-cli's: whether a network can be removed is a
// question about what is attached to it, and something else attached to a
// sandbox network is exactly the case where this must not force anything.
func (d *DockerCLI) networkAttachments(ctx context.Context, ownerFilter string) (byNetwork map[string][]attachment, known bool) {
	out, err := exec.CommandContext(ctx, d.bin(), "ps", "-a", "--format", "{{.Names}}|{{.State}}|{{.Networks}}").Output()
	if err != nil {
		// Reported as *unknown* rather than as an empty map, because the two mean
		// opposite things to the caller and the empty one is the dangerous
		// reading. See the bail-out above.
		return nil, false
	}
	// Whose containers are whose, asked with the same `label=value` filter every
	// other command uses rather than by parsing the label column: label values
	// are comma-separated in that column and a value containing `,sandbox.cli=`
	// would make somebody else's container look like ours — which is the one
	// mistake that would let this delete it. The *value* is part of the filter
	// for the same reason: a bare key matches any value, so a container carrying
	// `sandbox.cli=someone-elses` would be counted as ours.
	ours := map[string]bool{}
	if mine, err := exec.CommandContext(ctx, d.bin(), "ps", "-a", "--filter", "label="+ownerFilter, "--format", "{{.Names}}").Output(); err == nil {
		for _, n := range strings.Split(strings.TrimSpace(string(mine)), "\n") {
			if n = strings.TrimSpace(n); n != "" {
				ours[n] = true
			}
		}
	} else {
		// The same rule as above: not knowing which are ours is not a licence to
		// force-remove any of them.
		return nil, false
	}

	byNetwork = map[string][]attachment{}
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		parts := strings.SplitN(strings.TrimSpace(line), "|", 3)
		if len(parts) != 3 {
			continue
		}
		name, state, nets := parts[0], parts[1], parts[2]
		for _, n := range strings.Split(nets, ",") {
			if n = strings.TrimSpace(n); n != "" {
				byNetwork[n] = append(byNetwork[n], attachment{Name: name, State: state, Sandbox: ours[name]})
			}
		}
	}
	return byNetwork, true
}

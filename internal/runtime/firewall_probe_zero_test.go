package runtime

import "testing"

// TestFirewallProbeZeroValueIsUnknown pins the direction this enum fails in.
//
// FirewallOK was iota 0, so an unset field, a zero-valued struct, or a future
// path that returned without deciding all reported "this host can program the
// firewall" — and prod would have accepted every one of them. The rule for this
// subsystem is to fail closed, so the accident-shaped value has to be the one
// that refuses.
func TestFirewallProbeZeroValueIsUnknown(t *testing.T) {
	var p FirewallProbe
	if p != FirewallUnknown {
		t.Errorf("the zero value is %v, want FirewallUnknown so an undecided probe fails closed", p)
	}
	if FirewallOK == 0 {
		t.Error("FirewallOK is the zero value; an unset field would read as a healthy host")
	}
}

// TestRuntimeRefusalIsNotAFirewallVerdict.
//
// resolveRuntime is non-fatal in two places — podman reports only the runtime it
// is *using*, and an `info` that cannot be read returns the name unchanged — so
// an unaccepted spelling still reaches `run --runtime` and the engine refuses to
// start anything. Read as "blocked", that blamed the daemon for a name it
// rejected, and contradicted the runtime check one line below, which was
// reporting the same runtime as fine.
func TestRuntimeRefusalIsNotAFirewallVerdict(t *testing.T) {
	for _, out := range []string{
		"docker: Error response from daemon: unknown or invalid runtime name: kata-runtime.",
		"Error: invalid runtime name: kata",
		"Error: failed to find runtime \"kata\" in config",
		"crun: no such runtime",
	} {
		if !runtimeRefused(out) {
			t.Errorf("runtimeRefused(%q) = false, want true: the container never started", out)
		}
	}
	// A container that ran and could not program rules is the one case that *is*
	// about the firewall, and must stay Blocked.
	for _, out := range []string{
		"iptables v1.8.9 (legacy): can't initialize iptables table `nat': Permission denied",
		"Couldn't load match `conntrack':No such file or directory",
	} {
		if runtimeRefused(out) {
			t.Errorf("runtimeRefused(%q) = true, want false: this is a real firewall failure", out)
		}
	}
}

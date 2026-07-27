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

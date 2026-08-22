package runtime

import (
	"strings"
	"testing"
)

// TestFirewallProbeArgs pins the probe's argv, which nothing else can see: it is
// built outside BuildArgs, so the --dry-run golden test does not cover it and a
// regression here would ship green.
func TestFirewallProbeArgs(t *testing.T) {
	const img, probe = "sandbox-base:test", "set -e\niptables -L"

	plain := firewallProbeArgs(img, "", probe)
	if idx := indexOf(plain, "--runtime"); idx >= 0 {
		t.Errorf("no runtime selected, so none should be sent: %v", plain)
	}

	withRT := firewallProbeArgs(img, "runsc", probe)
	rt := indexOf(withRT, "--runtime")
	if rt < 0 || withRT[rt+1] != "runsc" {
		t.Fatalf("the selected runtime must reach the argv: %v", withRT)
	}

	// Order is the part that fails silently: a flag after the image reference is
	// handed to the *container* as a command argument, so the probe would run on
	// the engine's default runtime and report on the wrong kernel while looking
	// entirely healthy.
	imgAt := indexOf(withRT, img)
	if imgAt < 0 {
		t.Fatalf("the image is missing from the argv: %v", withRT)
	}
	if rt > imgAt {
		t.Errorf("--runtime at %d is after the image at %d; docker would pass it to the container: %v",
			rt, imgAt, withRT)
	}

	// The probe script is the last argument, after -c, in both shapes.
	for _, args := range [][]string{plain, withRT} {
		if args[len(args)-1] != probe || args[len(args)-2] != "-c" {
			t.Errorf("the probe script must be the -c argument: %v", args[len(args)-3:])
		}
	}

	// The capabilities the probe needs are not optional: without them it would
	// report that a perfectly capable host cannot program the firewall.
	for _, want := range []string{"NET_ADMIN", "NET_RAW"} {
		if !strings.Contains(strings.Join(plain, " "), want) {
			t.Errorf("the probe must ask for %s: %v", want, plain)
		}
	}
}

func indexOf(ss []string, want string) int {
	for i, s := range ss {
		if s == want {
			return i
		}
	}
	return -1
}

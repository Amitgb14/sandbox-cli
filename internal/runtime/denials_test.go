package runtime

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func writeLines(t *testing.T, d *EgressDenials, chunks ...string) *bytes.Buffer {
	t.Helper()
	var out bytes.Buffer
	w := newDenyTap(&out, d)
	for _, c := range chunks {
		if _, err := w.Write([]byte(c)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	return &out
}

// TestDenyTapPassesEverythingThrough is the property that matters most: this sits
// in front of the user's terminal on every allowlisted run, so it must be
// invisible. Output is forwarded byte-for-byte whether or not it is counted.
func TestDenyTapPassesEverythingThrough(t *testing.T) {
	d := &EgressDenials{}
	in := []string{
		"sandbox-cli: egress DENY gist.github.com:443 (not on the egress allowlist)\n",
		"some ordinary agent output\n",
		"partial line with no newline",
	}
	out := writeLines(t, d, in...)
	if got, want := out.String(), strings.Join(in, ""); got != want {
		t.Errorf("output was altered:\ngot  %q\nwant %q", got, want)
	}
}

func TestDenyTapCountsAndNamesHosts(t *testing.T) {
	d := &EgressDenials{}
	writeLines(t, d,
		"sandbox-cli: egress proxy on :3128 enforcing 9 name(s)\n",
		"sandbox-cli: egress allow github.com:443\n",
		"sandbox-cli: egress DENY gist.github.com:443 (not on the egress allowlist)\n",
		"npm ERR! network request failed\n",
		"sandbox-cli: egress DENY gist.github.com:443 (not on the egress allowlist)\n",
		"sandbox-cli: egress DENY docs.python.org:443 (not on the egress allowlist)\n",
	)
	if got := d.Count(); got != 3 {
		t.Errorf("count = %d, want 3 (allow lines and ordinary output must not count)", got)
	}
	want := []string{"gist.github.com", "docs.python.org"}
	if got := d.Hosts(); !equalStrings(got, want) {
		t.Errorf("hosts = %v, want %v (distinct, in first-seen order)", got, want)
	}
}

// TestDenyTapHandlesSplitWrites covers the realistic case: a pipe delivers
// whatever it has, so a single line routinely arrives in pieces and two lines
// routinely arrive in one write.
func TestDenyTapHandlesSplitWrites(t *testing.T) {
	d := &EgressDenials{}
	writeLines(t, d,
		"sandbox-cli: egress DE",
		"NY gist.github.com:443 (not on the egress allowlist)\nsandbox-cli: egress DENY a.example:80 (x)\nleft",
		"over\n",
	)
	if got := d.Count(); got != 2 {
		t.Errorf("count = %d, want 2", got)
	}
	if got := d.Hosts(); !equalStrings(got, []string{"gist.github.com", "a.example"}) {
		t.Errorf("hosts = %v", got)
	}
}

// TestDenyTapCountsANamelessConnection pins the case the proxy logs as ":0" — a
// client dialling a bare address with no name to check, which is the obvious way
// to try to evade a name-based allowlist. It has no host to name, so it counts
// and contributes nothing to the list rather than being dropped as a parse
// failure.
func TestDenyTapCountsANamelessConnection(t *testing.T) {
	d := &EgressDenials{}
	writeLines(t, d, "sandbox-cli: egress DENY :0 (connection carries no hostname to check)\n")
	if got := d.Count(); got != 1 {
		t.Errorf("count = %d, want 1", got)
	}
	if got := d.Hosts(); len(got) != 0 {
		t.Errorf("hosts = %v, want none", got)
	}
}

// TestDenyTapBoundsWhatTheContainerCanMakeUsStore is the reason the caps exist.
// Both inputs here are chosen by the guest: how many distinct names it mentions,
// and how long a line it writes without a newline. Neither may decide how big the
// user's audit file or this process's memory gets.
func TestDenyTapBoundsWhatTheContainerCanMakeUsStore(t *testing.T) {
	d := &EgressDenials{}
	var b strings.Builder
	for i := 0; i < maxDenyHosts*4; i++ {
		fmt.Fprintf(&b, "sandbox-cli: egress DENY h%d.example:443 (nope)\n", i)
	}
	writeLines(t, d, b.String())
	if got := d.Count(); got != maxDenyHosts*4 {
		t.Errorf("count = %d, want %d — the count is exact, only the list is capped", got, maxDenyHosts*4)
	}
	if got := len(d.Hosts()); got != maxDenyHosts {
		t.Errorf("kept %d hosts, want the cap of %d", got, maxDenyHosts)
	}

	long := &EgressDenials{}
	out := writeLines(t, long,
		"sandbox-cli: egress DENY "+strings.Repeat("x", maxDenyLineBytes*2)+".example:443 (nope)\n",
		"sandbox-cli: egress DENY after.example:443 (nope)\n",
	)
	if got := long.Count(); got != 1 {
		t.Errorf("count = %d, want 1: the oversized line is dropped, not buffered", got)
	}
	if got := long.Hosts(); !equalStrings(got, []string{"after.example"}) {
		t.Errorf("hosts = %v: parsing must resume on the next line", got)
	}
	// Dropping it from the count must never drop it from the user's screen.
	if !strings.Contains(out.String(), strings.Repeat("x", maxDenyLineBytes*2)) {
		t.Error("an oversized line was truncated on its way to the terminal")
	}
}

// TestDenialsAreForgeableByTheGuest is a test that asserts a *limitation*, on
// purpose. These lines arrive on the container's stderr, which the agent also
// writes to, so any process in the sandbox can produce one. That is why the audit
// field is called egress_denied_reported rather than egress_denied — if this test
// ever fails because the channel became unforgeable, the field should be renamed
// in the same change.
func TestDenialsAreForgeableByTheGuest(t *testing.T) {
	d := &EgressDenials{}
	writeLines(t, d, "sandbox-cli: egress DENY not-really-denied.example:443 (invented by the guest)\n")
	if d.Count() != 1 || !equalStrings(d.Hosts(), []string{"not-really-denied.example"}) {
		t.Fatalf("expected the forged line to be recorded (count=%d hosts=%v)", d.Count(), d.Hosts())
	}
}

// TestNilDenialsCostsNothing covers the default-mode and detached paths, where
// there is no proxy and nothing to count: the tap must not be inserted at all,
// and the accessors must be safe on a nil receiver so auditMeta needs no branch.
func TestNilDenialsCostsNothing(t *testing.T) {
	var out bytes.Buffer
	if got := newDenyTap(&out, nil); got != (&out) {
		t.Error("a nil collector must return the destination unwrapped")
	}
	var d *EgressDenials
	if d.Count() != 0 || d.Hosts() != nil {
		t.Error("nil collector accessors must be safe")
	}
	d.Observe("sandbox-cli: egress DENY x:1 (y)") // must not panic
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

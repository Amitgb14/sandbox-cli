package sandbox

import (
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/runtime"
)

// TestCanObserveDenials pins which runs record an egress-denial count, and — more
// to the point — which deliberately do not.
//
// The interactive case is the one worth guarding. Wrapping the stream a pty run
// arrives on would let it be counted, and would cost the container its terminal
// size, so this says no on purpose. If someone later "fixes" that by tapping
// stdout, this test is where the trade gets re-argued rather than silently made.
func TestCanObserveDenials(t *testing.T) {
	allow := map[string]string{"SANDBOX_EGRESS_ALLOW": "example.com"}

	cases := []struct {
		name string
		spec runtime.RunSpec
		want bool
	}{
		{"allowlist, no pty", runtime.RunSpec{Env: allow}, true},
		{"allowlist, interactive", runtime.RunSpec{Env: allow, TTY: true}, false},
		{"no allowlist", runtime.RunSpec{Env: map[string]string{}}, false},
		{"no allowlist, interactive", runtime.RunSpec{Env: map[string]string{}, TTY: true}, false},
		{"nil env", runtime.RunSpec{}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := canObserveDenials(c.spec); got != c.want {
				t.Errorf("canObserveDenials = %v, want %v", got, c.want)
			}
		})
	}
}

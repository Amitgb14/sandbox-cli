package sandbox

import (
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/config"
)

// A detached run gets no pty — that is the rule Console is the exception to,
// and both halves are pinned here because the exception is only safe while the
// default holds.
func TestDetachDropsTheTTYUnlessAConsoleWasAsked(t *testing.T) {
	cfg := config.Default()
	for _, tc := range []struct {
		name    string
		console bool
		want    bool
	}{
		{"unattended", false, false},
		{"console", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tty := true
			spec, err := BuildSpec(cfg, Options{
				Project: t.TempDir(),
				Detach:  true,
				Console: tc.console,
				// Explicitly asked for, so this also pins that --tty does not by
				// itself buy a console: without Console it is still dropped.
				TTY: &tty,
			})
			if err != nil {
				t.Fatalf("BuildSpec: %v", err)
			}
			if spec.TTY != tc.want {
				t.Errorf("TTY = %v, want %v", spec.TTY, tc.want)
			}
			// Whatever the console says, a detached container is still kept: its
			// exit code and logs are the entire supervision story.
			if spec.Remove {
				t.Error("a detached run must not be --rm")
			}
		})
	}
}

package agents

import (
	"reflect"
	"strings"
	"testing"
)

// The headless argv must not change when the permission flag moves onto its own
// field. It was inside AutonomousArgs; Autonomous now appends it. Same result,
// one definition.
func TestAutonomousStillCarriesTheSkipFlag(t *testing.T) {
	for name, want := range map[string]string{
		"claude": "--dangerously-skip-permissions",
		"gemini": "--yolo",
	} {
		d, ok := Lookup(name)
		if !ok {
			t.Fatalf("%s missing", name)
		}
		if argv := strings.Join(d.Autonomous("do it", nil), " "); !strings.Contains(argv, want) {
			t.Errorf("%s: autonomous argv %q lacks %s", name, argv, want)
		}
	}
}

// A console run is the agent's own UI. The permission flag is opt-in there,
// because an interactive session is where being asked is the point.
func TestConsoleAsksUnlessToldNotTo(t *testing.T) {
	d, _ := Lookup("claude")

	plain := strings.Join(d.Console("look at this", false), " ")
	if strings.Contains(plain, "--dangerously-skip-permissions") {
		t.Errorf("console must ask by default, got %q", plain)
	}
	if !strings.HasSuffix(plain, "look at this") {
		t.Errorf("prompt should seed the first turn, got %q", plain)
	}
	// Headless mode must not leak in: -p runs to completion and exits, which is
	// the opposite of what a console is for.
	if strings.Contains(plain, " -p ") {
		t.Errorf("console argv must not be headless, got %q", plain)
	}

	skipping := strings.Join(d.Console("look at this", true), " ")
	if !strings.Contains(skipping, "--dangerously-skip-permissions") {
		t.Errorf("skipPermissions must add the flag, got %q", skipping)
	}
}

// No prompt means just the agent, waiting.
func TestConsoleWithoutAPromptIsBareArgv(t *testing.T) {
	d, _ := Lookup("claude")
	if got, want := d.Console("", false), d.Command; !reflect.DeepEqual(got, want) {
		t.Errorf("Console() = %v, want the bare command %v", got, want)
	}
}

// Asking an agent that has no such flag changes nothing. The caller is meant to
// have refused; quietly doing something else would be worse.
func TestConsoleIgnoresSkipWhereThereIsNoFlag(t *testing.T) {
	for _, name := range []string{"codex", "opencode"} {
		d, ok := Lookup(name)
		if !ok {
			continue
		}
		if d.CanSkipPermissions() {
			t.Errorf("%s: expected no separable skip flag", name)
		}
		if got, want := d.Console("x", true), d.Console("x", false); !reflect.DeepEqual(got, want) {
			t.Errorf("%s: skip changed the argv to %v", name, got)
		}
	}
}

package agents

import (
	"reflect"
	"strings"
	"testing"
)

func TestLookup(t *testing.T) {
	for _, name := range Names() {
		d, ok := Lookup(name)
		if !ok {
			t.Fatalf("Names() returned %q but Lookup failed", name)
		}
		if d.Name != name {
			t.Errorf("descriptor %q has Name %q", name, d.Name)
		}
		if d.PersistDir == "" {
			t.Errorf("%s: PersistDir must not be empty (it names the login dir)", name)
		}
		if len(d.Command) == 0 {
			t.Errorf("%s: Command must not be empty", name)
		}
		if d.AutonomousArgs == nil {
			t.Errorf("%s: AutonomousArgs must be set; detached runs depend on it", name)
		}
	}
	if _, ok := Lookup("nope"); ok {
		t.Error("Lookup of an unknown agent should report !ok")
	}
}

func TestNamesSorted(t *testing.T) {
	got := Names()
	if len(got) < 2 {
		t.Fatalf("expected at least claude and codex, got %v", got)
	}
	// Sorted order is contractual: it goes into help text and error messages.
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Fatalf("Names() not sorted: %v", got)
		}
	}
}

func TestAutonomousPrefixesCommand(t *testing.T) {
	for _, name := range Names() {
		d, _ := Lookup(name)
		argv := d.Autonomous("do the thing", nil)
		if len(argv) < len(d.Command) {
			t.Fatalf("%s: argv shorter than Command: %v", name, argv)
		}
		if !reflect.DeepEqual(argv[:len(d.Command)], d.Command) {
			t.Errorf("%s: argv does not start with Command\n got %v\nwant prefix %v", name, argv, d.Command)
		}
		// The prompt must survive as its own argument, never concatenated or
		// shell-quoted: it reaches the agent through docker's argv, not a shell.
		found := false
		for _, a := range argv {
			if a == "do the thing" {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: prompt not present as a distinct argument: %v", name, argv)
		}
	}
}

func TestAutonomousExtraGoesLast(t *testing.T) {
	d, _ := Lookup("claude")
	argv := d.Autonomous("p", []string{"--model", "opus"})
	if got := argv[len(argv)-2:]; !reflect.DeepEqual(got, []string{"--model", "opus"}) {
		t.Errorf("extra args should be appended last, got tail %v in %v", got, argv)
	}
}

// The descriptor's Command must never be aliased into a returned argv: a caller
// appending to the result would otherwise corrupt the registry for every later
// run in the same process (which is exactly what a fleet does).
func TestAutonomousDoesNotAliasCommand(t *testing.T) {
	d, _ := Lookup("claude")
	before := append([]string(nil), d.Command...)

	argv := d.Autonomous("first", nil)
	argv = append(argv, "appended-by-caller")
	_ = argv

	after, _ := Lookup("claude")
	if !reflect.DeepEqual(after.Command, before) {
		t.Errorf("registry Command mutated: got %v, want %v", after.Command, before)
	}
	if second := d.Autonomous("second", nil); !reflect.DeepEqual(second[:len(before)], before) {
		t.Errorf("second call lost its Command prefix: %v", second)
	}
}

func TestClaudeAutonomousIsNonInteractive(t *testing.T) {
	d, _ := Lookup("claude")
	argv := strings.Join(d.Autonomous("x", nil), " ")
	// A detached container has no terminal, so an agent that can still stop to
	// ask a question hangs forever rather than failing.
	for _, want := range []string{"-p", "--dangerously-skip-permissions"} {
		if !strings.Contains(argv, want) {
			t.Errorf("claude autonomous argv missing %q: %s", want, argv)
		}
	}
}

func TestCodexAutonomousUsesExec(t *testing.T) {
	d, _ := Lookup("codex")
	argv := d.Autonomous("x", nil)
	if argv[len(d.Command)] != "exec" {
		t.Errorf("codex autonomous argv should start the exec subcommand: %v", argv)
	}
}

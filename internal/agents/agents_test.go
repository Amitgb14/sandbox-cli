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

// Every descriptor in the registry is offered to `fleet run`, and a fleet is
// unattended: an agent that stops to ask permission does not fail, it hangs
// until someone notices. So each one must return an argv that runs to completion
// on its own — a headless mode *and*, where the agent has an approval step, the
// flag that skips it.
//
// This cannot be checked by inspection; it is checked by having run each of
// them. The table is the record of that, and it is here so adding a descriptor
// means adding a line to it rather than quietly widening what a fleet may name.
func TestEveryAgentHasAVerifiedHeadlessArgv(t *testing.T) {
	// agent -> the tokens that make its run non-interactive.
	headless := map[string][]string{
		// Verified 2026-08-24 by running it: a bare positional prompt is cline's
		// non-interactive mode — act mode with auto-approve on, TUI behind `-i` —
		// so the recorded token is the prompt itself. The run wrote its file and
		// exited 0 with nothing attached.
		"cline":    {"do the thing", "--auto-approve"},
		"claude":   {"-p", "--dangerously-skip-permissions"},
		"codex":    {"exec"},
		"gemini":   {"-p", "--yolo"},
		"opencode": {"run"},
		"droid":    {"exec"},
	}
	for _, name := range Names() {
		want, ok := headless[name]
		if !ok {
			t.Errorf("agent %q is in the registry with no recorded headless argv.\n"+
				"A fleet can name it, and a fleet has no terminal: verify its non-interactive\n"+
				"mode by running it, then add it here. Do not guess the flags.", name)
			continue
		}
		d, _ := Lookup(name)
		argv := strings.Join(d.Autonomous("do the thing", nil), " ")
		for _, tok := range want {
			if !strings.Contains(argv, tok) {
				t.Errorf("%s: autonomous argv lost %q: %s", name, tok, argv)
			}
		}
	}
}

// Every line the claude bootstrap prints must go to stderr.
//
// stdout belongs to the agent: `claude -p` writes its answer there and a fleet's
// verify reads it, so one chatty install line would corrupt the only thing that
// run exists to produce. The bootstrap now says a lot more than it used to —
// announcing the install, and explaining a failure — which is exactly why this
// needs pinning rather than trusting.
func TestClaudeBootstrapNeverWritesToStdout(t *testing.T) {
	for i, line := range strings.Split(ClaudeBootstrap, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "echo ") {
			continue
		}
		if !strings.HasSuffix(trimmed, ">&2") {
			t.Errorf("line %d prints to stdout, which belongs to the agent: %q", i+1, trimmed)
		}
	}
	// The installer's own output is redirected too — it is the noisiest part.
	if !strings.Contains(ClaudeBootstrap, `bash "$installer" >&2`) {
		t.Error("the installer's output is not redirected to stderr")
	}
}

// The install must be bounded and must judge itself on the outcome. Both were
// learned from a report where a silent, unbounded install read as a hung agent
// for an evening.
func TestClaudeBootstrapIsBoundedAndChecksTheOutcome(t *testing.T) {
	for _, want := range []string{
		"--connect-timeout",                           // an unreachable host fails in seconds
		`command -v timeout`,                          // ...and the bound is not assumed to exist
		`if [ ! -x "$HOME/.local/bin/claude" ]; then`, // judged on the binary, not on exit codes
	} {
		if !strings.Contains(ClaudeBootstrap, want) {
			t.Errorf("claude bootstrap is missing %q", want)
		}
	}
}

// A console prompt is only ever appended where the descriptor says how.
//
// Written from a real failure: Console appended the prompt as a bare positional
// for every agent, and opencode reads a lone positional as the project directory
// to open — so a Studio console run died with "Failed to change directory to
// /workspace/review the code". The prompt was never a prompt to it.
func TestConsoleSeedsOnlyWhereTheDescriptorSaysHow(t *testing.T) {
	prompt := "review the code"

	for _, name := range Names() {
		d, _ := Lookup(name)
		argv := d.Console(prompt, false)
		last := ""
		if len(argv) > 0 {
			last = argv[len(argv)-1]
		}
		if d.CanSeedConsole() {
			if last != prompt {
				t.Errorf("%s says it can be seeded but its console argv does not end with the prompt: %v", name, argv)
			}
			continue
		}
		// The important half: an agent that cannot be seeded must not carry the
		// prompt anywhere in its argv, in any position.
		for _, a := range argv {
			if a == prompt {
				t.Errorf("%s cannot be seeded, yet its console argv carries the prompt (%v) — this is the bug that produced a chdir error", name, argv)
			}
		}
	}

	// opencode is the one this exists for, named so a later change that makes it
	// seedable has to come with evidence rather than a hunch.
	if d, ok := Lookup("opencode"); ok && d.CanSeedConsole() {
		t.Error("opencode is marked seedable; its bare positional is a project directory, so a prompt there becomes a path")
	}
}

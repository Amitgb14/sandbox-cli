package agents

import (
	"slices"
	"strings"
	"testing"
)

// Cline's descriptor, pinned to what was seen rather than what is plausible.
//
// Every claim below was established by running the agent in a sandbox on
// 2026-08-24, because a descriptor is a promise that a *fleet* — which has no
// terminal and nobody watching — can start this agent and have it finish. The
// shape is unusual enough to be worth pinning: cline's non-interactive mode is a
// bare positional prompt, and its TUI is the opt-in, which is the inverse of
// claude's `-p`. Somebody porting the claude descriptor's habits onto it would
// produce an argv that opens a UI nobody is looking at.

func TestClineIsHeadlessWithABarePrompt(t *testing.T) {
	d, ok := Lookup("cline")
	if !ok {
		t.Fatal("no cline descriptor")
	}
	argv := d.Autonomous("write hello into out.txt", nil)

	// The prompt is a positional. Verified: one run wrote its file and exited 0
	// in sixteen seconds with nothing attached.
	if !slices.Contains(argv, "write hello into out.txt") {
		t.Errorf("the prompt is not in the argv: %v", argv)
	}
	// And the TUI must not be asked for. `-i` would open a terminal UI in a
	// container nobody is attached to, which does not fail — it hangs, holding a
	// max_parallel slot.
	for _, tui := range []string{"-i", "--tui"} {
		if slices.Contains(argv, tui) {
			t.Errorf("autonomous argv opens the TUI (%s): %v", tui, argv)
		}
	}
}

func TestClineAsksForApprovalExplicitly(t *testing.T) {
	d, _ := Lookup("cline")
	argv := strings.Join(d.Autonomous("do the thing", nil), " ")
	// `--auto-approve` defaults to true upstream, and this passes it anyway: a
	// default is a decision somebody else can revisit, and an unattended run that
	// starts asking does not fail, it hangs. Verified accepted in the same
	// session that verified the prompt.
	if !strings.Contains(argv, "--auto-approve true") {
		t.Errorf("approval is left to upstream's default: %s", argv)
	}
}

func TestClineDoesNotClaimToSeedAConsole(t *testing.T) {
	d, _ := Lookup("cline")
	// nil means Studio refuses to seed a console run rather than building an argv
	// that dies inside the container. Whether a positional reaches cline's TUI as
	// a first turn was not verified, and opencode is why that matters: it reads a
	// lone positional as the directory to open, so a Studio console run died with
	// "Failed to change directory to /workspace/review the code".
	if d.CanSeedConsole() {
		t.Error("cline claims a verified console prompt; nobody has verified one")
	}
}

func TestClineIsNotProbedForRouting(t *testing.T) {
	d, _ := Lookup("cline")
	// Empty for the reason opencode's is: cline drives several providers and its
	// default is its own, so no single host's silence means "this agent cannot
	// work". Routing reports it unprobed rather than down — guessing
	// api.anthropic.com would fail over an agent configured against OpenRouter
	// for an outage it never had.
	if d.ProviderHost != "" {
		t.Errorf("cline names a provider host (%q); it is provider-agnostic", d.ProviderHost)
	}
}

func TestClineForwardsNoPathValuedVariables(t *testing.T) {
	d, _ := Lookup("cline")
	// Cline has several path-valued variables and none may cross: each names a
	// host directory that is not mounted, and CLINE_DATA_DIR in particular would
	// move the login out of the persisted HOME — quietly costing the session on
	// every run.
	for _, name := range []string{
		"CLINE_DATA_DIR", "CLINE_SANDBOX_DATA_DIR", "CLINE_TEAM_DATA_DIR",
		"CLINE_TOOL_APPROVAL_DIR", "CLINE_LOG_PATH", "NODE_EXTRA_CA_CERTS",
	} {
		if slices.Contains(d.EnvAllow, name) {
			t.Errorf("EnvAllow carries %s, which points at a host path the container does not have", name)
		}
	}
	if !slices.Contains(d.EnvAllow, "CLINE_API_KEY") {
		t.Error("EnvAllow does not carry CLINE_API_KEY, so a key on the host cannot reach the agent")
	}
}

func TestClineConsoleAsksForItsUI(t *testing.T) {
	d, _ := Lookup("cline")
	argv := d.Console("", false)
	// Every other agent's bare argv *is* its UI, which is why Console used
	// Command alone until cline arrived. Here a bare invocation is the headless
	// mode: without `-i` a console run starts an agent with no prompt and no UI,
	// and the attached terminal watches a container print usage and exit.
	// Verified with a pty on 2026-08-24 — `cline -i` starts the full-screen UI.
	if !slices.Contains(argv, "-i") {
		t.Errorf("console argv does not ask for the TUI: %v", argv)
	}
	// And the headless argv must not: a TUI in a fleet is a UI nobody is
	// watching, which does not fail — it hangs.
	if slices.Contains(d.Autonomous("do the thing", nil), "-i") {
		t.Error("the autonomous argv opens the TUI")
	}
}

func TestOnlyClineNeedsAFlagToBeInteractive(t *testing.T) {
	// The field exists for one agent and should stay that way until another is
	// verified to need it. A descriptor that quietly acquires ConsoleArgs is
	// claiming its bare binary is not its UI, which is a fact about that agent
	// rather than a default worth inheriting.
	for _, name := range Names() {
		if name == "cline" {
			continue
		}
		d, _ := Lookup(name)
		if len(d.ConsoleArgs) != 0 {
			t.Errorf("%s declares ConsoleArgs %v; verify its bare argv is not already its UI", name, d.ConsoleArgs)
		}
	}
}

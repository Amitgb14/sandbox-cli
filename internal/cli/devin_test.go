package cli

import (
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
)

// Devin's adapter, and the line it deliberately does not cross.
//
// Devin CLI ships a headless mode (`devin -p PROMPT`) and an auto-approval flag
// (`--permission-mode bypass`), both documented by Cognition. Neither appears in
// a descriptor, because nobody here has an account to run one with — and
// internal/agents' rule is that a descriptor is a promise a *fleet* can keep, so
// it is earned by running the agent rather than by reading its documentation.
// These tests pin that the adapter ships useful and honest in the meantime.

func TestDevinIsAnAdapterWithoutADescriptor(t *testing.T) {
	var found bool
	for _, cmd := range agentCmds() {
		if strings.Fields(cmd.Use)[0] == "devin" {
			found = true
		}
	}
	if !found {
		t.Fatal("devin is not registered in agentCmds()")
	}
	// The other half of the claim: Studio, the fleet and both SDKs must not offer
	// it, because its headless mode is unverified. When somebody with an account
	// verifies `devin -p`, this test changes in the same commit as the descriptor.
	if _, ok := agents.Lookup("devin"); ok {
		t.Error("devin has a descriptor; a fleet may now name an agent whose headless mode nobody has run")
	}
}

func TestDevinForwardsNoHostPaths(t *testing.T) {
	// Every adapter here excludes its agent's path-valued variables: each names a
	// host directory the container has not got, and one pointing at the
	// credential store would move the login out of the persisted HOME and discard
	// it every run.
	for _, name := range devinEnvAllow {
		if strings.Contains(name, "PATH") || strings.Contains(name, "DIR") ||
			strings.Contains(name, "HOME") || strings.Contains(name, "FILE") {
			t.Errorf("devinEnvAllow carries %s, which looks like a host path", name)
		}
	}
}

func TestDevinAddsNoApprovalFlagOfItsOwn(t *testing.T) {
	// `--permission-mode bypass` is a decision about what an agent may do with
	// nobody watching. The wrapper forwards it if you type it and never adds it:
	// the CLI is where a person is present to make that call, which is the same
	// reason no wrapper adds --dangerously-skip-permissions.
	//
	// Asserted over the **rendered docker argv**, not over the install string.
	// The first version of this test inspected `devinInstall`, which could never
	// contain an approval flag — so it would have passed while somebody added one
	// to the guest argv or to an afterParse env, which is exactly where the other
	// adapters put such things (see cursor.go's NO_OPEN_BROWSER).
	line := renderDryRun(t, newDevinCmd(), nil)
	for _, forbidden := range []string{"permission-mode", "bypass", "--dangerously"} {
		if strings.Contains(line, forbidden) {
			t.Errorf("the rendered run carries an approval decision (%s):\n%s", forbidden, line)
		}
	}
	// And what a user types does reach the agent, which is the other half of
	// "forwards it if you type it".
	typed := renderDryRun(t, newDevinCmd(), []string{"--", "-p", "hi", "--permission-mode", "bypass"})
	if !strings.Contains(typed, "--permission-mode") {
		t.Errorf("a typed approval flag did not reach the agent:\n%s", typed)
	}
}

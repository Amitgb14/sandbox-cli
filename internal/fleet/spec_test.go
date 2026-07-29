package fleet

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeSpec(t *testing.T, body string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "fleet.yaml")
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoad(t *testing.T) {
	p := writeSpec(t, `
agent: claude
max_parallel: 2
defaults:
  memory: 8g
  allow: [example.com]
  cache: true
tasks:
  - branch: feature-a
    prompt: implement the login form
  - branch: feature-b
    prompt: add rate limiting
    args: ["--model", "opus"]
`)
	s, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Agent != "claude" || s.MaxParallel != 2 || len(s.Tasks) != 2 {
		t.Fatalf("unexpected spec: %+v", s)
	}
	if s.Defaults.Memory != "8g" {
		t.Errorf("memory = %q, want the file's 8g", s.Defaults.Memory)
	}
	// Unset caps fall back to the built-in finite default, not to unlimited.
	if s.Defaults.CPUs != defaultCPUs {
		t.Errorf("cpus = %q, want default %q", s.Defaults.CPUs, defaultCPUs)
	}
	if got := s.Tasks[1].Args; len(got) != 2 || got[0] != "--model" {
		t.Errorf("task args not parsed: %v", got)
	}
}

// "0" is how a fleet file opts out of a resource cap; it must reach sandbox-cli
// as "unset" rather than being passed to docker as a literal zero.
func TestLoadUncapped(t *testing.T) {
	p := writeSpec(t, `
agent: claude
defaults:
  memory: "0"
  cpus: "0"
tasks:
  - {branch: a, prompt: do a}
`)
	s, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Defaults.Memory != "" || s.Defaults.CPUs != "" {
		t.Errorf("explicit 0 should mean unlimited, got memory=%q cpus=%q", s.Defaults.Memory, s.Defaults.CPUs)
	}
}

// A typo in a fleet file must fail loudly: silently ignoring it would run the
// fleet without settings the author believed they had set.
func TestLoadRejectsUnknownKeys(t *testing.T) {
	p := writeSpec(t, `
agent: claude
defaults:
  memoryy: 8g
tasks:
  - {branch: a, prompt: do a}
`)
	if _, err := Load(p); err == nil {
		t.Fatal("expected an error for an unknown key")
	}
}

func TestValidate(t *testing.T) {
	ok := Spec{Agent: "claude", Tasks: []Task{{Branch: "a", Prompt: "do a"}}}
	if err := ok.Validate(); err != nil {
		t.Fatalf("valid spec rejected: %v", err)
	}

	cases := map[string]Spec{
		"no agent":       {Tasks: []Task{{Branch: "a", Prompt: "p"}}},
		"unknown agent":  {Agent: "gpt5", Tasks: []Task{{Branch: "a", Prompt: "p"}}},
		"no tasks":       {Agent: "claude"},
		"blank branch":   {Agent: "claude", Tasks: []Task{{Branch: "  ", Prompt: "p"}}},
		"blank prompt":   {Agent: "claude", Tasks: []Task{{Branch: "a", Prompt: " "}}},
		"negative limit": {Agent: "claude", MaxParallel: -1, Tasks: []Task{{Branch: "a", Prompt: "p"}}},
	}
	for name, s := range cases {
		if err := s.Validate(); err == nil {
			t.Errorf("%s: expected an error", name)
		}
	}
}

// Two tasks on one branch is two agents in one checkout: they overwrite each
// other's edits with no warning and no recovery. It has to fail at parse time.
func TestValidateRejectsDuplicateBranch(t *testing.T) {
	s := Spec{Agent: "claude", Tasks: []Task{
		{Branch: "feature-a", Prompt: "one"},
		{Branch: "feature-a", Prompt: "two"},
	}}
	err := s.Validate()
	if err == nil {
		t.Fatal("expected duplicate branches to be rejected")
	}
	if !strings.Contains(err.Error(), "feature-a") {
		t.Errorf("error should name the branch, got: %v", err)
	}
}

func TestUnknownAgentErrorListsKnownOnes(t *testing.T) {
	err := Spec{Agent: "nope", Tasks: []Task{{Branch: "a", Prompt: "p"}}}.Validate()
	if err == nil || !strings.Contains(err.Error(), "claude") {
		t.Errorf("error should list the known agents, got: %v", err)
	}
}

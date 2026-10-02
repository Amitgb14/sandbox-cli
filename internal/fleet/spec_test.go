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

// A fleet that puts a different agent on each branch is the point of the
// per-task key: two agents, one repository, one command, and a `fleet status`
// that says which is which.
func TestLoadMixedAgents(t *testing.T) {
	p := writeSpec(t, `
agent: claude
tasks:
  - branch: feature-a
    prompt: implement it with claude
  - branch: feature-b
    agent: codex
    prompt: implement it with codex
    memory: 8g
    cpus: "4"
    allow: [docs.example.com]
`)
	s, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if got := s.AgentFor(s.Tasks[0]); got != "claude" {
		t.Errorf("task without its own agent should inherit the fleet's, got %q", got)
	}
	if got := s.AgentFor(s.Tasks[1]); got != "codex" {
		t.Errorf("task agent should win over the fleet's, got %q", got)
	}
	if got := s.Agents(); len(got) != 2 || got[0] != "claude" || got[1] != "codex" {
		t.Errorf("Agents() = %v, want the two sorted", got)
	}
}

// The fleet-wide agent becomes optional once every task names one, because
// requiring a default nobody uses is a line of the file that means nothing.
func TestFleetAgentIsOptionalWhenEveryTaskNamesOne(t *testing.T) {
	s := Spec{Tasks: []Task{
		{Branch: "a", Prompt: "p", Agent: "claude"},
		{Branch: "b", Prompt: "p", Agent: "codex"},
	}}
	if err := s.Validate(); err != nil {
		t.Errorf("a fleet where every task names its agent should be valid: %v", err)
	}
}

// The task with neither is the case worth an error of its own: "agent is
// required" is true but does not say where it was supposed to go, now that the
// key can appear in two places.
func TestTaskWithNoAgentAnywhereIsRejected(t *testing.T) {
	s := Spec{Tasks: []Task{
		{Branch: "a", Prompt: "p", Agent: "claude"},
		{Branch: "b", Prompt: "p"},
	}}
	err := s.Validate()
	if err == nil {
		t.Fatal("expected a refusal for the task with no agent")
	}
	if !strings.Contains(err.Error(), "b") || !strings.Contains(err.Error(), "agent") {
		t.Errorf("error should name the branch and the missing key, got: %v", err)
	}
}

func TestUnknownTaskAgentIsRejected(t *testing.T) {
	s := Spec{Agent: "claude", Tasks: []Task{{Branch: "a", Prompt: "p", Agent: "grok"}}}
	err := s.Validate()
	if err == nil || !strings.Contains(err.Error(), "grok") {
		t.Errorf("error should name the unknown agent, got: %v", err)
	}
}

func TestLimitsFor(t *testing.T) {
	s := Spec{
		Agent:    "claude",
		Defaults: Defaults{Memory: "4g", CPUs: "2", Allow: []string{"example.com"}},
		Tasks: []Task{
			{Branch: "plain", Prompt: "p"},
			{Branch: "bigger", Prompt: "p", Memory: "16g", Allow: []string{"docs.example.com", "example.com"}},
			{Branch: "uncapped", Prompt: "p", Memory: "0", CPUs: "0"},
		},
	}

	if got := s.LimitsFor(s.Tasks[0]); got.Memory != "4g" || got.CPUs != "2" {
		t.Errorf("a task that overrides nothing gets the defaults, got %+v", got)
	}
	big := s.LimitsFor(s.Tasks[1])
	if big.Memory != "16g" {
		t.Errorf("task memory should replace the default, got %q", big.Memory)
	}
	if big.CPUs != "2" {
		t.Errorf("an unset task cap still inherits, got %q", big.CPUs)
	}
	// Union, deduped, defaults first — allow adds to the fleet's list rather than
	// replacing it, so a task cannot quietly ask for a narrower allowlist than the
	// file's author wrote one line above.
	if len(big.Allow) != 2 || big.Allow[0] != "example.com" || big.Allow[1] != "docs.example.com" {
		t.Errorf("task allow should extend the fleet's, deduped, got %v", big.Allow)
	}
	// "0" is the opt-out at the task level too, and must reach sandbox-cli as
	// "unset" rather than as a literal zero passed to docker.
	if got := s.LimitsFor(s.Tasks[2]); got.Memory != "" || got.CPUs != "" {
		t.Errorf(`task "0" should mean unlimited, got %+v`, got)
	}
}

func TestConcurrency(t *testing.T) {
	three := []Task{{Branch: "a"}, {Branch: "b"}, {Branch: "c"}}
	cases := []struct {
		max, want int
	}{
		{0, 3},  // unset: everything at once
		{2, 2},  // the cap
		{10, 3}, // a cap above the task count cannot make more agents than there are tasks
	}
	for _, c := range cases {
		if got := (Spec{MaxParallel: c.max, Tasks: three}).Concurrency(); got != c.want {
			t.Errorf("max_parallel %d: concurrency = %d, want %d", c.max, got, c.want)
		}
	}
}

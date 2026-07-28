// Package fleet runs several agents at once: one detached container per git
// branch, launched from a single task file, then supervised and landed by branch
// name.
//
// It owns no isolation policy of its own. Every task is turned into the same
// sandbox.Options a `sandbox-cli claude --worktree BRANCH` run would produce,
// with Detach set — so a fleet agent is confined exactly like an interactive one,
// and any change to the sandbox boundary applies to both without fleet knowing.
package fleet

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
)

// Spec is a parsed fleet.yaml.
type Spec struct {
	// Agent names the agent to run for every task ("claude", "codex").
	Agent string `yaml:"agent"`

	// MaxParallel caps how many agents run at the same time. Zero (the default)
	// launches every task at once and returns immediately. A non-zero value below
	// the task count means `fleet run` must stay attached to fill slots as
	// containers exit — see Runner.Launch.
	MaxParallel int `yaml:"max_parallel"`

	Defaults Defaults `yaml:"defaults"`
	Tasks    []Task   `yaml:"tasks"`
}

// Defaults are sandbox options applied to every task in the fleet.
type Defaults struct {
	// Memory and CPUs cap each container (docker --memory / --cpus). They default
	// to a finite value rather than sandbox-cli's usual "unlimited", because the
	// whole point of a fleet is running several agents at once and N unbounded
	// containers will take the host down. Set them to "0" to opt out deliberately.
	Memory string `yaml:"memory"`
	CPUs   string `yaml:"cpus"`

	// Allow enables the egress allowlist and permits these domains on top of the
	// built-in baseline.
	Allow []string `yaml:"allow"`

	// Cache persists package-manager caches in named volumes across runs, which
	// matters more in a fleet: without it every agent re-downloads the same
	// dependencies into its own throwaway container.
	Cache bool `yaml:"cache"`

	// Git forwards the host git identity so the agent's commits are attributed to
	// you rather than to nobody.
	Git bool `yaml:"git"`
}

// Task is one agent working one branch.
type Task struct {
	// Branch is the git branch (and worktree) this agent works on. It is also the
	// task's identity for status, logs and landing, so it must be unique.
	Branch string `yaml:"branch"`

	// Prompt is what the agent is asked to do, passed as a single argument.
	Prompt string `yaml:"prompt"`

	// Args are extra arguments appended after the agent's own autonomous flags,
	// so a task can override them (e.g. ["--model", "opus"]).
	Args []string `yaml:"args"`

	// Verify is a shell command run inside the container after the agent, whose
	// exit code decides whether the task is done. It is the difference between
	// headless and autonomous: without one, a task that exits is a task that
	// *ran*, and nothing anywhere has said the work is right. A task with no
	// verify still runs — this is a fleet of agents, not a CI system — but
	// `fleet land` will say plainly that it is landing unchecked work.
	Verify string `yaml:"verify"`
}

// defaultMemory and defaultCPUs bound a single fleet agent. Chosen to let a
// typical laptop run three or four agents without swapping; a task that needs
// more says so explicitly.
const (
	defaultMemory = "4g"
	defaultCPUs   = "2"
)

// Load reads and validates a fleet.yaml.
func Load(path string) (Spec, error) {
	f, err := os.Open(path)
	if err != nil {
		return Spec{}, fmt.Errorf("reading fleet file: %w", err)
	}
	defer f.Close()

	var s Spec
	dec := yaml.NewDecoder(f)
	// Reject unknown keys. A typo in a fleet file would otherwise be silently
	// ignored and the fleet would run with settings the author believed they had
	// set — including, potentially, resource caps.
	dec.KnownFields(true)
	if err := dec.Decode(&s); err != nil {
		return Spec{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	s.applyDefaults()
	if err := s.Validate(); err != nil {
		return Spec{}, fmt.Errorf("%s: %w", path, err)
	}
	return s, nil
}

// applyDefaults fills in the values a fleet file may omit.
func (s *Spec) applyDefaults() {
	if s.Defaults.Memory == "" {
		s.Defaults.Memory = defaultMemory
	}
	if s.Defaults.CPUs == "" {
		s.Defaults.CPUs = defaultCPUs
	}
	// "0" is the explicit opt-out of a cap; translate it to sandbox-cli's own
	// "unset means unlimited" representation.
	if s.Defaults.Memory == "0" {
		s.Defaults.Memory = ""
	}
	if s.Defaults.CPUs == "0" {
		s.Defaults.CPUs = ""
	}
}

// Validate reports the first problem that would make this fleet unrunnable.
func (s Spec) Validate() error {
	if s.Agent == "" {
		return fmt.Errorf("agent is required (one of: %s)", strings.Join(agents.Names(), ", "))
	}
	if _, ok := agents.Lookup(s.Agent); !ok {
		return fmt.Errorf("unknown agent %q (known: %s)", s.Agent, strings.Join(agents.Names(), ", "))
	}
	if len(s.Tasks) == 0 {
		return fmt.Errorf("no tasks: a fleet needs at least one")
	}
	if s.MaxParallel < 0 {
		return fmt.Errorf("max_parallel must not be negative, got %d", s.MaxParallel)
	}
	seen := map[string]bool{}
	for i, t := range s.Tasks {
		if strings.TrimSpace(t.Branch) == "" {
			return fmt.Errorf("tasks[%d]: branch is required", i)
		}
		if strings.TrimSpace(t.Prompt) == "" {
			// A detached agent with no instructions burns a container doing nothing.
			return fmt.Errorf("tasks[%d] (%s): prompt is required", i, t.Branch)
		}
		if seen[t.Branch] {
			// Two tasks on one branch means two agents in one worktree: silent,
			// unrecoverable data loss as they overwrite each other's edits.
			return fmt.Errorf("tasks[%d]: duplicate branch %q; one agent per branch", i, t.Branch)
		}
		seen[t.Branch] = true
	}
	return nil
}

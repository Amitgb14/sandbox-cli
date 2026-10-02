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
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
)

// Spec is a parsed fleet.yaml.
type Spec struct {
	// Agent names the agent to run for tasks that do not name one of their own
	// ("claude", "codex"). It may be omitted entirely when every task does.
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

	// Agent overrides the fleet-wide `agent:` for this task, so one fleet can put
	// Claude on one branch and Codex on another and compare what comes back.
	//
	// Mixing agents costs nothing at the boundary — the descriptor decides the
	// container argv and the forwarded variable *names*, and every task is
	// confined by the same sandbox.Options either way — but it does mean each
	// agent named here must be logged in (or have its key exported) before the
	// fleet runs, because none of them can answer a login prompt from a detached
	// container.
	Agent string `yaml:"agent"`

	// Args are extra arguments appended after the agent's own autonomous flags,
	// so a task can override them (e.g. ["--model", "opus"]).
	//
	// They are the agent's own flags, so a task that overrides the fleet's agent
	// almost certainly wants its own args too: `--model opus` means nothing to
	// codex.
	Args []string `yaml:"args"`

	// Memory, CPUs and Allow override `defaults:` for this one task — the branch
	// that needs a bigger build, or one more domain, without raising the caps for
	// every other agent in the fleet.
	//
	// Memory and CPUs *replace* the default ("0" means uncapped, as at the fleet
	// level). Allow *adds* to it: the fleet-wide list is the set of domains every
	// agent here needs, and a task subtracting from it would be asking for a
	// weaker allowlist than the file's author wrote one line above. Ask for less
	// egress by moving the domain onto the tasks that need it, not by taking it
	// away from one.
	Memory string   `yaml:"memory"`
	CPUs   string   `yaml:"cpus"`
	Allow  []string `yaml:"allow"`

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
//
// # Where this file sits on the trust boundary
//
// A fleet file carries privilege-relevant settings: `allow` (fleet-wide or on a
// task) widens the egress allowlist, `defaults.git` forwards the host git
// identity, and `memory`/`cpus` override the caps a profile set. `internal/config`
// refuses exactly that class of key from a project `.sandbox.yaml`, on the
// grounds that a repository is untrusted input and discovery is not a deliberate
// act. `-f` defaults to ./fleet.yaml, so this file *is* discovered from the
// repository — and the difference has to be argued rather than inherited from a
// flag default.
//
// The argument: running `fleet run` at all is the deliberate act. Nobody types it
// by walking into a directory; it launches agents that will edit the repository
// autonomously, which is a larger decision than any single key in here. A
// `.sandbox.yaml` by contrast is consulted by every ordinary `sandbox-cli run`,
// where the user's intent was to run one command in one project and a planted
// file would change what that meant. So this file has **CLI-flag trust**: it may
// say anything a flag may say, because reaching it required the same kind of act.
//
// Two consequences to keep true if this changes:
//
//   - It must stay *named*, never *discovered upward*. `-f` looks in the current
//     directory only; no walk to the repository root, and no `~/.config` fallback.
//     A file found by searching is a file someone else can place.
//   - It must not become a way to weaken the profile. There is no `profile:` key
//     here on purpose: the profile comes from `--profile` or the user's own
//     config, which is where `config.trust` can enforce that a project may raise
//     it and never lower it.
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
	if s.Agent != "" {
		if _, ok := agents.Lookup(s.Agent); !ok {
			return fmt.Errorf("unknown agent %q (known: %s)", s.Agent, strings.Join(agents.Names(), ", "))
		}
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

		// Resolved per task rather than once for the file, because `agent:` is now
		// a default that a task may replace. A task with neither is the one case
		// worth spelling out separately: "agent is required" is true, but it is not
		// obvious *where* it was supposed to go once the key can appear twice.
		switch name := s.AgentFor(t); {
		case name == "":
			return fmt.Errorf("tasks[%d] (%s): no agent; set a fleet-wide `agent:` or give this task its own (one of: %s)",
				i, t.Branch, strings.Join(agents.Names(), ", "))
		default:
			if _, ok := agents.Lookup(name); !ok {
				return fmt.Errorf("tasks[%d] (%s): unknown agent %q (known: %s)",
					i, t.Branch, name, strings.Join(agents.Names(), ", "))
			}
		}
	}
	return nil
}

// AgentFor is the agent this task runs: its own if it named one, the fleet's
// otherwise.
func (s Spec) AgentFor(t Task) string {
	if a := strings.TrimSpace(t.Agent); a != "" {
		return a
	}
	return s.Agent
}

// Limits are one task's resolved resource and egress settings — what `defaults:`
// said, with the task's own overrides folded in.
//
// Resolved in one place because two callers need the identical answer for
// different reasons: Launch builds the container from it, and Plan prints it.
// A dry run that disagreed with the run it describes would be worse than no dry
// run at all.
type Limits struct {
	Memory string
	CPUs   string
	Allow  []string
}

// LimitsFor resolves one task's limits against the fleet defaults.
func (s Spec) LimitsFor(t Task) Limits {
	lim := Limits{
		Memory: pickLimit(t.Memory, s.Defaults.Memory),
		CPUs:   pickLimit(t.CPUs, s.Defaults.CPUs),
	}
	// Union, not replacement, and deduped so a domain named in both places is not
	// passed to the firewall twice. Order is defaults-then-task so the printed
	// plan reads the way the file does.
	seen := map[string]bool{}
	for _, d := range append(append([]string{}, s.Defaults.Allow...), t.Allow...) {
		d = strings.TrimSpace(d)
		if d == "" || seen[d] {
			continue
		}
		seen[d] = true
		lim.Allow = append(lim.Allow, d)
	}
	return lim
}

// pickLimit applies a task's override to a fleet-wide cap. Empty means "the task
// said nothing"; "0" is the same explicit opt-out it is at the fleet level, and
// has to be translated here too — applyDefaults only ever sees the `defaults:`
// block.
func pickLimit(task, fleet string) string {
	if task == "" {
		return fleet
	}
	if task == "0" {
		return ""
	}
	return task
}

// Concurrency is how many of this fleet's agents can be running at once: the
// max_parallel cap, or the task count when it is unset or higher.
func (s Spec) Concurrency() int {
	if s.MaxParallel <= 0 || s.MaxParallel > len(s.Tasks) {
		return len(s.Tasks)
	}
	return s.MaxParallel
}

// Agents lists the distinct agents this fleet will start, in sorted order — for
// the messages that have to say what a mixed fleet needs logged in.
func (s Spec) Agents() []string {
	seen := map[string]bool{}
	var out []string
	for _, t := range s.Tasks {
		if a := s.AgentFor(t); a != "" && !seen[a] {
			seen[a] = true
			out = append(out, a)
		}
	}
	sort.Strings(out)
	return out
}

// Package agents describes the AI coding agents sandbox-cli knows how to start,
// as data rather than as code duplicated per subcommand.
//
// Each agent needs the same four things known in two different places: the
// interactive `sandbox-cli claude` / `sandbox-cli codex` wrappers, and the
// headless fleet runner that launches many agents at once. Keeping that
// knowledge in one table is what stops the two paths from drifting — a fleet run
// must forward the same environment and persist the same login as the wrapper,
// or an agent that works interactively fails detached for reasons nobody can see.
//
// Deliberately *not* here: anything that touches the host. Mounting the Claude
// status line settings or the host's project history produces host paths and
// belongs to the wrapper (internal/cli/claude.go); a Descriptor only says what
// runs inside the container and which host env var names may cross the boundary.
package agents

import "sort"

// Descriptor is everything sandbox-cli needs to know to start one agent.
type Descriptor struct {
	// Name is the subcommand and the fleet `agent:` value, e.g. "claude".
	Name string

	// PersistDir names the sandbox-owned host directory that holds this agent's
	// login across ephemeral containers (~/.config/sandbox/agents/<PersistDir>,
	// see config.AgentStateDir). Separate from Name only so a rename of the
	// subcommand cannot silently orphan an existing login.
	PersistDir string

	// EnvAllow lists host environment variable names forwarded into the container
	// *only if set on the host*. Suggested, opt-in, and deliberately narrow:
	// nothing else about the host environment crosses the boundary.
	EnvAllow []string

	// Command is the container argv that starts the agent, to which caller
	// arguments are appended. It may be a shell bootstrap rather than the bare
	// binary (see claudeBootstrap).
	Command []string

	// AutonomousArgs returns the arguments that make the agent run prompt to
	// completion without ever asking a human anything.
	//
	// This is the contract that makes detached runs possible at all: a fleet
	// container has no terminal attached, so an agent that stops to ask for
	// permission does not fail — it hangs until someone kills it. Every
	// descriptor must therefore return a genuinely non-interactive argv, which
	// for most agents means opting out of their approval prompts. Detached and
	// autonomous are the same decision; see docs/GUIDE.md.
	AutonomousArgs func(prompt string) []string
}

// Autonomous returns the full container argv for a headless run of prompt.
// extra is appended last so a caller can override the agent's own defaults
// (e.g. a fleet task's `args:`).
func (d Descriptor) Autonomous(prompt string, extra []string) []string {
	return concat(d.Command, d.AutonomousArgs(prompt), extra)
}

// ClaudeBootstrap ensures a self-updating Claude install exists in the persisted
// HOME (~/.local/bin, installed via the native installer on first run) and execs
// it. The baked npm copy in /usr/local/bin is the offline fallback. Because the
// persisted install is user-writable, Claude Code keeps itself up to date across
// runs — the baked copy could not (root-owned).
//
// PATH is **appended** to, never prepended. That HOME is writable by the agent
// and shared by every session using this adapter, so a prepend would let a
// planted $HOME/.local/bin/git shadow the image's for every later run — the same
// shape as the root-phase hazard in CLAUDE.md, one privilege drop later. The
// wanted binary is reached by absolute path instead, which needs no PATH
// precedence at all.
const ClaudeBootstrap = `export PATH="$PATH:$HOME/.local/bin"
if [ ! -x "$HOME/.local/bin/claude" ]; then
  command -v curl >/dev/null 2>&1 && curl -fsSL https://claude.ai/install.sh | bash >/dev/null 2>&1 || true
fi
if [ -x "$HOME/.local/bin/claude" ]; then
  exec "$HOME/.local/bin/claude" "$@"
fi
exec claude "$@"`

// registry is the set of known agents, keyed by Name.
var registry = map[string]Descriptor{
	"claude": {
		Name:       "claude",
		PersistDir: "claude",
		EnvAllow: []string{
			"ANTHROPIC_API_KEY",
			"ANTHROPIC_AUTH_TOKEN",
			"ANTHROPIC_BASE_URL",
			"CLAUDE_CODE_USE_BEDROCK",
			"CLAUDE_CODE_USE_VERTEX",
		},
		// The trailing "claude" is $0 for the bootstrap shell, so the guest args
		// land in "$@" and reach the real binary.
		Command: []string{"sh", "-c", ClaudeBootstrap, "claude"},
		AutonomousArgs: func(prompt string) []string {
			// -p is Claude Code's headless "print" mode: run the prompt and exit.
			// Skipping permissions is what keeps it from blocking on a prompt no
			// one can answer — safe here precisely because the container is the
			// blast-radius boundary.
			return []string{"-p", prompt, "--dangerously-skip-permissions"}
		},
	},
	"codex": {
		Name:       "codex",
		PersistDir: "codex",
		EnvAllow: []string{
			"OPENAI_API_KEY",
			"OPENAI_BASE_URL",
			"CODEX_HOME",
		},
		Command: []string{"codex"},
		AutonomousArgs: func(prompt string) []string {
			// `codex exec` is Codex CLI's non-interactive subcommand. Codex applies
			// its own approval policy on top; a task that needs it relaxed passes
			// the relevant flag through the fleet task's `args:` rather than having
			// sandbox-cli guess at flag names that change between releases.
			return []string{"exec", prompt}
		},
	},
}

// Lookup returns the descriptor for name.
func Lookup(name string) (Descriptor, bool) {
	d, ok := registry[name]
	return d, ok
}

// Names returns the known agent names in sorted order, for help text and for
// error messages that have to list the valid choices.
func Names() []string {
	out := make([]string, 0, len(registry))
	for n := range registry {
		out = append(out, n)
	}
	sort.Strings(out)
	return out
}

// concat joins argv fragments into a fresh slice, so a Descriptor's Command is
// never aliased into (and then appended onto) a caller's slice.
func concat(parts ...[]string) []string {
	n := 0
	for _, p := range parts {
		n += len(p)
	}
	out := make([]string, 0, n)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

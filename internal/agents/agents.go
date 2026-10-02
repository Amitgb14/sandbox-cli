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

	// Env are NAME=VALUE settings sandbox-cli itself puts in the container, as
	// opposed to EnvAllow's "forward the host's value if it has one". They exist
	// for agents that behave wrongly in a container unless told something about it
	// — a keyring that is not there, a browser that cannot open — and the values
	// are constants compiled in here, never anything read from the host.
	//
	// In the descriptor rather than in the wrapper because a fleet gets no
	// wrapper: a setting left behind there — a keyring the container has no daemon
	// for is the standing example — is an agent that logs in every run and,
	// unattended, cannot.
	//
	// No agent in the table sets it today; droid, which did, was removed. The
	// field stays because the wiring behind it does: four call sites carry it to a
	// container, and the goose, cursor and qwen wrappers do the same job in the
	// place a fleet cannot reach, so the next agent needing it needs the mechanism
	// and not a rediscovery of why it exists.
	Env []string

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

	// ConsoleArgs are the arguments that make the agent start its interactive UI.
	//
	// Empty for almost every agent, because the bare binary *is* the UI — which is
	// why Console used Command alone until cline arrived. Cline inverts it: a bare
	// invocation is the headless mode and the TUI is opt-in behind `-i`, so
	// without this a console run started an agent with no prompt and no UI, and
	// the attached terminal got a container that printed usage and exited.
	//
	// A separate field rather than a second Command, because everything else about
	// starting the agent — the bootstrap, the install, the PATH — is identical in
	// both modes, and two argvs differing by one flag drift.
	ConsoleArgs []string

	// SkipPermissionArgs turns off the agent's approval prompts.
	//
	// Held apart from AutonomousArgs, and appended by Autonomous, so the flag
	// has exactly one definition. It is needed in two places — a headless run,
	// which hangs forever on a question nobody can answer, and an interactive
	// run somebody deliberately wants to leave alone — and writing it twice is
	// how a security-relevant argv drifts.
	//
	// Empty for the agents whose non-interactive mode is a *subcommand* rather
	// than a flag (`codex exec`, `opencode run`): there is nothing
	// to add to an interactive session, so those simply cannot be launched this
	// way, which is the honest answer rather than a flag that does nothing.
	SkipPermissionArgs []string

	// ProviderHost is the host this agent talks to when it works, e.g.
	// "api.anthropic.com". Routing probes it before choosing this agent — "is the
	// thing that answers actually answering" — and it lives in the descriptor for
	// the same reason EnvAllow does: the alternative is a second table somewhere
	// else that a new adapter can be added without.
	//
	// Empty means **do not probe**, and that is the honest answer for two kinds
	// of agent rather than an omission. `opencode` is provider-agnostic — its
	// EnvAllow spans four vendors because the user picks — so no single host's
	// health says anything about it. And an agent pointed at a proxy or a
	// self-hosted base URL (ANTHROPIC_BASE_URL and OPENAI_BASE_URL are both
	// forwardable) is not talking to the vendor at all, so the vendor's health is
	// not evidence about it either. An unprobeable agent still works in a chain;
	// it simply cannot be skipped in advance, only failed over from.
	ProviderHost string

	// ConsolePromptArgs seeds an *interactive* session's first turn, or is nil
	// when this agent has no way to be seeded.
	//
	// It exists because "append the prompt as a positional" is not a general
	// truth, and assuming it was cost a real run: opencode reads a lone
	// positional as **the project directory to open**, so a console run carrying
	// a prompt became `opencode "review the code"` and died with "Failed to
	// change directory to /workspace/review the code". The prompt was never a
	// prompt to it.
	//
	// Nil is therefore a first-class answer and means *do not seed* — the same
	// shape SkipPermissionArgs uses for agents with no such flag. A caller that
	// wants a prompt honoured by such an agent wants a headless run, where the
	// descriptor's AutonomousArgs spells it correctly (`run <prompt>`), and
	// should be told so rather than handed an argv that fails inside the
	// container.
	//
	// Only claude's positional is *verified* here — it is the agent the console
	// feature was built and attached against. codex and gemini keep the
	// behaviour they had, marked unverified, because changing them on a hunch
	// would regress runs that may be working; opencode is the one this was
	// written for.
	ConsolePromptArgs func(prompt string) []string
}

// Autonomous returns the full container argv for a headless run of prompt.
// extra is appended last so a caller can override the agent's own defaults
// (e.g. a fleet task's `args:`).
func (d Descriptor) Autonomous(prompt string, extra []string) []string {
	return concat(d.Command, d.AutonomousArgs(prompt), d.SkipPermissionArgs, extra)
}

// Console returns the container argv for an *interactive* run: the agent's
// normal UI, with prompt seeding the first turn when there is one.
//
// skipPermissions is a deliberate choice rather than a default. An interactive
// session is where being asked is the point — somebody is attached and can
// answer — so the caller opts in when it wants the agent to keep going instead.
// Requesting it from an agent that has no such flag returns the argv unchanged;
// the caller is expected to have refused already, and quietly doing something
// other than what was asked is worse than doing nothing.
func (d Descriptor) Console(prompt string, skipPermissions bool) []string {
	// ConsoleArgs first: where an agent has any, they are what selects the UI at
	// all, and a flag that changes the mode belongs before ones that configure it.
	argv := concat(d.Command, d.ConsoleArgs)
	if skipPermissions {
		argv = concat(argv, d.SkipPermissionArgs)
	}
	// Seeded only where the descriptor says how. An agent with no
	// ConsolePromptArgs cannot be given a first turn on the command line, and
	// appending the prompt anyway is what produced a chdir error instead of a
	// conversation — see the field's comment. CanSeedConsole is what a caller
	// checks to refuse the combination up front.
	if prompt != "" && d.ConsolePromptArgs != nil {
		argv = concat(argv, d.ConsolePromptArgs(prompt))
	}
	return argv
}

// CanSeedConsole reports whether an interactive run of this agent can be given
// its first turn as an argument.
func (d Descriptor) CanSeedConsole() bool { return d.ConsolePromptArgs != nil }

// CanSkipPermissions reports whether this agent has a flag for it.
func (d Descriptor) CanSkipPermissions() bool { return len(d.SkipPermissionArgs) > 0 }

// Invocation is the same run written the way a person would type it: the agent's
// name and its arguments, without the shell bootstrap that finds the binary.
//
// For display only — `fleet run --dry-run` reports what a task will *do*, and an
// eight-line install script pasted in front of every prompt buries exactly the
// two things the reader is checking. Never use it to start anything: the
// bootstrap it omits is what makes the agent exist in the container.
func (d Descriptor) Invocation(prompt string, extra []string) []string {
	return concat([]string{d.Name}, d.AutonomousArgs(prompt), extra)
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
//
// **It says what it is doing, and it is bounded.** This used to run the installer
// with both streams sent to /dev/null and no timeout, which made the first run of
// the flagship agent a multi-minute silence — the download is a whole Claude Code
// binary. Reported as a hang, and reasonably: it is indistinguishable from one.
// Worse, interrupting it leaves nothing behind, so the next run started over and
// the "first run only" cost became permanent.
//
// Three things follow, and the middle one is the reason the other bootstraps
// (bootstrap.go) could stay quiet while this one cannot: they install in seconds
// from a registry, this fetches a large release binary.
//
//   - It announces itself in the same words as every other agent's install.
//   - The installer's own output is kept, on **stderr**. Never stdout: `claude -p`
//     writes the answer there and a fleet's verify reads it, so a chatty install
//     would corrupt the one thing the run exists to produce.
//   - It is bounded, and the two bounds answer different questions. A host that
//     does not answer at all is caught by `--connect-timeout` in seconds. A host
//     that accepts and then stalls is caught by curl's `--max-time` (120s, for a
//     small script) and by `timeout` (900s, for the install itself) — so that
//     worst case is minutes, not seconds. Deliberately generous: a slow link is
//     not an error, and killing a download that would have finished leaves the
//     user worse off than waiting for it.
//
// Failure is still not fatal — `|| true` in spirit — because the baked copy works.
// But it now says so, and says what to allow under an egress allowlist, since
// `claude.ai` and `downloads.claude.ai` are not in the baseline and so this
// install fails silently on every run for anyone using the built-in default.
const ClaudeBootstrap = `export PATH="$PATH:$HOME/.local/bin"
if [ ! -x "$HOME/.local/bin/claude" ] && command -v curl >/dev/null 2>&1; then
  echo "sandbox-cli: installing the self-updating claude into the sandbox agent home (first run only; this downloads a release binary)..." >&2
  bound=""
  command -v timeout >/dev/null 2>&1 && bound="timeout 900"
  installer="$(mktemp)"
  if curl -fsSL --connect-timeout 15 --max-time 120 -o "$installer" https://claude.ai/install.sh; then
    $bound bash "$installer" >&2 || true
  fi
  rm -f "$installer"
  # Judged on the outcome, not on exit codes. A vendor script that returns 0
  # without leaving a binary behind would otherwise pass silently — and "it said
  # nothing and nothing happened" is the failure this whole block exists to end.
  if [ ! -x "$HOME/.local/bin/claude" ]; then
    echo "sandbox-cli: that install did not finish — continuing with the copy baked into the image, which cannot update itself." >&2
    echo "sandbox-cli: re-run to retry; with an egress allowlist it needs --allow claude.ai --allow downloads.claude.ai." >&2
  fi
fi
if [ -x "$HOME/.local/bin/claude" ]; then
  exec "$HOME/.local/bin/claude" "$@"
fi
exec claude "$@"`

// registry is the set of known agents, keyed by Name.
var registry = map[string]Descriptor{
	"claude": {
		Name: "claude",
		// Verified: the console feature was built and attached against this agent.
		ConsolePromptArgs: func(prompt string) []string { return []string{prompt} },
		PersistDir:        "claude",
		ProviderHost:      "api.anthropic.com",
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
			// The permission flag is SkipPermissionArgs below, appended by
			// Autonomous — one definition, because an interactive run needs the
			// same flag and a second copy is how the two drift.
			return []string{"-p", prompt}
		},
		// Safe here precisely because the container is the blast-radius boundary.
		SkipPermissionArgs: []string{"--dangerously-skip-permissions"},
	},
	"codex": {
		Name: "codex",
		// Unverified, and kept as it was: changing it on a hunch would regress
		// runs that may be working today.
		ConsolePromptArgs: func(prompt string) []string { return []string{prompt} },
		PersistDir:        "codex",
		ProviderHost:      "api.openai.com",
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
	"gemini": {
		Name: "gemini",
		// Unverified, and kept as it was.
		ConsolePromptArgs: func(prompt string) []string { return []string{prompt} },
		PersistDir:        "gemini",
		ProviderHost:      "generativelanguage.googleapis.com",
		// GOOGLE_APPLICATION_CREDENTIALS is deliberately absent: it names a host
		// file path that is not mounted, so forwarding it would produce a confusing
		// "credentials file not found" instead of a clean auth prompt.
		EnvAllow: []string{
			"GEMINI_API_KEY",
			"GOOGLE_API_KEY",
			"GOOGLE_GENAI_USE_VERTEXAI",
			"GOOGLE_CLOUD_PROJECT",
			"GOOGLE_CLOUD_LOCATION",
		},
		Command: NpmBootstrap("gemini", "@google/gemini-cli"),
		AutonomousArgs: func(prompt string) []string {
			// -p is Gemini CLI's non-interactive prompt mode, and --yolo is its
			// auto-approve. Both are needed: -p alone runs to completion but still
			// stops at a tool it wants confirmed, which detached means it hangs.
			return []string{"-p", prompt}
		},
		SkipPermissionArgs: []string{"--yolo"},
	},
	"opencode": {
		Name:       "opencode",
		PersistDir: "opencode",
		// Provider-agnostic, so the list spans the providers it can drive rather
		// than naming a vendor; each is forwarded only if the host has it set.
		EnvAllow: []string{
			"ANTHROPIC_API_KEY",
			"OPENAI_API_KEY",
			"GEMINI_API_KEY",
			"GROQ_API_KEY",
			"OPENROUTER_API_KEY",
			"OPENCODE_CONFIG",
			"OPENCODE_DISABLE_AUTOUPDATE",
		},
		Command: NpmBootstrap("opencode", "opencode-ai"),
		AutonomousArgs: func(prompt string) []string {
			// `opencode run` executes one message and exits. It has no TUI to draw
			// and no approval step to reach, which is what makes it usable here.
			return []string{"run", prompt}
		},
	},
	"cline": {
		Name:       "cline",
		PersistDir: "cline",
		// Empty for the reason opencode's is: Cline drives several providers and
		// its default one is its own, so there is no single host whose silence
		// means "this agent cannot work". Routing reports it unprobed rather than
		// down, which is the honest answer — guessing api.anthropic.com would fail
		// over an agent configured against OpenRouter for an outage it never had.
		ProviderHost: "",
		EnvAllow: []string{
			"ANTHROPIC_API_KEY",
			"CLINE_API_KEY",
			"OPENAI_API_KEY",
			"OPENROUTER_API_KEY",
			"AI_GATEWAY_API_KEY",
			"V0_API_KEY",
		},
		Command: NpmBootstrap("cline", "cline"),
		AutonomousArgs: func(prompt string) []string {
			// Verified by running it, 2026-08-24: `cline <prompt>` is the
			// non-interactive mode — a bare positional runs in act mode and the TUI
			// is opt-in behind `-i/--tui`, which is the inverse of claude's `-p`.
			// One real run wrote its file and exited 0 in sixteen seconds with
			// nothing attached.
			return []string{prompt}
		},
		// `--auto-approve` defaults to true, and this passes it anyway. A default
		// is a decision upstream can revisit; an unattended run that starts asking
		// does not fail, it hangs, and the flag costs two tokens. Verified accepted
		// in the same session.
		SkipPermissionArgs: []string{"--auto-approve", "true"},
		// Verified with a pty on 2026-08-24: `cline -i` starts the full-screen UI.
		// Without it a console run gets the headless mode with no prompt, which
		// prints usage and exits — an attached terminal watching a dead container.
		ConsoleArgs: []string{"-i"},
		// Not seeded: whether a positional reaches the TUI as a first turn was not
		// verified. nil means Studio refuses the combination rather than building
		// an argv that dies inside the container.
		ConsolePromptArgs: nil,
		// Resuming lives in internal/agentctx rather than here, and needs the
		// transcript store's location *and* format verified before a reader can
		// claim to understand it. Cline's help documents `--id <session-id>`, so
		// the capability exists; it is not claimed until somebody has read one.
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

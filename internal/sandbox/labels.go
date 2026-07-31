package sandbox

import "unicode/utf8"

// The docker labels stamped on every container sandbox-cli starts.
//
// These are the addressing mechanism for everything that happens *after* the
// launching process is gone: `ps`, `clean`, and the whole fleet. Docker is the
// state store, so a fact not stamped here is one no later command can recover —
// and a name is not a fact, because names are for humans and are not parsed.
//
// They are constants rather than literals at the one place that writes them
// because they are now read in three packages: `sandbox` stamps them, `fleet`
// filters on them, and `cli` displays them. A label key that is a string literal
// in two of those is a typo waiting to become an empty table.
const (
	// LabelCLI marks a container as ours. Stamped unconditionally, which is the
	// point: every other label describes the *work* and is omitted when there is
	// nothing true to say, so a run outside a git repository would otherwise carry
	// no labels at all and be invisible to `ps` — and a container nobody can list
	// is one nobody can stop.
	LabelCLI = "sandbox.cli"

	// LabelRepo is worktree.RepoID: a stable identity shared by every branch of one
	// repository, so "every container for this project" is a single label query
	// even though each agent runs in a different directory. Deliberately an id and
	// not a path — two clones of a same-named repo would otherwise share a label
	// namespace.
	LabelRepo = "sandbox.repo"

	// LabelBranch is the git branch the workspace was on at launch. It is also how
	// a fleet task is addressed, and how `land` recognises an agent working the
	// main checkout: git refuses to check out one branch in two worktrees, so a
	// container carrying the base branch's label is in the main checkout.
	LabelBranch = "sandbox.branch"

	// LabelAgent is the adapter name ("claude", "codex"), empty for a plain run.
	LabelAgent = "sandbox.agent"

	// LabelBase is the branch the work is expected to land on, recorded at launch
	// because by landing time the checkout may be on a different one — and "the
	// branch checked out now" is a different question from "the branch this agent
	// was sent to work towards".
	LabelBase = "sandbox.base"

	// LabelFleet marks a container that a `fleet run` launched, as opposed to an
	// interactive detached session in the same repository. Without it every fleet
	// command is repo-scoped rather than fleet-scoped: `fleet stop --all` reaches a
	// detached `sandbox-cli claude`, `fleet clean` reaps it, and max_parallel counts
	// it — so one open interactive session blocks a `max_parallel: 1` fleet forever
	// on a slot that will never free.
	LabelFleet = "sandbox.fleet"

	// LabelVerify is the task's definition of done, when it declared one. Its
	// presence is what lets `land` tell "this run had no check" from "this run
	// passed its check"; the verdict itself is the container's exit code.
	LabelVerify = "sandbox.verify"

	// LabelProfile is the security profile in force at launch — dev or prod.
	//
	// Recorded because it cannot be recovered afterwards and it is the first
	// question asked of a finished run: a container's capabilities and mounts
	// say what it *got*, but not which posture it was launched under, and the
	// config that decided it may have been edited since. Every other reviewable
	// fact about a run is stamped for exactly this reason.
	LabelProfile = "sandbox.profile"

	// LabelPrompt is what an agent was asked to do, when a caller supplied the
	// prompt as a value rather than burying it in an argv.
	//
	// Stamped because "what was this agent told to do" is unanswerable later
	// otherwise — the prompt survives only inside the container's command, where
	// reading it back means parsing an agent-specific argv and knowing which
	// position holds it.
	//
	// It is a label, so treat it as readable: anything that can talk to the
	// daemon can `docker inspect` it. That is the same bargain LabelVerify
	// already makes with a user-authored shell command, and the reason a prompt
	// is the *only* free text stamped — a secret value never becomes one, which
	// is what the credential broker exists to guarantee.
	LabelPrompt = "sandbox.prompt"

	// LabelBaseline is the crash-snapshot commit taken immediately before this
	// run started: a before-image of the workspace, including files git does not
	// track, written by internal/rescue through its private index.
	//
	// It exists so "what did this run change" can be answered at all. Without it
	// the only available question is "what is uncommitted in this workspace",
	// which is the same answer for a --worktree run (whose checkout belongs to
	// that run alone) and a wrong one for a run in a checkout you also work in —
	// there, your own unfinished edits get credited to an agent that never
	// touched them.
	//
	// A commit id rather than a ref: refs move, and this must still name the tree
	// the run actually started from when it is read a week later.
	LabelBaseline = "sandbox.baseline"
)

// maxPromptLabel bounds what LabelPrompt carries.
//
// Docker holds labels in the container config it keeps in memory and writes on
// every inspect, and a fleet prompt is routinely a page of instructions. The
// value is truncated rather than dropped because the opening line is what
// identifies a run in a list, and it is marked when truncated so no reader
// mistakes a prefix for the whole instruction.
const maxPromptLabel = 512

// truncatePrompt bounds a prompt for LabelPrompt, marking it when it had to cut.
//
// The marker is not decoration: a client showing a prompt has to be able to tell
// "this is what was asked" from "this is the start of what was asked", and a
// silently clipped instruction reads as a complete one. Cuts on a rune boundary
// so the value stays valid UTF-8 for JSON.
func truncatePrompt(p string) string {
	if len(p) <= maxPromptLabel {
		return p
	}
	cut := maxPromptLabel
	for cut > 0 && !utf8.ValidString(p[:cut]) {
		cut--
	}
	return p[:cut] + "…[truncated]"
}

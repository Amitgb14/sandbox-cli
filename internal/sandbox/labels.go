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

	// LabelRoutedFrom is the agent that was *asked* for, when routing fell through
	// to a different one. Absent when the run used the agent it was given.
	//
	// A label rather than only an audit line, because the audit line is written
	// when a run *ends* — which for a detached run is long after the launch
	// returned, and long after somebody looks at the Runs screen and asks why it
	// says codex when they picked claude. Docker is the state store: a fact not
	// stamped here is one no later command can recover.
	LabelRoutedFrom = "sandbox.routed_from"

	// LabelRouteReason is why that happened, in the words the run reported:
	// "provider answered 503". Paired with LabelRoutedFrom because the fact and
	// its justification are useless apart — one says a surprise happened, the
	// other says whether it was the right one.
	LabelRouteReason = "sandbox.route_reason"

	// LabelRouteID is the routing episode this run belongs to; every attempt in
	// one chain shares it. Stamped for the same reason the audit field exists —
	// two containers that were one attempt at one task are otherwise
	// indistinguishable from two unrelated runs.
	LabelRouteID = "sandbox.route_id"

	// LabelRouteAttempt is where in the episode this run sits: 1 for the agent
	// first asked for, 2 for the next.
	//
	// A label as well as an audit field, and the reason is the one that put
	// LabelRoutedFrom here: a detached run's audit line is written when it
	// *ends*, and the listing is read while it is still going. Without it the
	// two live containers of a failover are ordered only by their timestamps,
	// which is exactly the reading the id exists to make unnecessary.
	LabelRouteAttempt = "sandbox.route_attempt"

	// LabelHandoffFrom is the agent whose conversation this run was briefed with,
	// and LabelHandoffSession the session it came from.
	//
	// Deliberately *not* LabelRoutedFrom, though the mechanism underneath is the
	// same briefing. Routing means a provider stopped answering and sandbox-cli
	// chose the next agent; a handoff means a person read a conversation and
	// decided who should carry it on. In a listing the two look identical — codex
	// running work claude started — and they lead to opposite questions: one asks
	// what broke, the other asks nothing at all.
	LabelHandoffFrom    = "sandbox.handoff_from"
	LabelHandoffSession = "sandbox.handoff_session"

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

	// LabelSession is the agent conversation this run reopened, when it was
	// started with a resume rather than a fresh prompt.
	//
	// Stamped because it is the one case where the transcript belonging to a run
	// is *known* rather than inferred. Everything else correlates by agent, time
	// window and prompt, and a resumed run defeats all three by definition: its
	// conversation began before the container did. Docker is the state store, so
	// a fact not recorded here is one no later command can recover.
	LabelSession = "sandbox.session"

	// The session-server labels. Three, where the plan for that track proposed
	// ten — the other seven are either already stamped above or derivable from
	// what is, and a second label for a fact already recorded is a second answer
	// that can disagree with the first.
	//
	// Dropped as already present: `sandbox.managed` (LabelCLI is the marker for
	// "this is ours", and the filter every command already uses),
	// `sandbox.repo_hash` (LabelRepo *is* worktree.RepoID — directory name plus a
	// hash of the absolute path), `sandbox.branch`, `sandbox.agent`,
	// `sandbox.profile`. Dropped as derivable: the workspace id follows from
	// LabelRepo and the worktree id from LabelBranch, both deterministically, so
	// storing them would only create a way for the catalog and the container to
	// disagree about which worktree a pane is in.
	//
	// Stamped by phase 2, when the server starts containers. Declared now because
	// phase 1 *reads* them: a container started by an older sandbox-cli carries
	// none of them, and adoption has to be able to say so rather than guess.

	// LabelPane is the pane id a container belongs to, and the only one of the
	// three that cannot be derived: it is the catalog's own key, minted when the
	// pane is created.
	//
	// Its absence is meaningful. A container without it was started before the
	// session server existed, so the catalog has nothing to join on — such a pane
	// is listed with its *container name* as its id and marked legacy, because a
	// synthesised `p_` id would name something no engine has heard of.
	LabelPane = "sandbox.pane"

	// LabelPaneKind is what the pane is for: agent, shell, command, verify or
	// console.
	//
	// Named pane_kind rather than kind because `sandbox-cli list` already prints a
	// KIND column and it already means interactive-vs-fleet (sessionKind, from
	// LabelFleet). Two different KINDs in one tool reads fine in a diff and is
	// indistinguishable in a terminal, which is where somebody decides what to
	// kill.
	LabelPaneKind = "sandbox.pane_kind"

	// LabelPaneSession is which session daemon owns this pane.
	//
	// *Not* `sandbox.session`, which is taken, and the collision is the sharp kind
	// rather than the cosmetic kind: that label means the agent conversation a run
	// reopened, so reusing the name would make a daemon id and a conversation id
	// indistinguishable in the one place both are recorded — and `recover`'s
	// resume correlation reads it.
	LabelPaneSession = "sandbox.pane_session"

	// LabelSandbox is the config.SandboxKind that isolated this run.
	//
	// Recorded rather than inferred for the reason LabelProfile already is: a
	// catalog read next week should not have to ask which engine the machine has
	// *now*. Today it is always a container kind, which is exactly why stamping it
	// costs nothing and why the field has to exist before that stops being true.
	LabelSandbox = "sandbox.sandbox"

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

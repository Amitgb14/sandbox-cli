package sandbox

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
)

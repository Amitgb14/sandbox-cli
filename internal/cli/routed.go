package cli

import (
	"context"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agentctx"
	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/handoff"
	"github.com/Amitgb14/sandbox-cli/internal/rescue"
	"github.com/Amitgb14/sandbox-cli/internal/routing"
	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// Running an agent with a fallback behind it.
//
// The loop is small; what it costs to get right is everything around it. Three
// rules, each of which the tests in internal/routing state as a case:
//
//  1. **Probe before launching.** A provider that is not answering means skip
//     this agent before a container exists — nothing half-done, and the reason
//     is a measurement rather than an inference.
//  2. **Retry only a run that left the workspace alone.** A run that changed
//     files is a failed attempt, not an outage, and handing the next agent
//     somebody's half-finished edits is the thing this must never do.
//  3. **Say what happened, every time.** A run that silently used a different
//     agent than the one typed is worse than a run that failed: the transcript,
//     the login it used and the bill all moved, and nothing said so.
//
// The chain is re-targeted through internal/agents rather than by rewriting the
// wrapper's argv, because an agent is not just a command — it is a persisted
// HOME, an env allowlist, and a set of container variables. Rebuilding all four
// from the descriptor is what makes `--fallback codex` mean the same thing as
// having typed `sandbox-cli codex`.

// routedRun executes guest under the first agent in the chain that will have it,
// falling through on the two conditions above.
//
// primary is the agent the wrapper was invoked as; guestArgs are the arguments
// after the agent's own command (the prompt and its flags), which are re-applied
// to whichever agent ends up running.
// userInputs is what the *user* asked for, held apart from what the wrapper
// added on top.
//
// The distinction only exists because of routing, and it cannot be recovered
// afterwards: by the time a run reaches the loop, `--mount /notes` and the
// claude wrapper's own history mount are two strings in the same slice. A
// fallback needs the first and must not inherit the second, so each is captured
// at the point where the two are still separable (see runWrapper).
type userInputs struct {
	mounts   []string
	env      []string
	envAllow []string
}

// retarget builds the flags for a fallback attempt: the same run, aimed at a
// different agent.
//
// Four things change, and every one of them is load-bearing — this is the whole
// difference between `--fallback codex` and "claude's container with codex's
// binary in it":
//
//   - the persisted HOME, so the fallback finds its own login rather than
//     somebody else's;
//   - the env allowlist, so its own host variables are forwarded and the
//     primary's are not;
//   - the container env the descriptor sets — droid's FACTORY_DISABLE_KEYRING is
//     exactly the unattended-login failure internal/agents documents;
//   - the mounts, reset to the user's own so the primary wrapper's
//     agent-specific reach does not travel. Without this a claude → codex
//     failover bind-mounts the host's Claude history into a codex container that
//     was never asked to have it.
//
// The briefing is the fifth, and it belongs here rather than beside the argv
// because the two are one statement: an agent told to read /sandbox/context and
// handed no such mount burns its turns looking for a directory that does not
// exist. That is exactly the shape this function exists to prevent — every
// omission in this list fails *silently*, with a container that starts fine and
// an agent that is simply missing something.
//
// Everything else about the run — the workspace, the profile, the network
// posture, the caps — is carried unchanged. It describes the run, not the agent.
func retarget(base runFlags, d agents.Descriptor, user userInputs, carried *handoff.Export) runFlags {
	base.persistName = d.PersistDir
	base.envAllow = append(append([]string(nil), user.envAllow...), d.EnvAllow...)
	base.env = append(append([]string(nil), user.env...), d.Env...)
	base.mounts = append([]string(nil), user.mounts...)
	if carried != nil {
		base.mounts = append(base.mounts, carried.Dir+":"+handoff.GuestDir+":ro")
	}
	return base
}

func routedRun(rf *runFlags, primary string, guestArgs, unrouted []string, user userInputs) error {
	fallbacks, err := configuredFallbacks(rf, primary)
	if err != nil {
		return err
	}

	// Ten of the fifteen wrappers have no descriptor: they are adapters whose
	// non-interactive mode was never verified, so internal/agents does not admit
	// them. A fallback has to be re-targeted — its command, its persisted HOME,
	// its env allowlist — and only a descriptor says how, so routing simply is
	// not available for them. Asking for it is refused; not asking takes exactly
	// the path it always took.
	if _, known := agents.Lookup(primary); !known {
		if len(fallbacks) > 0 {
			return fmt.Errorf(
				"--fallback is not available for %s: it has no verified non-interactive mode, "+
					"so sandbox-cli cannot re-target a run at it (routable agents: %s)",
				primary, strings.Join(agents.Names(), ", "))
		}
		return execute(rf, unrouted)
	}

	// Whether anybody is watching this run, which decides what a fallback is
	// allowed to become.
	//
	// Three ways to be unattended, and each is the user saying so rather than
	// this inferring it: --detach leaves nothing attached, --no-tty gives the
	// agent no terminal to ask through, and typing the agent's own
	// skip-permissions flag is an explicit request to stop being asked. Anything
	// else is somebody at a terminal, and a failover must not quietly remove
	// them (see fallbackArgv).
	unattended := rf.detach || rf.noTTY || asksForAutonomy(primary, guestArgs)

	chain, err := routing.Resolve(primary, fallbacks, unattended)
	if err != nil {
		return err
	}
	// The common case, and it must stay exactly what it was: one agent, no probe,
	// no extra machinery in front of a run somebody is watching — and the
	// wrapper's own argv rather than a re-derived one, so a chain of one is
	// byte-identical to no chain at all.
	if len(chain) == 1 {
		return execute(rf, unrouted)
	}

	ctx := context.Background()
	var skipped []string
	var carried *handoff.Export
	// One briefing directory per failover, removed when the run is over. It is a
	// mount for the container's lifetime, so it cannot go earlier — and leaving
	// them behind would litter the temp directory once per outage.
	var briefings []string
	defer func() {
		for _, d := range briefings {
			os.RemoveAll(d)
		}
	}()
	var routedFrom, routeReason string

	// One id for the whole episode, minted before the first attempt so the agent
	// that was *asked for* carries it too. Without that the rescued case reads as
	// one routed run with no history, and "did routing help" stays unanswerable
	// for exactly the runs it helped.
	//
	// Only when there is somewhere to fall through to: an id on a run that could
	// never route describes an episode that cannot happen.
	episode := routing.NewID()

	// Which host to ask for each agent, from the user's own config. Read once:
	// the answer cannot change mid-chain, and re-reading per attempt would make
	// a config edit apply to half an episode.
	providerHosts := configuredProviders(rf)

	// What the user actually asked the agent to do, recovered from their own
	// arguments so it can be re-expressed in another agent's spelling.
	//
	// Not an error here, even when it cannot be recovered: the *primary* runs the
	// argv the user typed, whatever shape it has, and only a fallback needs the
	// task re-expressed. Refusing up front turned an ordinary interactive run
	// (`sandbox-cli claude --dangerously-skip-permissions`, with a chain in the
	// user's config) into a hard failure before anything launched.
	prompt, promptErr := promptFrom(guestArgs)

	for i, name := range chain {
		d, ok := agents.Lookup(name)
		if !ok {
			continue // Resolve validated these; belt and braces
		}

		// Probe every candidate, including the first: the whole point is to find
		// out before starting that claude is down.
		//
		// Except under --dry-run, which is asked to *show* a command rather than
		// decide anything. Reaching the network there would make a preview depend
		// on a provider's health, and could refuse to print an argv somebody only
		// wanted to read.
		if avail := probeUnlessDryRun(ctx, rf, name, providerHosts); !avail.Reachable {
			skipped = append(skipped, fmt.Sprintf("%s (%s)", name, avail.Reason))
			fmt.Fprintf(os.Stderr, "sandbox-cli: skipping %s — %s\n", name, avail.Reason)
			// Recorded, not merely printed. A skip is a switch: the run that
			// starts next is not the agent that was asked for, and stderr is not
			// where that is looked up afterwards — the container labels and the
			// audit line are. Without this the preflight case, which is the one
			// the feature exists for, was written down as an ordinary run of
			// whichever agent happened to answer.
			//
			routedFrom, routeReason = noteSkip(routedFrom, name, skipped)
			if i == len(chain)-1 {
				return fmt.Errorf("no agent in the chain %s is available: %s",
					chain, strings.Join(skipped, ", "))
			}
			continue
		}

		if len(skipped) > 0 || i > 0 {
			announceRoute(primary, name, skipped)
		}

		attempt := *rf
		attempt.routedFrom, attempt.routeReason = routedFrom, routeReason
		attempt.routeID, attempt.routeAttempt = episode, i+1
		if i > 0 {
			attempt = retarget(attempt, d, user, carried)
		}

		// The argv. The primary keeps exactly what the user typed; a fallback is
		// rebuilt **from the prompt**, because agent flags do not travel: claude's
		// headless mode is `-p <prompt>` and codex's is `exec <prompt>`, so
		// re-applying one agent's flags to another produces nonsense that fails in
		// a way nobody would connect to routing.
		argv := unrouted
		if i > 0 {
			if promptErr != nil {
				// The point at which it stops being recoverable: this agent needs the
				// task in its own spelling and there is none to give it.
				return promptErr
			}
			built, perr := fallbackArgv(d, prompt, carried, unattended)
			if perr != nil {
				return perr
			}
			argv = built
		}

		before := workspaceTree(&attempt)
		// When this attempt began, kept because it is the only thing that ties
		// the conversation it wrote to *this* run rather than to some earlier
		// one in the same store. Taken before the container starts, with slack
		// applied where it is used.
		startedAt := time.Now()
		err := execute(&attempt, argv)
		code := exitCode

		if err != nil {
			return err
		}
		if code == 0 {
			return nil
		}

		// The run failed. Whether that is an outage or an answer depends entirely
		// on whether it touched the workspace.
		changed := workspaceChanged(&attempt, before)
		over, why := routing.ShouldFailOver(routing.Outcome{
			Agent: name, ExitCode: code, WorkspaceChanged: changed,
		})
		if !over || i == len(chain)-1 {
			if over {
				fmt.Fprintf(os.Stderr,
					"sandbox-cli: %s %s, and it was the last agent in the chain\n", name, why)
			}
			exitCode = code
			return nil
		}
		fmt.Fprintf(os.Stderr, "sandbox-cli: %s %s — trying %s\n", name, why, chain[i+1])
		skipped = append(skipped, fmt.Sprintf("%s (exit %d, nothing written)", name, code))
		routedFrom, routeReason = name, why

		// Carry the conversation. The failed agent said things worth not
		// repeating — which files it read, what it decided, what it was about to
		// do — and the next one starts from the prompt alone without them.
		//
		// Best-effort by construction: an agent that died before writing a
		// transcript is the commonest case here, and a handoff that failed the run
		// because it had nothing to summarise would break the feature it exists to
		// improve.
		if ex := prepareHandoff(&attempt, name, startedAt, prompt); ex != nil {
			carried = ex
			briefings = append(briefings, ex.Dir)
		}
	}
	return nil
}

// baseBranchFor is the branch this run's work is measured against: the branch
// the *repository* has checked out, which is what `--worktree` branches from and
// what `land` merges into. Empty when it cannot be determined, which leaves the
// ledger reporting uncommitted work only — the place an interrupted agent's
// output usually still is anyway.
func baseBranchFor(rf *runFlags) string {
	repo := config.ExpandTilde(rf.project)
	if repo == "" {
		repo, _ = os.Getwd()
	}
	return worktree.HeadBranch(repo)
}

// promptFrom recovers the task from the user's own arguments.
//
// The rule is deliberately narrow: the prompt is the **last argument, when it is
// not a flag**. That covers what people actually type — `-p "do X"`,
// `--dangerously-skip-permissions "do X"`, a bare `"do X"` — and nothing else.
//
// When the last argument *is* a flag this reports an error rather than guessing —
// but the caller only acts on it at the moment a fallback has to be built, since
// the primary runs the argv as typed and an interactive run whose last argument
// is a flag is perfectly ordinary. Flags do not travel between agents (claude's headless mode is
// `-p <prompt>`, codex's is `exec <prompt>`), so a fallback has to be rebuilt
// from the prompt — and a wrong guess would send the next agent a flag value as
// though it were the task. Refusing costs a re-run; guessing costs an agent
// confidently doing the wrong thing with file-writing tools.
func promptFrom(guestArgs []string) (string, error) {
	if len(guestArgs) == 0 {
		return "", nil // an interactive run: there is no prompt to carry
	}
	last := guestArgs[len(guestArgs)-1]
	if !strings.HasPrefix(last, "-") {
		return last, nil
	}
	return "", fmt.Errorf(
		"cannot route this run: the task has to be re-expressed for a fallback agent, and the last argument (%q) is a flag rather than a prompt.\n"+
			"  Agent flags do not travel — claude's headless mode is `-p <prompt>` where codex's is `exec <prompt>` — so sandbox-cli rebuilds the run from the prompt.\n"+
			"  Put the prompt last, or drop --fallback for this run.", last)
}

// prepareHandoff exports the finished agent's conversation for the next one, or
// returns nil when there is nothing to carry and nowhere to put it.
func prepareHandoff(rf *runFlags, from string, since time.Time, prompt string) *handoff.Export {
	dir, err := os.MkdirTemp("", "sandbox-handoff-*")
	if err != nil {
		return nil
	}
	ws, _ := resolveWorkspaceFor(rf)
	ex, err := handoff.Write(dir, from, transcriptPathFor(from, since, prompt), ws, baseBranchFor(rf))
	if err != nil {
		os.RemoveAll(dir)
		return nil
	}
	fmt.Fprintf(os.Stderr,
		"sandbox-cli: carrying %s's briefing forward — %d prompt(s), %d changed file(s), mounted read-only at %s\n",
		from, ex.Turns, ex.Files, handoff.GuestDir)
	return ex
}

// transcriptPathFor is the conversation *this attempt* wrote, or "" when it
// cannot be told from another.
//
// The first version took the newest session in the sandbox-owned store by mtime,
// which is wrong in the two ways internal/studioapi's console view documents at
// length after hitting both. A session still being appended to has a recent
// mtime that says nothing about who owns it, and sandbox runs of one agent all
// pool into a single bucket — so a failover in one repository could export a
// conversation from another, and the *commonest* case here is an agent that
// died before writing anything at all, where the newest session is by definition
// somebody else's.
//
// Correlating instead on when the session *started*, inside this attempt's
// window, with the prompt as the tie-break when two ran at once. Nothing is
// exported rather than the wrong thing: a briefing is handed to another agent as
// evidence about work in progress, and a confident account of a conversation
// that never happened is worse than the honest "there was none".
func transcriptPathFor(agent string, since time.Time, prompt string) string {
	f, ok := agentctx.Resolve(agent, agentctx.DefaultRoots(), time.Now())
	if !ok || f.State != agentctx.StateVerified {
		return ""
	}
	f = sandboxOwnedStore(f)
	if f.Dir == "" {
		return ""
	}
	sessions, _, err := agentctx.List(f, agentctx.ListOpts{})
	if err != nil || len(sessions) == 0 {
		return ""
	}
	// The same slack the console view uses, for the same two reasons: a clock
	// that is not the container's, and a transcript whose first line is written
	// a moment after the process starts.
	path, ok := agentctx.PickSession(sessions, since.Add(-handoffSlack), time.Now().Add(handoffSlack), prompt)
	if !ok {
		return ""
	}
	return path
}

// handoffSlack widens the window at both ends. Small on purpose: it exists to
// absorb a second of clock skew, not to admit a neighbouring run.
const handoffSlack = 90 * time.Second

// sandboxOwnedStore narrows a finding to the agent HOME containers get. Mirrors
// studioapi's sandboxStore; kept separate rather than exported across packages
// because the two answer to different callers and neither should be able to
// widen the other's search.
func sandboxOwnedStore(f agentctx.Finding) agentctx.Finding {
	if f.Root == agentctx.RootAgent {
		f.Locations = nil
		return f
	}
	for _, loc := range f.Locations {
		if loc.Root == agentctx.RootAgent {
			f.Dir, f.Root, f.Locations = loc.Dir, loc.Root, nil
			return f
		}
	}
	f.Dir = ""
	return f
}

// autonomousArgv rebuilds the run for a fallback agent from the prompt, adding
// the handoff pointer when there is one to add.
func fallbackArgv(d agents.Descriptor, prompt string, carried *handoff.Export, unattended bool) ([]string, error) {
	if prompt == "" {
		// No prompt to carry: an interactive run started with no task on the
		// argv. The fallback gets its own UI, and the briefing is mounted for
		// the person driving it to read.
		return d.Command, nil
	}
	if carried != nil {
		prompt = carried.Prompt(prompt)
	}
	if !unattended {
		// The posture the user chose, kept. `sandbox-cli claude "refactor auth"`
		// is an interactive session seeded with a task: somebody is at the
		// terminal and the agent asks before it acts. Re-expressing that as
		// `codex exec` with approvals off would answer a failed provider by
		// removing the human — a larger change than the one that was asked for,
		// and precisely the flag docs/GUIDE.md says the wrappers must never add
		// on somebody's behalf.
		//
		// An agent that cannot be seeded on the command line still starts, with
		// the briefing mounted; the alternative is refusing the failover over a
		// first turn the person can type themselves.
		return d.Console(prompt, false), nil
	}
	return d.Autonomous(prompt, nil), nil
}

// configuredFallbacks is --fallback, or the user config's `routing:` when the
// flag is absent.
//
// The flag wins outright rather than merging, for the same reason the config
// layers replace a chain instead of appending to one: a chain is an ordered
// decision, and a merge of two produces an order nobody wrote.
//
// A `routing:` list whose head is a *different* agent than the wrapper being run
// is read as fallbacks only — typing `sandbox-cli codex` means codex, and a
// config that could silently redirect it to claude would make the command name a
// suggestion. So the primary is always what was typed, and the configured chain
// contributes the rest.
func configuredFallbacks(rf *runFlags, primary string) ([]string, error) {
	if len(rf.fallback) > 0 {
		return rf.fallback, nil
	}
	startDir, _ := os.Getwd()
	cfg, err := config.LoadProfile(startDir, rf.config, rf.profile)
	if err != nil {
		// Not fatal here: the run path below loads the same configuration and
		// reports properly. Failing twice for one bad file would print it twice.
		return nil, nil
	}
	var out []string
	for _, name := range cfg.Routing {
		if name != primary {
			out = append(out, name)
		}
	}
	return out, nil
}

// configuredProviders is the user's provider overrides, or nil.
//
// Silent on failure for the same reason configuredFallbacks is: the run path
// below loads the same configuration and reports properly, and failing twice for
// one bad file prints it twice.
func configuredProviders(rf *runFlags) map[string]string {
	startDir, _ := os.Getwd()
	cfg, err := config.LoadProfile(startDir, rf.config, rf.profile)
	if err != nil {
		return nil
	}
	return cfg.Providers
}

// probeUnlessDryRun answers the availability question, or declines to ask it.
//
// A dry run prints the command a real run would build; it starts nothing, so
// there is nothing for a provider's health to decide. Reporting "reachable,
// unprobed" keeps the preview on the primary — which is the agent the printed
// argv is for.
func probeUnlessDryRun(ctx context.Context, rf *runFlags, agent string, hosts map[string]string) routing.Availability {
	if rf.dryRun {
		return routing.Availability{Agent: agent, Reachable: true}
	}
	return routing.Probe(ctx, agent, hosts)
}

// announceRoute says that a different agent is running than the one typed.
//
// Not a debug line. The agent decides which login is used, which provider is
// billed, and where the transcript is written, so a run that quietly swapped it
// would be three surprises discovered later — and the whole reason the chain is
// safe to use is that it is never silent about having fired.
func announceRoute(primary, actual string, skipped []string) {
	fmt.Fprintf(os.Stderr, "sandbox-cli: routing %s → %s", primary, actual)
	if len(skipped) > 0 {
		fmt.Fprintf(os.Stderr, " (skipped %s)", strings.Join(skipped, ", "))
	}
	fmt.Fprintln(os.Stderr)
	fmt.Fprintf(os.Stderr,
		"sandbox-cli: %s runs with its own login and its own transcript; this is a different agent, not a resumed one\n",
		actual)
}

// workspaceTree records the workspace as it stands before a run, as a git tree
// object, so "did anything change" can be answered exactly afterwards.
//
// Returns "" when it cannot be answered — outside a repository, or with
// snapshots off. That absence is carried through to a nil WorkspaceChanged and
// therefore to "do not retry": the rule fails closed, because reading unknown as
// unchanged would retry a run that may have done real work.
func workspaceTree(rf *runFlags) string {
	if rf.noSnapshot {
		return ""
	}
	ws, err := resolveWorkspaceFor(rf)
	if err != nil || ws == "" {
		return ""
	}
	t, err := rescue.TreeOf(ws)
	if err != nil {
		return ""
	}
	return t
}

// workspaceChanged compares the workspace against the tree taken before the run.
// nil means the question could not be answered at either end.
func workspaceChanged(rf *runFlags, before string) *bool {
	if before == "" {
		return nil
	}
	after := workspaceTree(rf)
	if after == "" {
		return nil
	}
	changed := after != before
	return &changed
}

// resolveWorkspaceFor is the directory this run will mount, without building a
// whole spec for it. A worktree run works in the worktree, not the repository.
//
// Read-only, deliberately: newSession resolves `--worktree` with worktree.Resolve,
// which *creates* the directory when it is missing and prints that it did. Asking
// again here would repeat a side effect and a line of output for a question that
// only wants to look.
//
// The known limit that follows, stated rather than hidden: a worktree this run
// is about to create does not exist yet, so there is no "before" tree to compare
// against and the failover rule sees an unanswerable question. It therefore does
// not retry on that first run — failing closed, which is the same direction
// every other unknown takes here.
func resolveWorkspaceFor(rf *runFlags) (string, error) {
	repo := config.ExpandTilde(rf.project)
	if repo == "" {
		repo, _ = os.Getwd()
	}
	if rf.worktree == "" {
		return repo, nil
	}
	path, exists, err := worktree.Path(repo, rf.worktree)
	if err != nil || !exists {
		return "", err
	}
	return path, nil
}

// asksForAutonomy reports whether the user's own arguments already turn this
// agent's approval prompts off.
//
// The wrappers deliberately never add that flag — docs/GUIDE.md is explicit that
// a person at a terminal is the one party who can still be asked — so its
// presence in the argv is the user having asked for an agent that does not stop.
// A fallback may then be built the same way.
func asksForAutonomy(agent string, args []string) bool {
	d, ok := agents.Lookup(agent)
	if !ok {
		return false
	}
	for _, flag := range d.SkipPermissionArgs {
		if slices.Contains(args, flag) {
			return true
		}
	}
	return false
}

// noteSkip is the record a skipped provider leaves behind: which agent the run
// was meant to use, and why it is not using it.
//
// The *first* agent skipped is the one that was asked for; a later one is
// already a fallback, so the earliest answer is kept. The reason carries every
// skip so far, because "gemini ran" is explained by the whole sequence rather
// than by its last link.
func noteSkip(routedFrom, name string, skipped []string) (from, reason string) {
	if routedFrom == "" {
		routedFrom = name
	}
	return routedFrom, strings.Join(skipped, "; ")
}

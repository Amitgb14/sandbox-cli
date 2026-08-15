package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
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
func routedRun(rf *runFlags, primary string, guestArgs, unrouted, userMounts, userEnvAllow []string) error {
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

	chain, err := routing.Resolve(primary, fallbacks, false)
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
	episode := newRouteID()

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
			if i == len(chain)-1 {
				return fmt.Errorf("no agent in the chain %s is available: %s",
					chain, strings.Join(skipped, ", "))
			}
			continue
		}

		if len(skipped) > 0 || i > 0 {
			announceRoute(primary, name, skipped)
		}

		// Re-target: the descriptor's command, its persisted HOME, its env
		// allowlist. All three, or the fallback runs as itself with somebody
		// else's login mounted.
		attempt := *rf
		attempt.routedFrom, attempt.routeReason = routedFrom, routeReason
		attempt.routeID, attempt.routeAttempt = episode, i+1
		if i > 0 {
			// A fallback is re-targeted in *four* places, and every one of them is
			// load-bearing: the persisted HOME (its own login), the env allowlist
			// (its own forwarded names), the container env the descriptor sets —
			// droid's FACTORY_DISABLE_KEYRING is exactly the unattended-login
			// failure internal/agents documents — and the mounts, which are reset
			// to the user's own so the primary wrapper's agent-specific reach does
			// not travel. Without that last one a claude → codex failover
			// bind-mounts the host's Claude history into a codex container that was
			// never asked to have it.
			attempt.persistName = d.PersistDir
			attempt.envAllow = append(append([]string{}, userEnvAllow...), d.EnvAllow...)
			attempt.env = append(append([]string{}, rf.env...), d.Env...)
			attempt.mounts = append([]string(nil), userMounts...)
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
			// The briefing, mounted read-only where its prompt says it is. Written
			// beside the argv rather than anywhere else because the two are one
			// statement: an agent told to read /sandbox/context and handed no such
			// mount would burn turns looking for it.
			if carried != nil {
				attempt.mounts = append(attempt.mounts,
					carried.Dir+":"+handoff.GuestDir+":ro")
			}
			built, perr := autonomousArgv(d, prompt, carried)
			if perr != nil {
				return perr
			}
			argv = built
		}

		before := workspaceTree(&attempt)
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
		if ex := prepareHandoff(&attempt, name); ex != nil {
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
func prepareHandoff(rf *runFlags, from string) *handoff.Export {
	dir, err := os.MkdirTemp("", "sandbox-handoff-*")
	if err != nil {
		return nil
	}
	ws, _ := resolveWorkspaceFor(rf)
	ex, err := handoff.Write(dir, from, transcriptPathFor(from, ws), ws, baseBranchFor(rf))
	if err != nil {
		os.RemoveAll(dir)
		return nil
	}
	fmt.Fprintf(os.Stderr,
		"sandbox-cli: carrying %s's briefing forward — %d prompt(s), %d changed file(s), mounted read-only at %s\n",
		from, ex.Turns, ex.Files, handoff.GuestDir)
	return ex
}

// transcriptPathFor is the newest session this agent wrote for this project, or
// "" when there is none to find.
//
// Only the **sandbox-owned** store is searched, which is the same rule the
// console reads by: a container's HOME is that directory and nothing else, so a
// transcript anywhere but there was written by something other than this run —
// the developer's own live session, most often, which is by definition the most
// recently modified transcript on the machine.
func transcriptPathFor(agent, workspace string) string {
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
	newest := sessions[0]
	for _, s := range sessions[1:] {
		if s.Modified.After(newest.Modified) {
			newest = s
		}
	}
	return newest.Path
}

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
func autonomousArgv(d agents.Descriptor, prompt string, carried *handoff.Export) ([]string, error) {
	if prompt == "" {
		// No prompt to carry: an interactive run. The fallback gets its own UI,
		// and the briefing is mounted for the person driving it to read.
		return d.Command, nil
	}
	if carried != nil {
		prompt = carried.Prompt(prompt)
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

// newRouteID mints an identifier for one routing episode.
//
// Short and time-ordered rather than a UUID: it is read by people in a listing
// beside a timestamp, and it only has to be unique among the episodes on one
// machine. The same shape internal/rescue uses for a session id, for the same
// reason — an id nobody can say out loud is one nobody quotes in a bug report.
func newRouteID() string {
	var b [3]byte
	if _, err := rand.Read(b[:]); err != nil {
		// A machine with no entropy still gets an id: a collision here costs two
		// episodes being grouped, which is a wrong number in one table, while an
		// empty id costs the correlation entirely.
		return time.Now().UTC().Format("150405")
	}
	return time.Now().UTC().Format("20060102-150405") + "-" + hex.EncodeToString(b[:])
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

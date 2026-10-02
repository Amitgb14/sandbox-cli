package studioapi

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/handoff"
	"github.com/Amitgb14/sandbox-cli/internal/rescue"
	"github.com/Amitgb14/sandbox-cli/internal/routing"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

// Watching a detached run to its end, and starting the next agent when the one
// that failed left the workspace alone.
//
// This is the half of routing an HTTP handler cannot do, and the reason it could
// not is worth stating precisely, because it is not a property of Studio: a
// handler returns as soon as the container is up, so *nothing in the request*
// outlives the run. The daemon does. So the supervision goes where the lifetime
// is — one loop, owned by the process, asking the engine what has finished.
//
// It exists so that `--fallback codex` means the same thing in Studio as it does
// in a terminal. Until now it meant half: Studio probed the provider before
// launching and never retried afterwards, which covers an outage that has
// already started and misses one that begins ten seconds later — the case a long
// unattended run is most exposed to.
//
// Three rules, the same three internal/cli's routed run keeps, because they are
// the feature rather than an implementation of it:
//
//  1. **Retry only a run that left the workspace alone.** A run that wrote files
//     is a failed attempt, not an outage, and handing the next agent somebody's
//     half-finished edits is the thing this must never do. Everything unknowable
//     — a workspace that cannot be fingerprinted — counts as work done and is not
//     retried.
//  2. **The next agent is briefed, never resumed.** internal/handoff writes a
//     vendor-neutral briefing and it is mounted read-only; a session id belongs
//     to the agent that wrote it.
//  3. **Every switch is recorded.** Both attempts carry one route id, so the two
//     rows are recognisable afterwards as one attempt at one task.
//
// Two limits are deliberate and worth knowing before trusting this.
//
// **A restart forgets.** The watch set is in memory, so a daemon restarted
// mid-run leaves that run entirely alone: it keeps running, it is still listed,
// and it simply will not be retried. That is today's behaviour rather than a
// regression, and the alternative — persisting watches and re-adopting them —
// means deciding what to do with a container that finished while nothing was
// watching, which is a question with a wrong answer (retrying a run whose result
// somebody already acted on).
//
// **The failed container is renamed, not removed.** A detached container's name
// is what enforces one-agent-per-branch — docker refuses a duplicate atomically,
// which a list-then-launch check cannot — so the retry must take the name back.
// Removing the dead one would do that and throw away its logs, which are the
// evidence for why the failover happened. So it is renamed to
// `<name>-attempt<n>` and stays where it is.
type supervisor struct {
	s *Server

	// interval is how often the engine is asked what has finished. A poll rather
	// than an event stream because the engine interface already answers this
	// question — Containers is what the Runs screen is built on — and a
	// supervisor that needed its own transport would be a second way to learn the
	// same fact.
	interval time.Duration

	mu      sync.Mutex
	watched map[string]*watch // by container id

	// treeOf fingerprints a workspace, and is a field for the same reason
	// sandbox.hostTimezone is: it is the one input that needs a real git
	// repository on disk, and a test should be able to say what it returns
	// without building one.
	treeOf func(string) (string, error)

	start sync.Once
}

// watch is one supervised run: everything the next attempt would need, captured
// while the request that produced it is still in hand.
type watch struct {
	container string // the container being watched
	name      string // its name, which the retry inherits

	// req is the original request, with its original prompt. The prompt is *not*
	// updated as attempts go by: each briefing wraps the task the user asked for,
	// never the previous briefing's wrapper around it.
	req RunCreateRequest

	agent     string   // the agent currently running
	remaining []string // the chain still to try, in order
	workspace string   // host path mounted at /workspace
	before    string   // the workspace fingerprint taken before this attempt

	routeID string
	attempt int

	// briefings are the export directories mounted into attempts so far,
	// removed when this run stops being supervised. A mount is held for the
	// container's lifetime, so none of them can go earlier.
	briefings []string
}

func newSupervisor(s *Server) *supervisor {
	return &supervisor{
		s:        s,
		interval: 5 * time.Second,
		watched:  map[string]*watch{},
		treeOf:   rescue.TreeOf,
	}
}

// supervise registers a run and makes sure the loop is running.
//
// Started on first use rather than by the daemon's main, and the lifetime that
// follows is the honest one: the watch set is in memory, so the supervisor
// exists exactly as long as the process whose memory it is. There is nothing to
// shut down that outliving the process would mean anything to.
func (sv *supervisor) supervise(w *watch) {
	sv.mu.Lock()
	sv.watched[w.container] = w
	sv.mu.Unlock()
	sv.start.Do(func() { go sv.loop(context.Background()) })
}

func (sv *supervisor) loop(ctx context.Context) {
	t := time.NewTicker(sv.interval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			sv.tick(ctx)
		}
	}
}

// tick asks once what has finished, and decides for each.
//
// One listing for every watch rather than one call per run: this is the same
// question the Runs screen asks, and asking it per supervised run would turn a
// fleet-sized launch into a fleet-sized poll.
func (sv *supervisor) tick(ctx context.Context) {
	sv.mu.Lock()
	pending := len(sv.watched)
	sv.mu.Unlock()
	if pending == 0 {
		return
	}

	// Through listRuns rather than asking the engine directly, so the supervisor
	// and the Runs screen agree on what a sandbox container *is*. The first
	// version filtered on `sandbox.cli: "true"`, which is stamped `"1"` — nothing
	// matched, and nothing failed either: the loop simply found no finished runs,
	// forever.
	all, err := sv.s.listRuns(ctx, true, nil)
	if err != nil {
		// The engine being unreachable is not a decision. Runs are left watched:
		// a docker daemon that comes back finds them still supervised, which is
		// the opposite of what dropping them would do.
		return
	}
	live := make(map[string]runtime.ContainerInfo, len(all))
	for _, c := range all {
		live[c.ID] = c
	}

	sv.mu.Lock()
	watches := make([]*watch, 0, len(sv.watched))
	for _, w := range sv.watched {
		watches = append(watches, w)
	}
	sv.mu.Unlock()

	for _, w := range watches {
		c, ok := live[w.container]
		if !ok {
			// Removed while we were not looking — by `sandbox-cli clean`, by a
			// person, or by the engine. There is no exit code to read, so there is
			// nothing to decide.
			sv.drop(w)
			continue
		}
		if c.Running() || c.FinishedAt.IsZero() {
			continue
		}
		sv.settle(ctx, w, c)
	}
}

// settle applies the gate to one finished run.
func (sv *supervisor) settle(ctx context.Context, w *watch, c runtime.ContainerInfo) {
	if c.ExitCode == 0 || len(w.remaining) == 0 {
		sv.drop(w)
		return
	}

	over, why := routing.ShouldFailOver(routing.Outcome{
		Agent:            w.agent,
		ExitCode:         c.ExitCode,
		WorkspaceChanged: sv.changed(w),
	})
	if !over {
		sv.drop(w)
		return
	}
	if err := sv.failOver(ctx, w, why); err != nil {
		// A failover that could not start is reported and dropped rather than
		// retried on the next tick: whatever refused it — a name still held, an
		// image that will not build, a provider that answered and then stopped —
		// will refuse it again in five seconds, and a loop that keeps trying
		// writes the same line forever.
		log.Printf("sandbox-studio-api: %s failed and %s could not be started: %v",
			w.agent, w.remaining[0], err)
		sv.drop(w)
	}
}

// changed answers whether this attempt wrote anything, or nil when it cannot be
// answered.
//
// nil is a real answer and routing.ShouldFailOver reads it as "assume work was
// done". A workspace that cannot be compared is not evidence of an idle run, and
// the asymmetry is deliberate: a wrong retry destroys work, a missed one costs a
// re-run somebody asks for by hand.
func (sv *supervisor) changed(w *watch) *bool {
	if w.before == "" || w.workspace == "" {
		return nil
	}
	after, err := sv.treeOf(w.workspace)
	if err != nil || after == "" {
		return nil
	}
	changed := after != w.before
	return &changed
}

// failOver starts the next agent on the work the last one left.
func (sv *supervisor) failOver(ctx context.Context, w *watch, why string) error {
	next := w.remaining[0]

	// The briefing first, because it changes the prompt the options are built
	// from. Best-effort by construction: an agent that died before writing a
	// transcript is the commonest case here, and internal/handoff writes a useful
	// export from the file ledger alone.
	brief := sv.briefing(w)

	req := w.req
	req.Agent, req.Fallback = next, w.remaining[1:]
	if brief != nil {
		req.Prompt = brief.Prompt(w.req.Prompt)
	}

	opts, err := sv.s.buildRunOptions(ctx, req)
	if err != nil {
		return err
	}
	if brief != nil {
		opts.ExtraMounts = append(opts.ExtraMounts, brief.Dir+":"+handoff.GuestDir+":ro")
	}

	// The record. buildRunOptions may have skipped further agents on its own
	// probe, and its reason is kept alongside this one — both are true, and a
	// reader wants the sequence rather than the last link of it.
	opts.RoutedFrom = w.agent
	opts.RouteReason = joinReasons(why, opts.RouteReason)
	opts.RouteID, opts.RouteAttempt = w.routeID, w.attempt+1
	opts.Baseline = baselineFor(opts.Project, opts.Agent)

	restore := sv.handOverName(ctx, w, opts)

	before := sv.fingerprint(opts.Project)
	name, err := sv.s.Session.Start(ctx, opts, false)
	if err != nil {
		restore()
		return err
	}
	log.Printf("sandbox-studio-api: %s %s — started %s instead (route %s, attempt %d)",
		w.agent, why, opts.Agent, w.routeID, w.attempt+1)

	c, err := sv.s.resolveRun(ctx, name)
	if err != nil {
		// Launched but not yet listed. Nothing further can be supervised — the
		// container id is how a watch is addressed — so this attempt runs
		// unwatched rather than being retried into a second container.
		sv.drop(w)
		return nil
	}

	sv.mu.Lock()
	delete(sv.watched, w.container)
	next2 := &watch{
		container: c.ID,
		name:      name,
		req:       w.req, // the original prompt, not the briefed one
		agent:     opts.Agent,
		remaining: remainingAfter(req.Fallback, opts.Agent),
		workspace: opts.Project,
		before:    before,
		routeID:   w.routeID,
		attempt:   w.attempt + 1,
		briefings: w.briefings,
	}
	if brief != nil {
		next2.briefings = append(next2.briefings, brief.Dir)
	}
	sv.watched[c.ID] = next2
	sv.mu.Unlock()
	return nil
}

// handOverName frees the container name the retry is about to ask for, and
// returns the undo.
//
// Only when the retry actually wants it. A detached run in a repository and on a
// branch gets the deterministic sandbox-<repo>-<branch>, which is what enforces
// one-agent-per-branch — docker refuses a duplicate atomically, where a
// list-then-launch check has a window in which two agents pass it. So that name
// has to come back, and the dead attempt is *renamed* rather than removed
// because its logs are the evidence for why the failover happened. A run with no
// branch to name gets a timestamp instead, collides with nothing, and is left
// exactly as it is: renaming it would move a row somebody is reading, to solve a
// problem it does not have.
//
// The undo matters as much as the rename. A container left under a name that
// says it was superseded, when nothing superseded it, is a false record that
// outlives the incident.
func (sv *supervisor) handOverName(ctx context.Context, w *watch, opts sandbox.Options) (restore func()) {
	if w.name == "" || sandbox.ContainerName(opts) != w.name {
		return func() {}
	}
	if err := sv.s.RT.Rename(ctx, w.container, fmt.Sprintf("%s-attempt%d", w.name, w.attempt)); err != nil {
		return func() {}
	}
	return func() { _ = sv.s.RT.Rename(ctx, w.container, w.name) }
}

// briefing exports the finished agent's conversation for the next one, or
// returns nil when there is nowhere to put it.
func (sv *supervisor) briefing(w *watch) *handoff.Export {
	c, err := sv.s.resolveRun(context.Background(), w.container)
	if err != nil {
		return nil
	}
	// The transcript belonging to *this* container, by the same correlation the
	// conversation view uses — sandbox-owned store, started inside the window,
	// prompt matched. It reports nothing rather than a guess, and nothing is the
	// right input here: a briefing assembled from another run's conversation
	// would be confidently wrong in a file the next agent is told to trust.
	path, _, _ := sv.s.transcriptFor(c)

	dir, err := os.MkdirTemp("", "sandbox-handoff-*")
	if err != nil {
		return nil
	}
	ex, err := handoff.Write(dir, w.agent, path, w.workspace, w.req.Base)
	if err != nil {
		os.RemoveAll(dir)
		return nil
	}
	return ex
}

// fingerprint is the workspace as it stands, or "" when it cannot be read.
func (sv *supervisor) fingerprint(workspace string) string {
	if workspace == "" {
		return ""
	}
	t, err := sv.treeOf(workspace)
	if err != nil {
		return ""
	}
	return t
}

// drop stops supervising a run and clears what was held for it.
func (sv *supervisor) drop(w *watch) {
	sv.mu.Lock()
	delete(sv.watched, w.container)
	sv.mu.Unlock()
	for _, dir := range w.briefings {
		os.RemoveAll(dir)
	}
}

// remainingAfter returns the part of a chain that comes after agent.
//
// The chain is re-probed on every attempt, so the agent that starts is not
// always the one at the head of it — an unreachable candidate is skipped before
// a container exists. What is left to try is therefore what follows whoever
// actually started, and an agent that is not in the chain at all leaves nothing.
func remainingAfter(chain []string, agent string) []string {
	for i, name := range chain {
		if name == agent {
			return append([]string(nil), chain[i+1:]...)
		}
	}
	return nil
}

// joinReasons keeps both halves of why a run ended up on this agent.
func joinReasons(parts ...string) string {
	var kept []string
	for _, p := range parts {
		if strings.TrimSpace(p) != "" {
			kept = append(kept, p)
		}
	}
	return strings.Join(kept, "; ")
}

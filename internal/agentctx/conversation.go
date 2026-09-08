package agentctx

import "time"

// The slack around a run's own window, shared so the CLI and the daemon cannot
// name different conversations for the same run.
//
// Generous on the late side and tight on the early one: a transcript's last
// write can land after the manifest is closed, while an earlier conversation in
// the same project would be swept in by any looseness before the start.
const (
	ConversationSlackBefore = 2 * time.Minute
	ConversationSlackAfter  = 15 * time.Minute
)

// ConversationFor finds the transcript belonging to one sandbox run.
//
// The inputs are the three things a run records about itself — which agent, which
// project, and when it was alive — and they are all a rescue manifest or a
// container label has. Nothing here is written; the transcript store belongs to
// the agent.
//
// It lives here because there are two callers and they must not answer
// differently about the same run: `sandbox-cli recover restore` prints a resume
// command, and Studio's restore offers a Continue button, and a user who sees
// one id in the terminal and another in the browser has been told the tool does
// not know. PickSession already shared the *choosing* for the same reason; this
// shares the half that decides which sessions are offered to it, which is where
// the first divergence actually happened.
//
// **Which store, and it is not the obvious one.** The full finding is searched,
// with the project filter, rather than the sandbox-owned store alone. A
// sandbox's transcripts land in the *host* bucket for the project, because the
// claude wrapper mounts that bucket into the container — while the sandbox-owned
// store buckets by the container's own working directory, which is always
// `/workspace`. So narrowing to the sandbox store and filtering by the project
// path is a search that cannot succeed: measured on a real machine, 14 sessions
// under the full store and 0 under the narrowed one. The failure is invisible,
// because "found nothing" is also the honest answer in every case where this
// declines to guess.
//
// The cost of the wider store is the one console.go documents: the user's own
// Claude Code sessions are in there too. What keeps it honest is that the window
// comes from a *finished* run rather than from "now", and that two candidates
// resolve to nothing rather than to the newest.
//
// Returns ok=false — meaning silence, not an error — whenever the answer is not
// clear: no agent (a plain `run` has no conversation), no verified store, no
// resume argv for that agent, or nothing that began inside the window. Resuming
// the wrong conversation is worse than offering none.
func ConversationFor(agent, project string, from, until time.Time) (Finding, Session, bool) {
	if agent == "" || project == "" {
		return Finding{}, Session{}, false
	}
	f, ok := resolveStore(agent)
	if !ok || f.State != StateVerified || len(f.Resume) == 0 {
		return Finding{}, Session{}, false
	}
	// **Two buckets, because the two front ends mount differently.**
	//
	// A run started by the CLI gets the host's history bucket for the project
	// mounted into its HOME, so its transcript lands under the *host path*. A run
	// started by Studio gets no such mount — `studioapi.buildRunOptions` builds
	// none — so its transcript lands under the container's own working directory,
	// which is always `/workspace`.
	//
	// Searching only the host path is therefore not merely incomplete for a
	// Studio run, it is **wrong**: that bucket is where the developer's own
	// Claude Code sessions for the same project live, so the one conversation it
	// can offer is the one that is not the run's. That is the misattribution
	// console.go guards a container against, arriving by a different road.
	//
	// The `/workspace` bucket is shared by every project's Studio runs — the
	// transcripts record no more than that path, which is what PooledSessions
	// exists to say — so it cannot be narrowed further and the window is the only
	// thing separating them. Two candidates resolve to nothing, which is the
	// right answer when nothing can tell them apart.
	byProject, err := listSessions(f, ListOpts{Project: project})
	if err != nil {
		return Finding{}, Session{}, false
	}
	byWorkspace, err := listSessions(f, ListOpts{Project: ContainerWorkspace})
	if err != nil {
		return Finding{}, Session{}, false
	}
	sessions := mergeSessions(byProject, byWorkspace)
	if len(sessions) == 0 {
		return Finding{}, Session{}, false
	}
	// No prompt to disambiguate with — neither a rescue manifest nor this
	// signature carries one — so several sessions in one window yield nothing.
	sess, ok := PickSessionIn(sessions, from, until, "")
	if !ok {
		return Finding{}, Session{}, false
	}
	return f, sess, true
}

// The two lookups are vars so both callers' tests can pin them. Everything else
// is arithmetic on times; these are the only inputs that differ per machine,
// which is the same reason sandbox.hostTimezone is one.
var (
	resolveStore = func(agent string) (Finding, bool) {
		return Resolve(agent, DefaultRoots(), time.Now())
	}
	listSessions = func(f Finding, o ListOpts) ([]Session, error) {
		sessions, _, err := List(f, o)
		return sessions, err
	}
)

// ContainerWorkspace is where every sandbox mounts the project, and so the
// working directory every transcript written *inside* one records.
//
// Exported because it is the second bucket a conversation can be in, and a
// caller correlating a run has to know that "which project" has two answers
// depending on which front end launched it.
const ContainerWorkspace = "/workspace"

// mergeSessions concatenates two listings without repeating a session that is in
// both — the same transcript is reachable under either bucket when a run had the
// history mount, and counting it twice would look like an ambiguity that is not
// there and silence an answer that is.
func mergeSessions(a, b []Session) []Session {
	seen := make(map[string]bool, len(a)+len(b))
	out := make([]Session, 0, len(a)+len(b))
	for _, list := range [][]Session{a, b} {
		for _, s := range list {
			key := s.Path
			if key == "" {
				key = s.ID
			}
			if key == "" || seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, s)
		}
	}
	return out
}

// TimestampsAvailable reports whether this agent's transcripts carry a start
// time, which is what a run's window can be applied to.
//
// Only two formats have a reader (claude-jsonl and codex's rollout JSONL);
// everything else is listed Partial, with an id and file times and nothing more.
// Correlation therefore cannot work for those agents, and this is how a caller
// tells that apart from "looked and found nothing" — the two are the same
// silence otherwise, and only one of them is worth the user's time to
// investigate.
//
// It used to appear to work for them: the window was applied to a transcript's
// mtime, which every listing has. That is the filter console.go documents as
// having matched a two-day-old conversation, so what was lost by moving to start
// times is an answer that was sometimes wrong, not an answer that was right.
func TimestampsAvailable(agent string) bool {
	store, ok := Lookup(agent)
	if !ok {
		return false
	}
	return store.Format == FormatClaudeJSONL || store.Format == FormatCodexRollout
}

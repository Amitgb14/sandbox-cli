package agentctx

import "time"

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
	sessions, err := listSessions(f, ListOpts{Project: project})
	if err != nil || len(sessions) == 0 {
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

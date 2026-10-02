package cli

import (
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agentctx"
	"github.com/Amitgb14/sandbox-cli/internal/rescue"
)

// Recovering the work and recovering the conversation are two different things,
// and `recover` used to speak only about the first. After a crash that is
// backwards: /workspace is a bind mount, so the files are already on disk and
// usually need nothing, while the conversation lives inside the container's HOME
// and is the half that actually goes missing. A user who followed what the tool
// printed got a branch and concluded the session was gone.
//
// So the restore output names the conversation too, when one can be found.

// conversationSlack is how far outside a run's own window a transcript may sit
// and still be considered part of it.
//
// A transcript's mtime is its last write, which lands while the run is alive, so
// the window is generous on the late side (a final write can follow the manifest
// being closed) and tight on the early side, where an earlier conversation in
// the same project would otherwise be swept in.
const (
	conversationSlackAfter  = 15 * time.Minute
	conversationSlackBefore = 2 * time.Minute
)

// The two lookups this needs are vars so tests can pin them. Everything else
// here is arithmetic on times; these are the only inputs that differ per
// machine, which is the same reason sandbox.hostTimezone is a var.
var (
	resolveAgentStore = func(agent string) (agentctx.Finding, bool) {
		return agentctx.Resolve(agent, agentctx.DefaultRoots(), time.Now())
	}
	listAgentSessions = func(f agentctx.Finding, o agentctx.ListOpts) ([]agentctx.Session, error) {
		sessions, _, err := agentctx.List(f, o)
		return sessions, err
	}
)

// conversation is a transcript believed to belong to a rescue session.
type conversation struct {
	agent   string
	session agentctx.Session
	// others is how many more sessions fell in the same window. Reported rather
	// than hidden: picking the newest of several is a guess, and a guess the user
	// can check beats one they cannot see.
	others int
	// resumeArgs is the agent's own resume flag from the verified descriptor,
	// never a hardcoded one.
	resumeArgs []string
}

// resumeCommand is the line to run to get the conversation back.
func (c conversation) resumeCommand() string {
	return fmt.Sprintf("sandbox-cli %s %s %s",
		c.agent, strings.Join(c.resumeArgs, " "), sessionIDCell(c.session.ID, false))
}

// findConversation looks for the transcript belonging to a rescue session.
//
// Correlation is by agent, project and time, all three of which the session
// manifest already records — no new state, and nothing written into the
// transcript store, which belongs to the agent.
//
// Returns ok=false rather than a guess whenever the answer is not clear: no
// agent recorded (a plain `run` has no conversation), no verified store, or
// nothing in the window. Silence is correct there — a wrong resume id sends
// someone into another conversation, which is worse than not offering one.
func findConversation(s rescue.Session) (conversation, bool) {
	if s.Agent == "" || s.Workspace == "" {
		return conversation{}, false
	}
	f, ok := resolveAgentStore(s.Agent)
	if !ok || f.State != agentctx.StateVerified || len(f.Resume) == 0 {
		return conversation{}, false
	}
	sessions, err := listAgentSessions(f, agentctx.ListOpts{Project: s.Workspace})
	if err != nil || len(sessions) == 0 {
		return conversation{}, false
	}

	from := s.StartedAt.Add(-conversationSlackBefore)
	until := s.Activity().Add(conversationSlackAfter)

	var in []agentctx.Session
	for _, sess := range sessions {
		if sess.Modified.Before(from) || sess.Modified.After(until) {
			continue
		}
		in = append(in, sess)
	}
	if len(in) == 0 {
		return conversation{}, false
	}
	// agentctx.List returns newest first, so the first survivor is the one whose
	// last write is closest to the end of the run.
	return conversation{
		agent:      f.Agent,
		session:    in[0],
		others:     len(in) - 1,
		resumeArgs: f.Resume,
	}, true
}

// reportConversation prints how to get the conversation back, or — when it
// cannot be found — says that it looked, which is the difference between "there
// is nothing" and "we never mentioned it".
func reportConversation(s rescue.Session) {
	if s.Agent == "" {
		return // a plain `run`: there is no conversation to resume
	}
	c, ok := findConversation(s)
	if !ok {
		fmt.Fprintf(os.Stderr, "  The %s conversation from this run could not be located.\n", s.Agent)
		// Runs from before the per-project history mount was fixed all landed in
		// one shared bucket and cannot be attributed to a project, so the
		// correlation above will never find them. Pointing straight at that bucket
		// turns a two-step hunt into one command — and this is exactly the case
		// the issue was filed from.
		if f, found := resolveAgentStore(s.Agent); found {
			if _, n := agentctx.PooledSessions(f); n > 0 {
				fmt.Fprintf(os.Stderr, "  %d session(s) predate per-project history and are not attributable to a project;\n", n)
				fmt.Fprintf(os.Stderr, "  if this run is among them, find it with:\n")
				fmt.Fprintf(os.Stderr, "    sandbox-cli %s context list --project /workspace\n", s.Agent)
				return
			}
		}
		fmt.Fprintf(os.Stderr, "  Look for it with:\n")
		fmt.Fprintf(os.Stderr, "    sandbox-cli %s context list\n", s.Agent)
		return
	}
	fmt.Fprintf(os.Stderr, "  Resume the conversation:  %s\n", c.resumeCommand())
	if c.others > 0 {
		fmt.Fprintf(os.Stderr, "    (%d other %s session(s) overlap this run; `sandbox-cli %s context list` shows them)\n",
			c.others, s.Agent, s.Agent)
	}
}

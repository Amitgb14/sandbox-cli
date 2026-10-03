// Package agentstate says how an agent is doing — working, waiting for you,
// idle, finished — from evidence rather than from prose. Ported from beta.15's
// line after 0.0.1 (_old/internal/detect), where it answered "which of my
// agents is waiting for me"; here the evidence comes over the API.
//
// # What it will not do
//
// It does not match prose. Reading the last assistant message for "Do you want
// to proceed?" or "[y/n]" is a table of claims about other people's products
// that can be neither completed nor kept current, and a vendor rewording one
// would turn a confident `blocked` into a confident lie. Somebody told
// `unknown` goes and looks; somebody told `blocked` wrongly waits on nothing.
//
// # What it does instead
//
// Three structural facts, none of which is anybody's wording:
//
//   - **Who spoke last.** agentctx keeps only prompts somebody typed and what
//     the agent said. If the last turn is the user's, the agent owes an answer.
//   - **How long ago.** A conversation that grew recently is an agent working.
//   - **Whether anyone can answer.** A process with a terminal is a console: an
//     agent gone quiet there is waiting for somebody. A headless one has no
//     keyboard, so quiet there is idle and nothing more.
package agentstate

import (
	"context"
	"sort"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agentctx"
	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// State is how an agent is doing.
type State string

const (
	Unknown   State = "unknown"
	Working   State = "working"
	Blocked   State = "blocked" // quiet, with a terminal somebody can type at
	Idle      State = "idle"    // quiet, and nothing can type at it
	Done      State = "done"    // its process exited 0
	Failed    State = "failed"  // its process exited non-zero
	Suspended State = "suspended"
	Stopped   State = "stopped" // the sandbox is gone
)

// Quiet is how long a conversation must go without growing before the agent
// is taken to have stopped doing something. A trade, not a measurement: too
// short and an agent thinking between tool calls reads as waiting; too long
// and somebody stares at `working` while the agent stares back. Generous
// towards `working`, because telling someone an agent needs them when it does
// not is the error that wastes their time.
const Quiet = 30 * time.Second

// Evidence is what is known about one agent, without deciding anything: so
// the deciding is testable without a sandbox.
type Evidence struct {
	// SandboxState is the sandbox's: running, suspended, terminated.
	SandboxState string
	// Process is the agent's process, nil when there is none.
	Process *api.Process
	// Transcript is the agent's conversation, newest last; empty when there is
	// none to read (no verified format, nothing written yet), which is a reason
	// to answer Unknown rather than to guess.
	Transcript []agentctx.Message
	Now        time.Time
}

// From decides. The order is the order of certainty: what the sandbox and the
// process prove first, because an exit code is a fact and a transcript an
// inference; then the conversation; and Unknown where the evidence runs out.
func From(e Evidence) State {
	switch e.SandboxState {
	case api.StateTerminated:
		return Stopped
	case api.StateSuspended:
		return Suspended
	case api.StateRunning:
	default:
		return Unknown
	}
	if e.Process == nil {
		return Unknown
	}
	if e.Process.State == api.ProcessExited {
		if e.Process.ExitCode != nil && *e.Process.ExitCode == 0 {
			return Done
		}
		return Failed
	}

	last, ok := lastTurn(e.Transcript)
	if !ok {
		// Usually an agent that has not written its first turn yet, which is
		// indistinguishable here from one whose format is not read.
		return Unknown
	}
	if last.Role == "user" {
		// A prompt with nothing after it: the agent owes an answer, whether for
		// two seconds or two hours. "Thinking" and "hung" are the same evidence.
		return Working
	}
	if last.At.IsZero() {
		return Unknown
	}
	// A turn in the future is a clock disagreement between host and guest,
	// treated as fresh rather than as evidence of anything.
	if since := e.Now.Sub(last.At); since < Quiet {
		return Working
	}
	if e.Process.Tty {
		return Blocked
	}
	return Idle
}

func lastTurn(msgs []agentctx.Message) (agentctx.Message, bool) {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != "" {
			return msgs[i], true
		}
	}
	return agentctx.Message{}, false
}

// Describe says why a state was reported, in the words a person reads.
func Describe(s State) string {
	switch s {
	case Working:
		return "the conversation is still moving, or the agent owes an answer to the last prompt"
	case Blocked:
		return "the agent spoke last and has been quiet since, and it has a terminal — so it is waiting for somebody to answer"
	case Idle:
		return "the agent spoke last and has been quiet since, and nothing can type at it"
	case Done:
		return "its process exited 0"
	case Failed:
		return "its process exited non-zero"
	case Suspended:
		return "the sandbox is suspended"
	case Stopped:
		return "the sandbox is gone"
	}
	return "there is not enough to say — no transcript the agent's format can be read from, or nothing written yet"
}

// Report is one sandbox's agent and how it is doing.
type Report struct {
	Sandbox string
	Name    string
	Agent   string
	PID     int
	State   State
}

// AgentLabel is the label a run puts on a sandbox it started an agent in.
const AgentLabel = "agent"

// Look gathers the evidence for one sandbox over the API and decides.
//
// The agent is the sandbox's first process: a run starts it first, and what
// sandbox-cli runs beside it later — checkpoints, bring-back — comes after. A
// sandbox with no agent label is not an agent run, and is Unknown rather than
// read as one.
func Look(ctx context.Context, c *api.Client, sb api.Sandbox, now time.Time) Report {
	r := Report{Sandbox: sb.ID, Name: sb.Name, Agent: sb.Labels[AgentLabel], State: Unknown}
	e := Evidence{SandboxState: sb.State, Now: now}
	if sb.State == api.StateRunning {
		if ps, err := c.Processes(ctx, sb.ID); err == nil && len(ps) > 0 {
			sort.Slice(ps, func(i, j int) bool { return ps[i].PID < ps[j].PID })
			e.Process = &ps[0]
			r.PID = ps[0].PID
		}
		if e.Process != nil && e.Process.State == api.ProcessRunning && r.Agent != "" {
			e.Transcript = ReadTranscript(ctx, c, sb.ID, r.Agent)
		}
	}
	if r.Agent == "" && sb.State == api.StateRunning {
		return r
	}
	r.State = From(e)
	return r
}

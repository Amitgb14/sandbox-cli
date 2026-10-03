// Package detect decides how an agent is doing, from evidence rather than from
// prose.
//
// The catalog can already say what a *container* is: created, running, exited with
// a code, gone. What it cannot say is the thing somebody running six agents
// actually wants to know — which of them is waiting for me. An agent editing a file
// and an agent parked at a permission prompt are the same running container, so
// `internal/session` reports both as `unknown`, deliberately, and this package is
// where that stops being the only honest answer.
//
// # What it will not do
//
// It does not match prose. The obvious implementation reads the last assistant
// message and looks for "Do you want to proceed?", "[y/n]", "May I", and similar —
// and that is `internal/creds`' prefix table all over again: a lookup against
// claims about other people's products, which can be neither completed nor kept
// current. Worse here than there, because `creds` reports the evidence ("begins
// with `ghp_`") while this would report a *conclusion*, so a vendor rewording a
// prompt turns a confident `blocked` into a confident lie. A user waiting on an
// agent that is not asking is the failure; a user told `unknown` goes and looks.
//
// # What it does instead
//
// Three structural facts, none of which is anybody's wording:
//
//   - **Who spoke last.** A transcript is append-only, and `agentctx` already
//     distinguishes a prompt somebody typed from a tool result coming back as a
//     user message. If the last turn is the user's, the agent owes an answer.
//   - **How long ago.** A transcript being appended to is an agent working. One
//     that has not grown is an agent that has stopped doing something.
//   - **Whether anyone can answer.** A pane with an open stdin is a console: a
//     human can type at it, and an agent that has gone quiet there is waiting for
//     one. A headless pane has no keyboard, so "waiting for a human" is not a
//     state it can be in — quiet there is quiet, and nothing more.
//
// The better signal, not available yet: a claude transcript records `tool_use`
// blocks, and one with no matching `tool_result` is an agent waiting — on the tool,
// or on somebody approving it — which is structural and exact. `agentctx.Message`
// drops both (it carries what was *said*, because that is the half that can contain
// a question), so reaching it means either a second parser here or new shape there.
// Neither is worth doing before something needs the precision.
package detect

import (
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agentctx"
	"github.com/Amitgb14/sandbox-cli/internal/protocol"
)

// Quiet is how long a transcript must go without growing before the agent is
// taken to have stopped doing something.
//
// Thirty seconds, and the number is a trade rather than a measurement. Too short
// and an agent thinking between tool calls reads as waiting; too long and somebody
// stares at `working` while the agent stares back. It is generous towards
// `working` on purpose: reporting an agent as needing attention when it does not is
// the error that wastes the user's time, and the opposite error costs them a later
// glance at a listing they were going to look at anyway.
const Quiet = 30 * time.Second

// Evidence is what a caller knows about a pane without having looked at its
// conversation.
//
// Deliberately a struct of facts rather than a container or a pane: this package
// decides, and the deciding should be testable without an engine, a catalog or a
// file. `From` is the only function, and these are all it gets.
type Evidence struct {
	// ContainerState is the engine's own word — "running", "created", "exited",
	// "paused", "" when unreadable.
	ContainerState string
	// ExitCode is meaningful once the container has exited.
	ExitCode int
	// OpenStdin is whether anything can type at this pane. It is what separates
	// "waiting for a human" from "quiet": a console run has a keyboard, a headless
	// run does not, and an agent cannot be blocked on input nobody can give.
	OpenStdin bool

	// Transcript is the tail of the agent's conversation, newest last, as
	// agentctx reads it. Empty when there is none to read — no agent, no verified
	// store, a format with no reader — and that is a reason to answer `unknown`
	// rather than to guess.
	Transcript []agentctx.Message

	// Now is the clock, injected so the tests do not race it.
	Now time.Time
}

// From decides a pane's state.
//
// The order is the order of certainty. What the container proves comes first,
// because an exit code is a fact and a transcript is an inference; within the
// running cases, the transcript decides; and anything the evidence does not reach
// is `unknown`, which is an answer rather than a gap.
func From(e Evidence) protocol.PaneState {
	// 1. The container, which can settle the question outright.
	switch e.ContainerState {
	case "created":
		return protocol.StateStarting
	case "exited", "dead":
		if e.ExitCode == 0 {
			return protocol.StateDone
		}
		return protocol.StateFailed
	case "running", "restarting", "paused", "removing":
		// Keep going: the container is there, and what the *agent* is doing is the
		// question this package exists for.
	default:
		// An unreadable state is not a licence to infer anything — the rule `clean`
		// already states. Not knowing what the container is doing means not knowing
		// what the agent is.
		return protocol.StateUnknown
	}

	// 2. The conversation.
	last, ok := lastTurn(e.Transcript)
	if !ok {
		// Nothing to read. A container that is up with no transcript is usually one
		// whose agent has not written its first turn yet, which is indistinguishable
		// here from an agent whose store is not verified.
		return protocol.StateUnknown
	}

	if last.At.IsZero() {
		// A turn with no timestamp, which `agentctx` yields for formats it reads
		// without one. Who spoke last is still known, and that alone settles the one
		// case it can: the agent owes an answer.
		if last.Role == "user" {
			return protocol.StateWorking
		}
		return protocol.StateUnknown
	}

	// A turn in the future is a clock disagreement — the host's and the
	// container's, or a transcript written through a timezone mix-up. Treated as
	// fresh rather than as evidence of anything, because the alternative is
	// reporting an agent idle because somebody's clock is ahead.
	since := e.Now.Sub(last.At)
	if since < 0 {
		since = 0
	}

	if last.Role == "user" {
		// A prompt somebody typed, with nothing after it. The agent owes an answer,
		// whether it has been two seconds or two hours: "thinking" and "hung" are
		// the same evidence, and this package does not guess between them.
		return protocol.StateWorking
	}

	if since < Quiet {
		// The agent spoke recently. Mid-answer, or between tool calls.
		return protocol.StateWorking
	}

	// Quiet, with the agent having spoken last. Whether that means "waiting for
	// you" depends entirely on whether there is a you to wait for.
	if e.OpenStdin {
		return protocol.StateBlocked
	}
	return protocol.StateIdle
}

// lastTurn is the newest message, skipping ones with nothing in them.
//
// Empty turns are skipped rather than trusted because a transcript is appended to
// while it is read: the last line is routinely half-written, and `agentctx` already
// drops what it cannot parse. What is left could still be a turn whose text was
// entirely a tool call, which says nothing about who is waiting.
func lastTurn(msgs []agentctx.Message) (agentctx.Message, bool) {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "" {
			continue
		}
		return msgs[i], true
	}
	return agentctx.Message{}, false
}

// Describe says why a state was reported, in the words a person reads.
//
// Separate from `From` because a state and an explanation are different jobs — the
// same split `internal/fleet`'s `branchRefusal` makes between classifying an error
// and explaining it. A listing shows the state; somebody asking "why does it say
// that" gets this.
func Describe(s protocol.PaneState) string {
	switch s {
	case protocol.StateStarting:
		return "the container has been created and has not started yet"
	case protocol.StateWorking:
		return "the conversation is still moving, or the agent owes an answer to the last prompt"
	case protocol.StateBlocked:
		return "the agent spoke last and has been quiet since, and this pane has a console — so it is waiting for somebody to answer"
	case protocol.StateIdle:
		return "the agent spoke last and has been quiet since, and nothing can type at this pane"
	case protocol.StateDone:
		return "the container exited 0"
	case protocol.StateFailed:
		return "the container exited non-zero"
	case protocol.StateStopped:
		return "the container is gone; the pane is what is left of it"
	}
	return "there is not enough to say — no transcript, no timestamps, or a container state the engine did not report"
}

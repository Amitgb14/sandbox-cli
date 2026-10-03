package studioapi

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agentctx"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

// The console: reading what a running agent said, and answering it.
//
// Two halves, deliberately different mechanisms, because they are answering
// different questions.
//
// *Reading* comes from the agent's transcript, not from the container's output.
// A console run starts the agent's interactive UI, which is a full-screen TUI:
// its stdout is a stream of cursor moves and repaints, and text scraped out of
// it mid-redraw looks like an answer without being one. The transcript is the
// same conversation as structured data, and it is already written per turn
// while the run is in flight (measured: it grew 121KB → 150KB across a run
// whose stdout stayed empty).
//
// *Answering* goes to the container's stdin over the engine's API socket
// (runtime.ConsoleWrite), because that is the only path that exists — the
// docker CLI's `attach` refuses when the client has no tty, and a web server
// never will.
//
// The raw stream is exposed too, for the terminal view that renders the TUI
// properly. Both readers are safe to run at once: neither holds state on the
// server, so a browser tab that vanishes leaves nothing to reap.

// conversationSlack widens the window a run's transcript is looked for in.
//
// The agent writes its first line a moment after the container starts, and its
// last a moment before the process ends; matching exactly would miss both ends.
// Kept small on purpose — the window is the only thing separating this run's
// conversation from the one before it in the same pooled directory.
const conversationSlack = 2 * time.Minute

// maxConversationTurns bounds a response. A long session is thousands of turns
// and the console shows the recent end of it; the whole thing is what
// `sandbox-cli context list` and a resume are for.
const maxConversationTurns = 200

// maxConsoleInput bounds one keystroke delivery. This is a person typing, not a
// file upload — and stdin goes to an agent that acts on what it reads.
const maxConsoleInput = 64 * 1024

// handleRunConversation is GET /v1/runs/{id}/conversation.
func (s *Server) handleRunConversation(w http.ResponseWriter, r *http.Request) {
	c, err := s.resolveRun(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}

	path, resume, ok := s.transcriptFor(c)
	if !ok {
		// Not an error: a run with no agent has no transcript, and a claude run
		// has none until it has written its first turn. Both are "nothing to show
		// yet", which an empty conversation says without inventing a reason.
		writeJSON(w, http.StatusOK, ConversationResponse{Messages: []agentctx.Message{}})
		return
	}
	format := ""
	if store, ok := agentctx.Lookup(c.Labels[sandbox.LabelAgent]); ok {
		format = store.Format
	}
	msgs, err := agentctx.TranscriptOf(format, path, maxConversationTurns)
	if err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	if msgs == nil {
		msgs = []agentctx.Message{}
	}
	writeJSON(w, http.StatusOK, ConversationResponse{
		Messages:  msgs,
		SessionID: sessionIDFromPath(path),
		Resume:    resume,
		// Whether anything can be typed back. A finished run still has a readable
		// conversation, and saying so is what stops the UI offering a reply box
		// that would 409.
		Writable: c.Running() && c.OpenStdin,
	})
}

// transcriptFor finds the transcript belonging to a run.
//
// Two filters, and the first one is not optional. The claude wrapper genuinely
// has *two* verified stores — the user's own ~/.claude history, and the
// sandbox-owned agent HOME that gets mounted into containers — and only the
// second can hold a transcript a container wrote. Matching across both put the
// developer's own live Claude Code session, which is by definition the most
// recently modified transcript on the machine, on the screen of a sandbox run
// that had just started. Observed, not theorised.
//
// The second filter is the run's time window, and it is applied to when the
// session *began*. Modified alone is what let a two-day-old conversation match:
// a session still being appended to has a recent mtime and says nothing about
// which run it belongs to. A session that started before the container did
// cannot be that container's.
//
// When nothing survives both, this reports *nothing* rather than the closest
// candidate. Showing somebody another run's conversation — and putting a reply
// box under it, wired to a real agent's stdin — is worse than showing none.
func (s *Server) transcriptFor(c runtime.ContainerInfo) (path, resume string, ok bool) {
	agent := c.Labels[sandbox.LabelAgent]
	if agent == "" {
		return "", "", false
	}
	f, ok := agentctx.Resolve(agent, agentctx.DefaultRoots(), time.Now())
	if !ok || f.State != agentctx.StateVerified {
		return "", "", false
	}
	// Only the sandbox-owned store. A container's HOME is that directory and
	// nothing else, so a transcript anywhere but here was written by something
	// that is not this run.
	f = sandboxStore(f)
	if f.Dir == "" {
		return "", "", false
	}
	sessions, _, err := agentctx.List(f, agentctx.ListOpts{})
	if err != nil {
		return "", "", false
	}

	// A run started by resuming names its conversation outright, so there is
	// nothing to infer. This is not an optimisation: every heuristic below
	// assumes a session began around the time its container did, and a resumed
	// one began before — so without this a resumed run reports no conversation
	// at all, which is what it did the first time it was tried.
	if id := c.Labels[sandbox.LabelSession]; id != "" {
		for _, sess := range sessions {
			if sess.ID != id {
				continue
			}
			return sess.Path, resumeCommand(f, id), true
		}
		// Named a session that is not in this store. Reporting nothing beats
		// falling through to a guess: the run said which conversation it is.
		return "", "", false
	}

	from := c.StartedAt.Add(-conversationSlack)
	// A running container has no finish time; "now" is the right end of its
	// window, and the slack covers a transcript written a moment after this read.
	until := time.Now().Add(conversationSlack)
	if !c.Running() && !c.FinishedAt.IsZero() {
		until = c.FinishedAt.Add(conversationSlack)
	}

	path, ok = agentctx.PickSession(sessions, from, until, c.Labels[sandbox.LabelPrompt])
	if !ok {
		return "", "", false
	}
	return path, resumeCommand(f, sessionIDFromPath(path)), true
}

// resumeCommand is the line to type on the host to carry this conversation on.
//
// --no-sync is not optional and is the whole reason this is built here rather
// than assembled by a client from an id. A Studio run's transcript lives in the
// sandbox-owned agent HOME under the pooled `-workspace` bucket, and the claude
// wrapper's default history mount puts the *host's* per-project bucket over
// exactly that path — so the session is real, the id is right, and the plain
// command answers "No conversation found with session ID". Measured both ways:
// without the flag it fails, with it the agent resumed and answered a question
// about the earlier turn.
//
// The resume flag itself comes from the verified descriptor, never a hardcoded
// one, the same rule cli/recover_resume.go keeps.
func resumeCommand(f agentctx.Finding, id string) string {
	if id == "" || len(f.Resume) == 0 {
		return ""
	}
	parts := []string{"sandbox-cli", f.Agent}
	if f.Agent == "claude" {
		parts = append(parts, "--no-sync")
	}
	parts = append(parts, f.Resume...)
	return strings.Join(append(parts, id), " ")
}

// sessionIDFromPath reads the session id off the transcript's filename.
//
// Whole, never abbreviated: the listing prints ids short for reading, and
// Claude Code rejects anything that is not a complete UUID.
func sessionIDFromPath(path string) string {
	// Delegated rather than reimplemented. Which part of a file name is the id is
	// a fact about each agent's store, which is agentctx's job — and the copy
	// that used to live here was the claude-shaped half of it, so a codex run
	// reported `rollout-<timestamp>-<uuid>` as its session id and built a resume
	// command the agent would refuse.
	return agentctx.SessionIDFromPath(path)
}

// sandboxStore narrows a Finding to the location under the sandbox-owned agent
// HOME — the one that is actually mounted into a container.
//
// Returns a Finding with an empty Dir when there is no such location, which is
// a real state rather than an error: an agent run with --no-persist-auth has a
// HOME that went away with the container, so its transcript did too.
func sandboxStore(f agentctx.Finding) agentctx.Finding {
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

// handleRunConsoleInput is POST /v1/runs/{id}/console/input.
func (s *Server) handleRunConsoleInput(w http.ResponseWriter, r *http.Request) {
	// The one endpoint that refuses to work unauthenticated, whatever the rest of
	// the server is doing.
	//
	// Everything else here is read-only or launches a container the caller could
	// have launched anyway (POST /runs already takes an arbitrary argv, so this
	// is not a new class of reach). What is new is a keyboard on a session that
	// is *already running* — one holding a workspace and, under dev's defaults,
	// an OAuth refresh token in the agent's HOME. A token is a one-word flag; an
	// unauthenticated keyboard is not something to hand out because somebody
	// forgot one.
	if s.Token == "" {
		writeError(w, http.StatusForbidden, fmt.Errorf(
			"typing at a running agent requires the server to have a -token set; "+
				"start sandbox-studio-api with -token (or $SANDBOX_STUDIO_TOKEN) to enable the console"))
		return
	}

	c, err := s.resolveRun(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if !c.Running() {
		writeError(w, http.StatusConflict, fmt.Errorf("%s is not running, so there is nothing listening", shortID(c.ID)))
		return
	}
	if !c.OpenStdin {
		// The run was launched without a console. Nothing to do about it now —
		// stdin is fixed at create time — so say what would have made it work.
		writeError(w, http.StatusConflict, fmt.Errorf(
			"this run has no console: it was launched without \"console\": true, "+
				"so the container was created with no stdin and cannot be typed at"))
		return
	}

	var req ConsoleInputRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxConsoleInput)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid body: %w", err))
		return
	}
	if req.Data == "" {
		writeError(w, http.StatusBadRequest, fmt.Errorf("data is required"))
		return
	}

	data := req.Data
	if req.Enter {
		// Carriage return, not newline: the agent's stdin is a pty in raw mode,
		// where Enter is what a terminal actually sends (\r). A \n arrives as a
		// literal line feed and a TUI reading key events does not treat it as
		// submit — the message appears in the box and simply sits there.
		data += "\r"
	}

	if err := s.RT.ConsoleWrite(r.Context(), c.ID, []byte(data)); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRunConsoleResize is POST /v1/runs/{id}/console/resize.
//
// A terminal emulator knows its own size and the container does not, and until
// it is told, a full-screen agent renders nothing — so this is what turns an
// attached console from a blank rectangle into the agent's interface. Same
// token rule as input: it drives the session rather than reading it.
func (s *Server) handleRunConsoleResize(w http.ResponseWriter, r *http.Request) {
	if s.Token == "" {
		writeError(w, http.StatusForbidden, fmt.Errorf(
			"driving a running agent's console requires the server to have a -token set"))
		return
	}
	c, err := s.resolveRun(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if !c.Running() || !c.TTY {
		writeError(w, http.StatusConflict, fmt.Errorf("%s has no terminal to resize", shortID(c.ID)))
		return
	}
	var req ConsoleResizeRequest
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1024)).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, fmt.Errorf("invalid body: %w", err))
		return
	}
	if err := s.RT.ConsoleResize(r.Context(), c.ID, req.Rows, req.Cols); err != nil {
		writeError(w, http.StatusBadGateway, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// handleRunConsoleStream is GET /v1/runs/{id}/console — the raw output stream,
// for a client that renders a terminal rather than a conversation.
func (s *Server) handleRunConsoleStream(w http.ResponseWriter, r *http.Request) {
	c, err := s.resolveRun(r.Context(), r.PathValue("id"))
	if err != nil {
		writeError(w, http.StatusNotFound, err)
		return
	}
	if !c.Running() {
		writeError(w, http.StatusConflict, fmt.Errorf("%s is not running", shortID(c.ID)))
		return
	}
	flusher, ok := w.(http.Flusher)
	if !ok {
		writeError(w, http.StatusInternalServerError, fmt.Errorf("streaming not supported by this connection"))
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	// Base64 rather than the raw bytes: this is a pty stream carrying escape
	// sequences and partial UTF-8 runes, and SSE is a line protocol. Encoding
	// removes both hazards — an embedded newline cannot end the event early, and
	// a rune split across two reads survives to be joined by the client.
	err = s.RT.ConsoleStream(r.Context(), c.ID, &sseBase64Writer{w: w, flusher: flusher})
	if err != nil && r.Context().Err() == nil {
		writeSSE(w, "error", ErrorResponse{Error: err.Error()})
		flusher.Flush()
	}
}

// sseBase64Writer frames raw pty bytes as SSE events.
//
// Base64 rather than text, because this stream is neither: it carries escape
// sequences, and a read can split a UTF-8 rune or land mid-sequence. Encoding
// each chunk means the transport never has to understand the payload — the
// client joins the pieces and hands them to a terminal emulator, which is the
// only thing that should be interpreting them.
type sseBase64Writer struct {
	w       http.ResponseWriter
	flusher http.Flusher
}

func (sw *sseBase64Writer) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	writeSSE(sw.w, "output", base64.StdEncoding.EncodeToString(p))
	sw.flusher.Flush()
	return len(p), nil
}

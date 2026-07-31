package studioapi

import (
	"net/http"
	"strings"
	"testing"
)

// A console run must differ from a headless one in exactly two ways: the
// container keeps a terminal, and the agent runs its interactive argv. Either
// alone is useless — a keyboard wired to `claude -p` is answering a question
// nothing will ask, and an interactive agent with no stdin waits on a terminal
// that was never created.
func TestConsoleRunIsInteractiveAndKeepsATerminal(t *testing.T) {
	s, fr := newTestServer(t)
	rec := doRequest(t, s.Handler(), http.MethodPost, "/v1/runs", RunCreateRequest{
		Agent:   "claude",
		Prompt:  "have a look and ask me if anything is unclear",
		Branch:  "feature-x",
		Console: true,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if len(fr.started) != 1 {
		t.Fatalf("started %d containers, want 1", len(fr.started))
	}
	started := fr.started[0]
	if !started.Detach {
		t.Error("a console run is still detached: it is launched from one window and attached from another")
	}
	// Console is an Options concept; by the time it reaches a RunSpec it has
	// resolved into the TTY that BuildArgs renders as -dit. Asserting on the
	// resolved value rather than the request is the point — it is the half that
	// actually creates something to type at.
	if !started.TTY {
		t.Error("a console run must keep a terminal, or attach has no keyboard")
	}
	argv := strings.Join(started.Command, " ")
	// -p is the headless mode. It runs the prompt to completion and exits, so an
	// agent started that way can never stop to ask anything.
	for _, headless := range []string{" -p ", "--print"} {
		if strings.Contains(argv, headless) {
			t.Errorf("console run must not use the headless argv, got %q", argv)
		}
	}
	// The prompt seeds the first turn rather than being the whole run.
	if !strings.HasSuffix(argv, "have a look and ask me if anything is unclear") {
		t.Errorf("prompt should seed the interactive session, got %q", argv)
	}
}

// Without a prompt a console run is just the agent, waiting for whatever gets
// typed at it.
func TestConsoleRunWithoutAPromptIsBareAgent(t *testing.T) {
	s, fr := newTestServer(t)
	rec := doRequest(t, s.Handler(), http.MethodPost, "/v1/runs", RunCreateRequest{
		Agent: "claude", Branch: "feature-x", Console: true,
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201: %s", rec.Code, rec.Body.String())
	}
	if argv := strings.Join(fr.started[0].Command, " "); !strings.HasSuffix(argv, "claude") {
		t.Errorf("expected the bare interactive argv, got %q", argv)
	}
}

// The two refusals. Both are about a request that cannot mean what it says
// rather than about safety, so they refuse instead of picking a half.
func TestConsoleRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  RunCreateRequest
		want string
	}{
		{
			// verify's exit code is the container's, which is how `land` decides
			// the work is done. An interactive session's exit code is whenever
			// somebody quit, which answers a different question.
			name: "with verify",
			req:  RunCreateRequest{Agent: "claude", Console: true, Verify: "go test ./...", Branch: "x"},
			want: "verify",
		},
		{
			// Console swaps an agent out of headless mode. A plain command has no
			// headless mode to swap out of — it is the argv the caller wrote.
			name: "without an agent",
			req:  RunCreateRequest{Command: []string{"bash"}, Console: true, Branch: "x"},
			want: "console needs an agent",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, fr := newTestServer(t)
			rec := doRequest(t, s.Handler(), http.MethodPost, "/v1/runs", tc.req)
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tc.want) {
				t.Errorf("error should mention %q, got %s", tc.want, rec.Body.String())
			}
			if len(fr.started) != 0 {
				t.Error("a refused request must not have started a container")
			}
		})
	}
}

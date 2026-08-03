package studioapi

import (
	"net/http"
	"testing"
)

// The console's reply path, and the three things it refuses.
func TestConsoleInputRequiresAToken(t *testing.T) {
	s, fr := newTestServer(t)
	s.Token = "" // the default, and the reason this check exists
	launch(t, s, RunCreateRequest{Agent: "claude", Prompt: "go", Branch: "feature-x", Console: true})
	rec := doRequest(t, s.Handler(), http.MethodPost, "/v1/runs/feature-x/console/input",
		ConsoleInputRequest{Data: "hello", Enter: true})
	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	if len(fr.consoleWrites) != 0 {
		t.Error("nothing may reach a container's stdin without a token")
	}
}

func TestConsoleInputRefusesARunWithNoConsole(t *testing.T) {
	s, fr := newTestServer(t)
	s.Token = "t"
	// A normal headless run: launched without a console, so no stdin.
	launch(t, s, RunCreateRequest{Agent: "claude", Prompt: "go", Branch: "feature-x"})
	rec := doRequestAuthed(t, s.Handler(), http.MethodPost, "/v1/runs/feature-x/console/input", "t",
		ConsoleInputRequest{Data: "hello", Enter: true})
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409: %s", rec.Code, rec.Body.String())
	}
	if len(fr.consoleWrites) != 0 {
		t.Error("a container created without stdin must not be written to")
	}
}

func TestConsoleInputAppendsTheCarriageReturnThatSubmits(t *testing.T) {
	s, fr := newTestServer(t)
	s.Token = "t"
	launch(t, s, RunCreateRequest{Agent: "claude", Prompt: "go", Branch: "feature-x", Console: true})

	rec := doRequestAuthed(t, s.Handler(), http.MethodPost, "/v1/runs/feature-x/console/input", "t",
		ConsoleInputRequest{Data: "use the other approach", Enter: true})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if len(fr.consoleWrites) != 1 {
		t.Fatalf("wrote %d times, want 1", len(fr.consoleWrites))
	}
	// \r, not \n: the pty is in raw mode and a line feed is not a submit.
	if got, want := fr.consoleWrites[0], "use the other approach\r"; got != want {
		t.Errorf("wrote %q, want %q", got, want)
	}
}

// Without Enter the bytes go as-is, so a client can send a partial line or a
// control character (Ctrl-C, an arrow key) without a newline being invented.
func TestConsoleInputWithoutEnterSendsExactlyWhatWasGiven(t *testing.T) {
	s, fr := newTestServer(t)
	s.Token = "t"
	launch(t, s, RunCreateRequest{Agent: "claude", Prompt: "go", Branch: "feature-x", Console: true})

	rec := doRequestAuthed(t, s.Handler(), http.MethodPost, "/v1/runs/feature-x/console/input", "t",
		ConsoleInputRequest{Data: "\x03"})
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", rec.Code, rec.Body.String())
	}
	if fr.consoleWrites[0] != "\x03" {
		t.Errorf("wrote %q, want a bare ETX", fr.consoleWrites[0])
	}
}

// launch starts a run through the real create path, so a fixture container is
// whatever the API would actually have produced for that request.
func launch(t *testing.T, s *Server, req RunCreateRequest) {
	t.Helper()
	// Authed when the server has a token, since these tests set one precisely to
	// reach the console — the fixture must get past the same gate everything else
	// does.
	rec := doRequestAuthed(t, s.Handler(), http.MethodPost, "/v1/runs", s.Token, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("launching the fixture run: %d %s", rec.Code, rec.Body.String())
	}
}

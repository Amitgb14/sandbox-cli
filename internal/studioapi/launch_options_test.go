package studioapi

import (
	"net/http"
	"strings"
	"testing"
)

// Skip-permissions is opt-in on a console run, and off is the default: an
// interactive session is where being asked is the point.
func TestConsoleAsksUnlessSkipPermissionsIsRequested(t *testing.T) {
	for _, tc := range []struct {
		name string
		skip bool
		want bool
	}{
		{"default", false, false},
		{"requested", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, fr := newTestServer(t)
			launch(t, s, RunCreateRequest{
				Agent: "claude", Prompt: "go", Branch: "feature-x",
				Console: true, SkipPermissions: tc.skip,
			})
			argv := strings.Join(fr.started[0].Command, " ")
			if got := strings.Contains(argv, "--dangerously-skip-permissions"); got != tc.want {
				t.Errorf("skip flag present = %v, want %v (argv %q)", got, tc.want, argv)
			}
		})
	}
}

// A headless run has it regardless — an agent that stops to ask does not fail,
// it hangs — so the request field changes nothing there.
func TestHeadlessAlwaysSkipsPermissions(t *testing.T) {
	s, fr := newTestServer(t)
	launch(t, s, RunCreateRequest{Agent: "claude", Prompt: "go", Branch: "feature-x"})
	if argv := strings.Join(fr.started[0].Command, " "); !strings.Contains(argv, "--dangerously-skip-permissions") {
		t.Errorf("a headless run must skip permissions, got %q", argv)
	}
}

// Resume carries the agent's own flag and the whole id, and replaces the prompt
// rather than joining it.
func TestResumeBuildsTheAgentsOwnResumeArgv(t *testing.T) {
	s, fr := newTestServer(t)
	const id = "0c41de5c-6302-472a-ba36-fe0f82a66295"
	launch(t, s, RunCreateRequest{
		Agent: "claude", Branch: "feature-x", Console: true, Resume: id,
	})
	argv := strings.Join(fr.started[0].Command, " ")
	if !strings.Contains(argv, "--resume "+id) {
		t.Errorf("expected the agent's resume flag and the whole id, got %q", argv)
	}
	if strings.Contains(argv, " -p ") {
		t.Errorf("resume must not be headless, got %q", argv)
	}
}

// The refusals. Each is a request that cannot mean what it says.
func TestLaunchOptionRefusals(t *testing.T) {
	for _, tc := range []struct {
		name string
		req  RunCreateRequest
		want string
	}{
		{
			// A conversation is something an agent has; a plain argv does not.
			name: "resume without an agent",
			req:  RunCreateRequest{Command: []string{"bash"}, Console: true, Resume: "x", Branch: "b"},
			want: "resume needs an agent",
		},
		{
			// A headless resume would replay one prompt into an old conversation
			// and exit, which is not what "carry this on" means.
			name: "resume without console",
			req:  RunCreateRequest{Agent: "claude", Resume: "x", Branch: "b"},
			want: "resume needs console",
		},
		{
			name: "skip permissions without an agent",
			req:  RunCreateRequest{Command: []string{"bash"}, SkipPermissions: true, Branch: "b"},
			want: "skip_permissions needs an agent",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, fr := newTestServer(t)
			rec := doRequestAuthed(t, s.Handler(), http.MethodPost, "/v1/runs", s.Token, tc.req)
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

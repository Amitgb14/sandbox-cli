package agentusage

import (
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/server"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
	"github.com/Amitgb14/sandbox-cli/internal/workspace"
)

func TestRolledOver(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		at   time.Time
		want bool
	}{
		{"reset still ahead", now.Add(time.Minute), false},
		{"reset just passed", now.Add(-time.Minute), true},
		{"reset exactly now", now, true},
		// A window that reported no reset time cannot be placed either side of
		// one, so it is never called rolled over — the alternative is hiding a
		// percentage that is still true.
		{"no reset reported", time.Time{}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := Window{Kind: KindSevenDay, Percent: 25, ResetsAt: tc.at}
			if got := w.RolledOver(now); got != tc.want {
				t.Errorf("RolledOver = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestNeedsRefresh(t *testing.T) {
	now := time.Date(2026, 7, 25, 12, 0, 0, 0, time.UTC)
	for _, tc := range []struct {
		name string
		at   time.Time
		want bool
	}{
		{"hours old", now.Add(-16 * time.Hour), true},
		{"just fetched", now.Add(-time.Second), false},
		{"exactly at the threshold", now.Add(-StaleAfter), true},
		// An age we cannot vouch for is not the same as a recent one.
		{"no stamp at all", time.Time{}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := Snapshot{Agent: "claude", Windows: []Window{{Kind: KindFiveHour}}, FetchedAt: tc.at}
			if got := s.NeedsRefresh(now); got != tc.want {
				t.Errorf("NeedsRefresh = %v, want %v", got, tc.want)
			}
		})
	}
}

func refreshServer(t *testing.T, cmd ...string) (*api.Client, string) {
	t.Helper()
	cfg := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", cfg)
	srv := httptest.NewServer((&server.Server{Backend: fake.New(api.CapEgressAllowlist), Policy: spec.DefaultPolicy()}).Handler())
	t.Cleanup(srv.Close)
	c, err := api.NewClient(srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	prev := refreshCommand
	refreshCommand = func(agents.Descriptor) []string { return cmd }
	t.Cleanup(func() { refreshCommand = prev })
	d, _ := agents.Lookup("claude")
	return c, workspace.LoginDir(d)
}

func saveLogin(t *testing.T, dir string) {
	t.Helper()
	os.MkdirAll(filepath.Join(dir, ".claude"), 0o700)
	os.WriteFile(filepath.Join(dir, ".claude", ".credentials.json"), []byte(`{"t":1}`), 0o600)
	os.WriteFile(filepath.Join(dir, ".claude.json"), []byte(`{}`), 0o600)
}

// No saved login is the ordinary case before the first claude run, so it has
// to be an explanation rather than a sandbox that fails to authenticate.
func TestRefreshWithoutALogin(t *testing.T) {
	c, _ := refreshServer(t, "true")
	err := Refresh(context.Background(), c)
	if err == nil || !strings.Contains(err.Error(), "sandbox-cli claude") {
		t.Fatalf("err = %v, want it to say how to log in", err)
	}
}

// The turn runs in a sandbox with the saved login restored, the login is
// copied back out (which is what carries the refreshed cache), and the sandbox
// is gone afterwards.
func TestRefreshRunsTheAgentInASandbox(t *testing.T) {
	c, dir := refreshServer(t, "cat", "/sandbox/home/.claude/.credentials.json")
	saveLogin(t, dir)
	if err := Refresh(context.Background(), c); err != nil {
		t.Fatalf("Refresh: %v — the login was not restored before the turn", err)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, ".claude.json")); string(b) != "{}" {
		t.Errorf("the cache did not come back: %q", b)
	}
	list, _ := c.Sandboxes(context.Background())
	for _, sb := range list {
		if sb.State != api.StateTerminated {
			t.Errorf("sandbox %s left %s", sb.ID, sb.State)
		}
	}
}

// Only the saved login goes in: a host ANTHROPIC_API_KEY would make claude
// bill the key, and the windows being measured belong to the subscription.
func TestRefreshDoesNotForwardAnAPIKey(t *testing.T) {
	t.Setenv("ANTHROPIC_API_KEY", "sk-host")
	c, dir := refreshServer(t, "printenv", "ANTHROPIC_API_KEY")
	saveLogin(t, dir)
	// printenv exits 1 for a name that is not set.
	if err := Refresh(context.Background(), c); err == nil || !strings.Contains(err.Error(), "exited 1") {
		t.Fatalf("err = %v: the host's API key reached the refresh sandbox", err)
	}
}

// A failing agent has usually said why — signed out, offline, no plan — and
// that sentence is worth more than the exit status wrapping it.
func TestRefreshReportsWhatTheAgentSaid(t *testing.T) {
	c, dir := refreshServer(t, "cat", "/no/such/file")
	saveLogin(t, dir)
	err := Refresh(context.Background(), c)
	if err == nil || !strings.Contains(err.Error(), "/no/such/file") {
		t.Errorf("error = %v, want it to carry the agent's own message", err)
	}
}

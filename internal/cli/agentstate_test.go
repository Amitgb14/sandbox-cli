package cli

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agentstate"
	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/server"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// wait returns as soon as the agent is in a state asked for; a timeout says
// which state it was really in and exits 2, not 1, because the agent did
// nothing wrong; and a sandbox that is gone ends the wait rather than letting
// it run to its timeout.
func TestAgentWait(t *testing.T) {
	srv := httptest.NewServer((&server.Server{Backend: fake.New(api.CapEgressAllowlist), Policy: spec.DefaultPolicy()}).Handler())
	defer srv.Close()
	c, _ := api.NewClient(srv.URL, "")
	ctx := context.Background()
	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{Name: "w", Labels: map[string]string{agentstate.AgentLabel: "claude"}})
	if err != nil {
		t.Fatal(err)
	}
	c.StartProcess(ctx, sb.ID, api.RunRequest{Argv: []string{"sleep", "1"}})
	want := func(s ...agentstate.State) map[agentstate.State]bool {
		m := map[agentstate.State]bool{}
		for _, x := range s {
			m[x] = true
		}
		return m
	}

	r, err := waitForState(ctx, c, "w", want(agentstate.Done, agentstate.Failed), 10*time.Second, 50*time.Millisecond)
	if err != nil || r.State != agentstate.Done {
		t.Fatalf("waiting for the agent to finish: %+v %v", r, err)
	}

	_, err = waitForState(ctx, c, "w", want(agentstate.Blocked), 300*time.Millisecond, 50*time.Millisecond)
	var to errWaitTimedOut
	if !errors.As(err, &to) || to.last != agentstate.Done || to.ExitCode() != 2 {
		t.Fatalf("a timeout: %v", err)
	}

	c.TerminateSandbox(ctx, sb.ID)
	start := time.Now()
	if _, err := waitForState(ctx, c, "w", want(agentstate.Blocked), 10*time.Second, 50*time.Millisecond); err == nil || !strings.Contains(err.Error(), "gone") {
		t.Errorf("a sandbox that is gone: %v", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Error("waited for a sandbox that is gone")
	}
	if r, err := waitForState(ctx, c, "w", want(agentstate.Stopped), time.Second, 50*time.Millisecond); err != nil || r.State != agentstate.Stopped {
		t.Errorf("waiting for it to stop: %+v %v", r, err)
	}

	var out bytes.Buffer
	printAgentStates(&out, []agentstate.Report{{Sandbox: sb.ID, Name: "w\x1b[2J", Agent: "claude", State: agentstate.Blocked}}, true)
	if strings.ContainsRune(out.String(), 0x1b) || !strings.Contains(out.String(), "waiting for somebody") {
		t.Errorf("listing: %q", out.String())
	}
}

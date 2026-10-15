package gateway

import (
	"slices"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// The gateway's own reads of a sandbox — saving an agent's login from it
// every few minutes — are background to the node: not activity, so they do
// not hold its idle timeout off, and not a reason to resume it. Sent as
// ordinary requests, they kept every sandbox with a kept login from ever
// idling out. The user's own reads through the gateway are activity.
func TestTheGatewaysLoginSavesAreBackground(t *testing.T) {
	n1 := startNode(t, "n1", allCaps...)
	tg := startGateway(t, withKey, n1)
	ctx := ctxT(t)
	ana, _ := tg.loginClient("ana", "aa")
	sb, err := ana.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := ana.KeepAgentLogin(ctx, sb.ID, "claude"); err != nil {
		t.Fatal(err)
	}
	if err := ana.WriteFile(ctx, sb.ID, credPath, []byte(agentCred)); err != nil {
		t.Fatal(err)
	}

	mark, bgMark := n1.marks()
	tg.g.saveAgentLogins(ctx)
	paths, bg := n1.requestsSince(mark, bgMark)
	var ours []string
	for _, p := range paths {
		if strings.Contains(p, sb.ID) {
			ours = append(ours, p)
		}
	}
	if len(ours) == 0 {
		t.Fatal("the save read nothing from the sandbox")
	}
	if len(bg) != len(ours) {
		t.Errorf("the save sent %d requests about %s, %d of them background: %v; background %v", len(ours), sb.ID, len(bg), ours, bg)
	}
	if l, err := ana.AgentLogins(ctx); err != nil || len(l) != 1 {
		t.Fatalf("the login was not saved: %+v, %v", l, err)
	}

	mark, bgMark = n1.marks()
	if _, err := ana.ReadFile(ctx, sb.ID, credPath); err != nil {
		t.Fatal(err)
	}
	if _, bg := n1.requestsSince(mark, bgMark); len(bg) != 0 {
		t.Errorf("the user's own read was sent as background: %v", bg)
	}
}

// A key that may only read does not keep a sandbox up or wake one: its
// requests reach the node as background. Waking a sandbox spends the
// machine, which is create's to do.
func TestAReadOnlyKeyDoesNotKeepASandboxUp(t *testing.T) {
	n1 := startNode(t, "n1", allCaps...)
	tg := startGateway(t, nil, n1)
	ctx := ctxT(t)
	owner := tg.client("ana", "aa", ScopeRead, ScopeCreate)
	reader := tg.client("ana", "aa", ScopeRead)
	sb, err := owner.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if err := owner.WriteFile(ctx, sb.ID, "/sandbox/home/notes", []byte("x")); err != nil {
		t.Fatal(err)
	}
	mark, bgMark := n1.marks()
	if _, err := reader.ReadFile(ctx, sb.ID, "/sandbox/home/notes"); err != nil {
		t.Fatal(err)
	}
	if _, bg := n1.requestsSince(mark, bgMark); !slices.Contains(bg, "GET /v1/sandboxes/"+sb.ID+"/files") {
		t.Errorf("a read-only key's file read was not background: %v", bg)
	}
}

// A create asking to be suspended when idle goes to a node that can
// suspend, rather than to one that would refuse it.
func TestAnIdleSuspendIsScheduledWhereItCanBeHad(t *testing.T) {
	plain := startNode(t, "plain", api.CapEgressAllowlist)
	sleeper := startNode(t, "sleeper", api.CapEgressAllowlist, api.CapSuspend)
	tg := startGateway(t, nil, plain, sleeper)
	ctx := ctxT(t)
	c := tg.client("ana", "aa", ScopeRead, ScopeCreate)
	for range 4 {
		sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{IdleAction: api.IdleSuspend})
		if err != nil {
			t.Fatal(err)
		}
		if n := nodeOf(tg, sb.ID); n != "sleeper" {
			t.Fatalf("an idle suspend went to %s", n)
		}
		if sb.IdleAction != api.IdleSuspend {
			t.Fatalf("idle_action %q", sb.IdleAction)
		}
	}
}

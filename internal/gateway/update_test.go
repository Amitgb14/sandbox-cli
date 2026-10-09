package gateway

import (
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// Labels are replaced whole, but never the gateway's own: a client cannot
// set, change or drop gateway.owner — dropping it would hide the sandbox
// from its owner — and one that sends back the labels it read, gateway's
// included and unchanged, is not refused for it.
func TestAnUpdateKeepsTheGatewaysLabels(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1", allCaps...))
	alice := tg.user("alice")
	ctx := ctxT(t)
	sb, err := alice.CreateSandbox(ctx, api.CreateSandboxRequest{Name: "web", Labels: map[string]string{"team": "a"}})
	if err != nil {
		t.Fatal(err)
	}
	if sb.Labels[LabelOwner] != "alice" {
		t.Fatalf("precondition: owner label %v", sb.Labels)
	}
	labels := func(m map[string]string) *map[string]string { return &m }

	for name, l := range map[string]map[string]string{
		"forging the owner":   {LabelOwner: "bob"},
		"setting a new one":   {"gateway.job": "j1"},
		"changing the tenant": {LabelTenant: "acme"},
	} {
		_, err := alice.UpdateSandbox(ctx, sb.ID, api.UpdateSandboxRequest{Labels: labels(l)})
		wantCode(t, err, api.CodeInvalidRequest)
		if got, _ := alice.Sandbox(ctx, sb.ID); got.Labels[LabelOwner] != "alice" || got.Labels["team"] != "a" {
			t.Errorf("%s changed the labels: %v", name, got.Labels)
		}
	}

	// {} removes the client's labels and keeps the owner: still alice's,
	// still listed, still found by name.
	got, err := alice.UpdateSandbox(ctx, sb.ID, api.UpdateSandboxRequest{Labels: labels(map[string]string{})})
	if err != nil {
		t.Fatal(err)
	}
	if got.Labels[LabelOwner] != "alice" || got.Labels["team"] != "" {
		t.Fatalf("after clearing: %v", got.Labels)
	}
	if list, _ := alice.Sandboxes(ctx); len(list) != 1 {
		t.Fatalf("alice lists %d sandboxes after clearing her labels", len(list))
	}
	if byName, err := alice.Sandbox(ctx, "web"); err != nil || byName.ID != sb.ID {
		t.Fatalf("web resolves to %+v, %v", byName, err)
	}

	// Read, modify, write back: the gateway's labels ride along unchanged.
	cur, _ := alice.Sandbox(ctx, sb.ID)
	cur.Labels["team"] = "b"
	if got, err := alice.UpdateSandbox(ctx, sb.ID, api.UpdateSandboxRequest{Labels: &cur.Labels}); err != nil || got.Labels["team"] != "b" {
		t.Fatalf("writing back what was read: %v, %v", got.Labels, err)
	}
}

// A name is unique among a user's sandboxes on every node, not only the
// one the sandbox is on; another user's same name on the same node is
// refused without saying whose.
func TestARenameIsUniquePerUserAcrossNodes(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1", allCaps...), startNode(t, "n2", allCaps...), startNode(t, "n3", allCaps...))
	alice := tg.user("alice")
	ctx := ctxT(t)
	// Two of alice's sandboxes on different nodes.
	var a, b api.Sandbox
	for i := 0; i < 12 && b.ID == ""; i++ {
		sb, err := alice.CreateSandbox(ctx, api.CreateSandboxRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if a.ID == "" {
			a = sb
			continue
		}
		oa, _ := tg.g.ownerOf(a.ID)
		ob, _ := tg.g.ownerOf(sb.ID)
		if oa.Node != ob.Node {
			b = sb
		}
	}
	if b.ID == "" {
		t.Skip("every sandbox landed on one node")
	}
	name := "web"
	if _, err := alice.UpdateSandbox(ctx, a.ID, api.UpdateSandboxRequest{Name: &name}); err != nil {
		t.Fatal(err)
	}
	_, err := alice.UpdateSandbox(ctx, b.ID, api.UpdateSandboxRequest{Name: &name})
	wantCode(t, err, api.CodeConflict)
	if got, _ := alice.Sandbox(ctx, b.ID); got.Name != "" {
		t.Fatalf("a refused rename took: %q", got.Name)
	}

	// Bob, on the one node alice's "web" is on, is refused the name by that
	// node — said without naming anyone's sandbox.
	single := startGateway(t, nil, startNode(t, "s1", allCaps...))
	sa, sbob := single.user("alice"), single.user("bob")
	if _, err := sa.CreateSandbox(ctx, api.CreateSandboxRequest{Name: "web"}); err != nil {
		t.Fatal(err)
	}
	mine, err := sbob.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = sbob.UpdateSandbox(ctx, mine.ID, api.UpdateSandboxRequest{Name: &name})
	wantCode(t, err, api.CodeConflict)
	if strings.Contains(err.Error(), "named") {
		t.Errorf("the refusal says a sandbox by that name exists: %v", err)
	}
}

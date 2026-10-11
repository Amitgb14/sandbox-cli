package gateway

import (
	"context"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// A sandbox's details are stored as its node answered at create, kept in
// step with a change made through the gateway, and kept as history once it
// is terminated, until the sweep.
func TestSandboxDetailsAreKept(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1", allCaps...))
	ctx := ctxT(t)
	ana := tg.user("ana")
	sb, err := ana.CreateSandbox(ctx, api.CreateSandboxRequest{Name: "web", LifetimeSecs: 3600, Labels: map[string]string{"team": "a"}})
	if err != nil {
		t.Fatal(err)
	}
	row := func() SandboxRow {
		t.Helper()
		rows, err := tg.store.Store.SandboxHistory(context.Background(), SandboxFilter{AllTenant: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range rows {
			if r.ID == sb.ID {
				return r
			}
		}
		t.Fatalf("%s is not stored", sb.ID)
		return SandboxRow{}
	}
	r := row()
	d := r.Details
	if d == nil || d.Name != "web" || d.Image != sb.Image || d.State != sb.State || d.LifetimeSecs != 3600 ||
		d.ExpiresAt == nil || d.Labels["team"] != "a" || d.Network.Mode == "" || r.Owner.User != "ana" {
		t.Fatalf("the stored details: %+v %+v", r, d)
	}

	// A change made on the node — a lifetime moved — reaches the row with
	// the next refresh from the node.
	life := 1800
	if _, err := ana.UpdateSandbox(ctx, sb.ID, api.UpdateSandboxRequest{LifetimeSecs: &life}); err != nil {
		t.Fatal(err)
	}
	tg.g.reconcile(ctx)
	if d := row().Details; d.LifetimeSecs != 1800 {
		t.Errorf("after the update and a refresh: lifetime %d", d.LifetimeSecs)
	}

	if err := ana.TerminateSandbox(ctx, sb.ID); err != nil {
		t.Fatal(err)
	}
	r = row()
	if r.TerminatedAt == nil || r.Details.State != api.StateTerminated {
		t.Fatalf("a terminated sandbox's row: %+v", r)
	}
	if _, ok := tg.store.OwnerOf(sb.ID); ok {
		t.Error("a terminated sandbox still has an owner record")
	}
	if u := tg.store.UsageOf(""); u.Sandboxes != 0 {
		t.Errorf("a terminated sandbox counts against the quota: %+v", u)
	}
	if n, err := tg.store.Store.PruneSandboxHistory(context.Background(), time.Now().Add(time.Minute)); err != nil || n != 1 {
		t.Errorf("the sweep: %d %v", n, err)
	}
}

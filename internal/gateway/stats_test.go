package gateway

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// The super admin's dashboard counts users, organisations, keys and
// sandboxes, and sums what the nodes have and give, without naming any
// sandbox or its owner; nobody else may ask.
func TestAdminStatsCountWithoutNaming(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1"))
	ctx := ctxT(t)
	alice := tg.client("alice", "alice", ScopeRead, ScopeCreate, ScopeDelete)
	bob := tg.client("bob", "bob", ScopeRead, ScopeCreate, ScopeOrgCreate)
	mustOrg(t, bob, "acme")
	a1, err := alice.CreateSandbox(ctx, api.CreateSandboxRequest{CPUs: 1, MemoryMB: 512, DiskMB: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := bob.CreateSandbox(ctx, api.CreateSandboxRequest{CPUs: 2, MemoryMB: 1024, DiskMB: 2048}); err != nil {
		t.Fatal(err)
	}
	a2, err := alice.CreateSandbox(ctx, api.CreateSandboxRequest{CPUs: 1, MemoryMB: 256, DiskMB: 1024})
	if err != nil {
		t.Fatal(err)
	}
	if err := alice.TerminateSandbox(ctx, a2.ID); err != nil {
		t.Fatal(err)
	}

	st, err := tg.admin.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// root (the admin), alice and bob; alice, bob and acme are organisations.
	if st.Users.Total != 3 || st.Orgs != 3 || st.Keys.Active != 3 {
		t.Errorf("users %d, orgs %d, keys %d; want 3, 3, 3", st.Users.Total, st.Orgs, st.Keys.Active)
	}
	if st.Sandboxes.Running != 2 || st.Sandboxes.Created != 3 || st.Sandboxes.Created24h != 3 {
		t.Errorf("sandboxes: %+v; want 2 running of 3 made today", st.Sandboxes)
	}
	if st.Allocated != (api.NodeResources{CPUs: 3, MemoryMB: 1536, DiskMB: 3072}) {
		t.Errorf("allocated %+v; want the two running ones'", st.Allocated)
	}
	if st.Nodes.Total != 1 || st.Nodes.Healthy != 1 || st.Nodes.Capacity.MemoryMB == 0 {
		t.Errorf("nodes: %+v", st.Nodes)
	}

	raw, _ := json.Marshal(st)
	for _, s := range []string{a1.ID, "alice", "bob", "acme"} {
		if strings.Contains(string(raw), s) {
			t.Errorf("the stats name %q: %s", s, raw)
		}
	}
	_, err = alice.Stats(ctx)
	wantCode(t, err, api.CodeRefused)
}

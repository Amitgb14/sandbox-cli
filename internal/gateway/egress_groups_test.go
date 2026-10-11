package gateway

import (
	"reflect"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

func allowlist(allow []string, groups ...string) *api.NetworkPolicy {
	return &api.NetworkPolicy{Mode: api.NetworkAllowlist, Allow: allow, Groups: groups}
}

// Only an admin writes groups; every key reads them, to pick from.
func TestOnlyAnAdminWritesAllowlistGroups(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1", allCaps...))
	ctx := ctxT(t)
	bob := tg.user("bob")

	_, err := bob.PutEgressGroup(ctx, api.EgressGroup{Name: "go", Hosts: []string{"proxy.golang.org"}})
	wantCode(t, err, api.CodeRefused)
	got, err := tg.admin.PutEgressGroup(ctx, api.EgressGroup{Name: "go", Description: "Go modules", Hosts: []string{"Proxy.Golang.org.", "sum.golang.org", "proxy.golang.org"}})
	if err != nil {
		t.Fatal(err)
	}
	// Held to the names a request's allow list holds.
	if want := []string{"proxy.golang.org", "sum.golang.org"}; !reflect.DeepEqual(got.Hosts, want) {
		t.Errorf("hosts kept as %v; want %v", got.Hosts, want)
	}
	err = bob.DeleteEgressGroup(ctx, "go")
	wantCode(t, err, api.CodeRefused)
	key, _ := tg.keyed("carl", "", ScopeRead, ScopeCreate)
	if resp := tg.raw("GET", "/v1/admin/egress-groups", key, ""); resp.StatusCode != 403 {
		t.Errorf("a user read the admin listing: %d", resp.StatusCode)
	}

	groups, err := bob.EgressGroups(ctx)
	if err != nil || len(groups) != 1 || groups[0].Name != "go" || groups[0].Description != "Go modules" {
		t.Fatalf("a user's listing: %+v, %v", groups, err)
	}

	for _, bad := range []api.EgressGroup{
		{Name: "Go", Hosts: []string{"a.example.com"}},
		{Name: "empty"},
		{Name: "url", Hosts: []string{"https://example.com"}},
		{Name: "star", Hosts: []string{"*"}},
	} {
		if _, err := tg.admin.PutEgressGroup(ctx, bad); !api.IsCode(err, api.CodeInvalidRequest) {
			t.Errorf("group %+v: %v; want refused as invalid", bad, err)
		}
	}
	err = tg.admin.DeleteEgressGroup(ctx, "nope")
	wantCode(t, err, api.CodeNotFound)
	if err := tg.admin.DeleteEgressGroup(ctx, "go"); err != nil {
		t.Fatal(err)
	}
	if groups, err := bob.EgressGroups(ctx); err != nil || len(groups) != 0 {
		t.Errorf("after removing it: %+v, %v", groups, err)
	}
}

// A group named at create becomes hosts before the node sees it, beside the
// request's own; named with no allow list, its hosts are the whole list, not
// added to the node's default.
func TestAllowlistGroupsBecomeHosts(t *testing.T) {
	n1 := startNode(t, "n1", allCaps...)
	tg := startGateway(t, nil, n1)
	ctx := ctxT(t)
	bob := tg.user("bob")
	for _, g := range []api.EgressGroup{
		{Name: "go", Hosts: []string{"proxy.golang.org", "sum.golang.org"}},
		{Name: "npm", Hosts: []string{"registry.npmjs.org", "proxy.golang.org"}},
	} {
		if _, err := tg.admin.PutEgressGroup(ctx, g); err != nil {
			t.Fatal(err)
		}
	}

	sb, err := bob.CreateSandbox(ctx, api.CreateSandboxRequest{Network: allowlist([]string{"github.com"}, "go", "npm")})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"github.com", "proxy.golang.org", "registry.npmjs.org", "sum.golang.org"}
	if got, ok := n1.be.Network(sb.ID); !ok || !reflect.DeepEqual(got.Allow, want) || len(got.Groups) != 0 {
		t.Errorf("the node was given %+v; want allow %v and no groups", got, want)
	}

	sb, err = bob.CreateSandbox(ctx, api.CreateSandboxRequest{Network: allowlist(nil, "go")})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := n1.be.Network(sb.ID); !reflect.DeepEqual(got.Allow, []string{"proxy.golang.org", "sum.golang.org"}) {
		t.Errorf("groups with no allow list gave %v; want the group's hosts alone", got.Allow)
	}

	// The same on an update of a running sandbox.
	if _, err := bob.UpdateSandbox(ctx, sb.ID, api.UpdateSandboxRequest{Network: allowlist([]string{"github.com"}, "npm")}); err != nil {
		t.Fatal(err)
	}
	if got, _ := n1.be.Network(sb.ID); !reflect.DeepEqual(got.Allow, []string{"github.com", "proxy.golang.org", "registry.npmjs.org"}) {
		t.Errorf("after an update naming npm: %v", got.Allow)
	}
}

// A group that cannot be applied refuses the request: an unknown name is
// not dropped, and groups go with an allowlist only.
func TestAllowlistGroupsRefuseRatherThanDrop(t *testing.T) {
	n1 := startNode(t, "n1", allCaps...)
	tg := startGateway(t, nil, n1)
	ctx := ctxT(t)
	bob := tg.user("bob")
	if _, err := tg.admin.PutEgressGroup(ctx, api.EgressGroup{Name: "go", Hosts: []string{"proxy.golang.org"}}); err != nil {
		t.Fatal(err)
	}
	before := n1.be.Count()
	for name, net := range map[string]*api.NetworkPolicy{
		"an unknown group":    allowlist(nil, "go", "nope"),
		"groups in mode open": {Mode: api.NetworkOpen, Groups: []string{"go"}},
		"groups with no mode": {Groups: []string{"go"}},
	} {
		_, err := bob.CreateSandbox(ctx, api.CreateSandboxRequest{Network: net})
		if !api.IsCode(err, api.CodeInvalidRequest) {
			t.Errorf("%s: %v; want invalid_request", name, err)
		}
	}
	if n := n1.be.Count(); n != before {
		t.Errorf("%d sandboxes made by refused requests", n-before)
	}

	sb, err := bob.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = bob.UpdateSandbox(ctx, sb.ID, api.UpdateSandboxRequest{Network: allowlist(nil, "nope")})
	wantCode(t, err, api.CodeInvalidRequest)
}

// An edit holds for sandboxes made after it; a running one keeps its hosts.
func TestAllowlistGroupEditHoldsForTheNextSandbox(t *testing.T) {
	n1 := startNode(t, "n1", allCaps...)
	tg := startGateway(t, nil, n1)
	ctx := ctxT(t)
	bob := tg.user("bob")
	if _, err := tg.admin.PutEgressGroup(ctx, api.EgressGroup{Name: "go", Hosts: []string{"proxy.golang.org"}}); err != nil {
		t.Fatal(err)
	}
	first, err := bob.CreateSandbox(ctx, api.CreateSandboxRequest{Network: allowlist(nil, "go")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tg.admin.PutEgressGroup(ctx, api.EgressGroup{Name: "go", Hosts: []string{"goproxy.example.com"}}); err != nil {
		t.Fatal(err)
	}
	second, err := bob.CreateSandbox(ctx, api.CreateSandboxRequest{Network: allowlist(nil, "go")})
	if err != nil {
		t.Fatal(err)
	}
	if got, _ := n1.be.Network(first.ID); !reflect.DeepEqual(got.Allow, []string{"proxy.golang.org"}) {
		t.Errorf("the running sandbox's hosts changed with the group: %v", got.Allow)
	}
	if got, _ := n1.be.Network(second.ID); !reflect.DeepEqual(got.Allow, []string{"goproxy.example.com"}) {
		t.Errorf("the next sandbox got %v; want the group as it is now", got.Allow)
	}
}

// A job or service naming a group is refused when the group does not exist,
// and a group one still names cannot be removed: its next sandbox could
// not be made.
func TestJobsAndServicesHoldTheirGroups(t *testing.T) {
	n1 := startNode(t, "n1", allCaps...)
	tg := startGateway(t, nil, n1)
	ctx := ctxT(t)
	ana := tg.jobUser("ana")

	_, err := ana.CreateJob(ctx, api.JobSpec{Command: []string{"true"}, Network: allowlist(nil, "go")})
	wantCode(t, err, api.CodeInvalidRequest)
	sp := webSpec(1)
	sp.Network = allowlist(nil, "go")
	_, err = ana.DeployService(ctx, sp)
	wantCode(t, err, api.CodeInvalidRequest)

	if _, err := tg.admin.PutEgressGroup(ctx, api.EgressGroup{Name: "go", Hosts: []string{"proxy.golang.org"}}); err != nil {
		t.Fatal(err)
	}
	j, err := ana.CreateJob(ctx, api.JobSpec{Command: []string{"sleep", "1"}, Network: allowlist(nil, "go")})
	if err != nil {
		t.Fatal(err)
	}
	j = waitJob(t, ana, j.ID, "the run to start", func(j api.Job) bool { return j.Runs[0].PID != 0 })
	if got, _ := n1.be.Network(j.Runs[0].Sandbox); !reflect.DeepEqual(got.Allow, []string{"proxy.golang.org"}) {
		t.Errorf("the job's sandbox got %v", got.Allow)
	}
	err = tg.admin.DeleteEgressGroup(ctx, "go")
	wantCode(t, err, api.CodeConflict)
	waitJob(t, ana, j.ID, "the job to end", finished)

	if _, err := ana.DeployService(ctx, sp); err != nil {
		t.Fatal(err)
	}
	err = tg.admin.DeleteEgressGroup(ctx, "go")
	wantCode(t, err, api.CodeConflict)
	if err := ana.DeleteService(ctx, sp.Name); err != nil {
		t.Fatal(err)
	}
	if err := tg.admin.DeleteEgressGroup(ctx, "go"); err != nil {
		t.Errorf("once nothing names it: %v", err)
	}
}

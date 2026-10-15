package cli

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/agenthome"
	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/gateway"
	"github.com/Amitgb14/sandbox-cli/internal/server"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

const credFile = agenthome.GuestHome + "/.claude/.credentials.json"

// loginGateway is a gateway with one fake-backend node, a user's client of
// it, and a client of the node itself. With key false the gateway has no
// secrets key, so keeps no logins.
func loginGateway(t *testing.T, key bool) (gw, node *api.Client) {
	t.Helper()
	const nodeToken = "node-token-0123456789abcdef"
	be := fake.New(api.CapEgressAllowlist)
	ns := httptest.NewServer((&server.Server{Backend: be, Policy: spec.DefaultPolicyFor(be.Capabilities()), Token: nodeToken,
		Capacity: api.NodeResources{CPUs: 16, MemoryMB: 16 << 10, DiskMB: 1 << 20}}).Handler())
	t.Cleanup(ns.Close)
	tokenFile := filepath.Join(t.TempDir(), "token")
	if err := os.WriteFile(tokenFile, []byte(nodeToken+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := gateway.OpenSQLite(context.Background(), filepath.Join(t.TempDir(), "gateway.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	cfg := gateway.Config{Store: st, JobsDir: filepath.Join(t.TempDir(), "jobs"), Logf: t.Logf,
		StaticNodes: []gateway.NodeConfig{{Name: "n1", Endpoint: ns.URL, TokenFile: tokenFile}}}
	if key {
		cfg.SecretsKey = bytes.Repeat([]byte{7}, 32)
	}
	g, err := gateway.New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	g.Start(context.Background())
	t.Cleanup(g.Close)
	gs := httptest.NewServer(g.Handler())
	t.Cleanup(gs.Close)
	secret, _, err := st.CreateKey(context.Background(), "ana", "aa", []string{gateway.ScopeRead, gateway.ScopeCreate, gateway.ScopeDelete})
	if err != nil {
		t.Fatal(err)
	}
	gw = api.NewClientWithHTTP(gs.URL, secret, gs.Client())
	node = api.NewClientWithHTTP(ns.URL, nodeToken, ns.Client())
	return gw, node
}

// saveLocalLogin writes this machine's saved claude login.
func saveLocalLogin(t *testing.T, d agents.Descriptor, cred string) {
	t.Helper()
	p := filepath.Join(agenthome.LoginDir(d), ".claude", ".credentials.json")
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(cred), 0o600); err != nil {
		t.Fatal(err)
	}
}

func guestCred(t *testing.T, c *api.Client, sb string) string {
	t.Helper()
	b, err := c.ReadFile(context.Background(), sb, credFile)
	if err != nil {
		t.Fatalf("read the credentials in %s: %v", sb, err)
	}
	return string(b)
}

// Against a gateway that keeps logins, a run's login is the gateway's. The
// first run gives it this machine's login; after that the gateway's wins,
// whatever this machine holds.
func TestARunOnAGatewayUsesTheLoginTheGatewayKeeps(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ctx := context.Background()
	d, _ := agents.LookupInteractive("claude")
	saveLocalLogin(t, d, `{"from":"this machine"}`)
	gw, _ := loginGateway(t, true)

	sb1, err := gw.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !keepLoginOnGateway(ctx, gw, sb1.ID, d) {
		t.Fatal("a gateway that keeps logins was not used")
	}
	if got := guestCred(t, gw, sb1.ID); got != `{"from":"this machine"}` {
		t.Fatalf("the first run, with nothing kept yet, got %q; want this machine's login", got)
	}
	// The agent refreshes it; the delete saves it on the gateway.
	if err := gw.WriteFile(ctx, sb1.ID, credFile, []byte(`{"from":"the gateway"}`)); err != nil {
		t.Fatal(err)
	}
	if err := gw.TerminateSandbox(ctx, sb1.ID); err != nil {
		t.Fatal(err)
	}
	if l, err := gw.AgentLogins(ctx); err != nil || len(l) != 1 || l[0].Agent != "claude" {
		t.Fatalf("kept logins: %+v, %v", l, err)
	}

	// This machine's copy is now stale; the gateway's is what goes in.
	sb2, err := gw.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if !keepLoginOnGateway(ctx, gw, sb2.ID, d) {
		t.Fatal("not kept the second time")
	}
	if got := guestCred(t, gw, sb2.ID); got != `{"from":"the gateway"}` {
		t.Fatalf("the second run got %q; want the gateway's login", got)
	}
}

// A plain sandboxd, and a gateway that keeps no logins, leave the login to
// this machine's files, as before.
func TestARunElsewhereKeepsThisMachinesLogin(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	ctx := context.Background()
	d, _ := agents.LookupInteractive("claude")
	gw, node := loginGateway(t, false)
	for name, c := range map[string]*api.Client{"a plain sandboxd": node, "a gateway without a secrets key": gw} {
		sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if keepLoginOnGateway(ctx, c, sb.ID, d) {
			t.Errorf("%s was used to keep the login", name)
		}
		if _, err := c.ReadFile(ctx, sb.ID, credFile); err == nil {
			t.Errorf("%s: something was written into the sandbox", name)
		}
	}
}

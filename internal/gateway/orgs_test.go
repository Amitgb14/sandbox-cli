package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// Organisations: a request selects a tenant with X-Sandbox-Org, and every
// guarantee a tenant had is the organisation's. These tests hold the gateway
// to what selecting may never do: reach a tenant without a membership,
// change anything for a key with none, or outlast the membership.

var orgScopes = []string{ScopeRead, ScopeCreate, ScopeDelete, ScopeSSH, ScopeSecretsWrite, ScopeOrgCreate}

// attachSSH starts an SSH server on tg's gateway, as serve wires one.
func attachSSH(t *testing.T, tg *testGateway) (*SSHServer, string) {
	t.Helper()
	srv, err := NewSSHServer(SSHConfig{
		Store: tg.store, Router: tg.g, HostKeyFile: filepath.Join(t.TempDir(), "host_ed25519"),
		Host: "ssh.example.test", Port: 2222, Logf: t.Logf, HandshakeTimeout: 10 * time.Second,
		ExecArgv: func(cmd string) []string { return strings.Fields(cmd) },
		Audit:    tg.g.audit,
	})
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(func() { srv.Close() })
	tg.g.SetSSH(srv)
	return srv, ln.Addr().String()
}

// tryDial is dialAs that reports a refused login instead of failing.
func tryDial(t *testing.T, srv *SSHServer, addr, user string, auth ...ssh.AuthMethod) (*ssh.Client, error) {
	t.Helper()
	hostKey, _, _, _, err := ssh.ParseAuthorizedKey([]byte(srv.Info().HostKeys[0]))
	if err != nil {
		t.Fatal(err)
	}
	c, err := ssh.Dial("tcp", addr, &ssh.ClientConfig{User: user, Auth: auth,
		HostKeyCallback: ssh.FixedHostKey(hostKey), Timeout: 10 * time.Second})
	if err == nil {
		t.Cleanup(func() { c.Close() })
	}
	return c, err
}

// keyed issues a key and returns its secret and a client holding it.
func (tg *testGateway) keyed(user, tenant string, scopes ...string) (string, *api.Client) {
	tg.t.Helper()
	secret, _, err := tg.store.CreateKey(user, tenant, scopes)
	if err != nil {
		tg.t.Fatal(err)
	}
	return secret, api.NewClientWithHTTP(tg.ts.URL, secret, tg.ts.Client())
}

func sandboxIDs(t *testing.T, c *api.Client) map[string]bool {
	t.Helper()
	list, err := c.Sandboxes(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, sb := range list {
		out[sb.ID] = true
	}
	return out
}

func mustOrg(t *testing.T, c *api.Client, name string) {
	t.Helper()
	if _, err := c.CreateOrg(ctxT(t), name); err != nil {
		t.Fatalf("create org %s: %v", name, err)
	}
}

// codeOf sends one request and returns its status and error code.
func (tg *testGateway) codeOf(method, path, key, body string, hdr ...string) (int, string) {
	tg.t.Helper()
	resp := tg.raw(method, path, key, body, hdr...)
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	var eb api.ErrorBody
	_ = json.Unmarshal(data, &eb)
	return resp.StatusCode, eb.Error.Code
}

// A sandbox, a secret, a job and a service made in one organisation are
// invisible and unreachable from another — by id, by name, by tunnel, by
// SSH and through the router — and from the user's own tenant.
func TestOrgsAreIsolated(t *testing.T) {
	e := startServicesWith(t, withKey, "n1")
	srv, addr := attachSSH(t, e.testGateway)
	ctx := ctxT(t)
	_, alice := e.keyed("alice", "", orgScopes...)
	mustOrg(t, alice, "aa")
	mustOrg(t, alice, "bb")
	inA, inB := alice.WithOrg("aa"), alice.WithOrg("bb")

	sbA, err := inA.CreateSandbox(ctx, api.CreateSandboxRequest{Name: "box"})
	if err != nil {
		t.Fatal(err)
	}
	// (A node keeps names unique among all its sandboxes, so B's is
	// another name; that a name is looked up only within its tenant is
	// checked below.)
	sbB, err := inB.CreateSandbox(ctx, api.CreateSandboxRequest{Name: "boxb"})
	if err != nil {
		t.Fatal(err)
	}
	if sbA.Labels[LabelTenant] != "aa" || sbB.Labels[LabelTenant] != "bb" {
		t.Fatalf("tenant labels: %v %v", sbA.Labels, sbB.Labels)
	}
	if err := inA.SetSecret(ctx, "TOKEN", "for-a"); err != nil {
		t.Fatal(err)
	}
	job, err := inA.CreateJob(ctx, api.JobSpec{Command: []string{"sleep", "60"}})
	if err != nil {
		t.Fatal(err)
	}
	if s, err := inA.DeployService(ctx, webSpec(1)); err != nil || s.URL != "http://web--aa.apps.test" {
		t.Fatalf("deploy in aa: %+v %v", s, err)
	}
	webA := ids(waitService(t, inA, "web", "ready", ready(1)))

	// Listings.
	if got := sandboxIDs(t, inB); !got[sbB.ID] || got[sbA.ID] {
		t.Errorf("bb lists %v", got)
	}
	if got := sandboxIDs(t, inA); got[sbB.ID] || !got[sbA.ID] {
		t.Errorf("aa lists %v", got)
	}
	if got := sandboxIDs(t, alice); got[sbA.ID] || got[sbB.ID] {
		t.Errorf("alice's own tenant lists %v", got)
	}
	if s, err := inB.Secrets(ctx); err != nil || len(s) != 0 {
		t.Errorf("bb's secrets: %v %v", s, err)
	}
	if s, err := inA.Secrets(ctx); err != nil || len(s) != 1 {
		t.Errorf("aa's secrets: %v %v", s, err)
	}
	if j, err := inB.Jobs(ctx); err != nil || len(j) != 0 {
		t.Errorf("bb's jobs: %v %v", j, err)
	}
	if s, err := inB.Services(ctx); err != nil || len(s) != 0 {
		t.Errorf("bb's services: %v %v", s, err)
	}

	// By id, by name, by every kind of route, from B and from home.
	for name, c := range map[string]*api.Client{"bb": inB, "home": alice} {
		_, err := c.Sandbox(ctx, sbA.ID)
		wantCode(t, err, api.CodeNotFound)
		_, err = c.Job(ctx, job.ID)
		wantCode(t, err, api.CodeNotFound)
		_, err = c.Service(ctx, "web")
		wantCode(t, err, api.CodeNotFound)
		_, err = c.Tunnel(ctx, sbA.ID, appPort)
		wantCode(t, err, api.CodeNotFound)
		_, err = c.SSHAccess(ctx, sbA.ID, 0)
		wantCode(t, err, api.CodeNotFound)
		_, err = c.StartProcess(ctx, sbA.ID, api.RunRequest{Argv: []string{"true"}})
		wantCode(t, err, api.CodeNotFound)
		_, err = c.Sandbox(ctx, "box")
		wantCode(t, err, api.CodeNotFound)
		_ = name
	}
	if sb, err := inB.Sandbox(ctx, "boxb"); err != nil || sb.ID != sbB.ID {
		t.Errorf("bb's box: %+v %v", sb, err)
	}
	if sb, err := inA.Sandbox(ctx, "box"); err != nil || sb.ID != sbA.ID {
		t.Errorf("aa's box: %+v %v", sb, err)
	}

	// The router: aa's service under aa's name only.
	if code, body := e.get("web--aa.apps.test", "/"); code != http.StatusOK || !webA[body["id"]] {
		t.Errorf("web--aa: %d %v", code, body)
	}
	for _, host := range []string{"web--bb.apps.test", "web.apps.test"} {
		if code, _ := e.get(host, "/"); code != http.StatusNotFound {
			t.Errorf("%s: %d; want 404", host, code)
		}
	}

	// SSH: a token issued in aa reaches aa's sandbox; one issued in bb
	// cannot be had for it (above). A key logs in to aa's sandbox by id;
	// the name "box" is looked up in alice's own tenant only, which has none.
	k, _ := newKey(t)
	if _, err := alice.AddSSHKey(ctx, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k.PublicKey()))), ""); err != nil {
		t.Fatal(err)
	}
	acc, err := inA.SSHAccess(ctx, sbA.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	dialAs(t, srv, addr, acc.User).Close()
	dialAs(t, srv, addr, sbA.ID, ssh.PublicKeys(k)).Close()
	if _, err := tryDial(t, srv, addr, "box", ssh.PublicKeys(k)); err == nil {
		t.Error("an SSH login by name reached a sandbox outside the key's own tenant")
	}
	// bob's key cannot reach aa's sandbox by id.
	_, bob := e.keyed("bob", "", orgScopes...)
	kb, _ := newKey(t)
	if _, err := bob.AddSSHKey(ctx, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(kb.PublicKey()))), ""); err != nil {
		t.Fatal(err)
	}
	if _, err := tryDial(t, srv, addr, sbA.ID, ssh.PublicKeys(kb)); err == nil {
		t.Error("bob's SSH key logged in to a sandbox of an organisation he is not in")
	}
}

// Someone who is not a member gets the 404 of an organisation that does
// not exist, on every kind of endpoint.
func TestOrgHeaderOfANonMemberIsNotFound(t *testing.T) {
	e := startServicesWith(t, withKey, "n1")
	ctx := ctxT(t)
	_, alice := e.keyed("alice", "", orgScopes...)
	mustOrg(t, alice, "aa")
	sbA, err := alice.WithOrg("aa").CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	bobKey, _ := e.keyed("bob", "", orgScopes...)
	for _, org := range []string{"aa", "zz"} {
		for _, r := range []struct{ method, path, body string }{
			{"GET", "/v1/whoami", ""},
			{"GET", "/v1/capabilities", ""},
			{"GET", "/v1/sandboxes", ""},
			{"POST", "/v1/sandboxes", "{}"},
			{"GET", "/v1/sandboxes/" + sbA.ID, ""},
			{"DELETE", "/v1/sandboxes/" + sbA.ID, ""},
			{"GET", "/v1/sandboxes/" + sbA.ID + "/tunnel?port=8080", ""},
			{"POST", "/v1/sandboxes/" + sbA.ID + "/ssh-access", "{}"},
			{"GET", "/v1/secrets", ""},
			{"PUT", "/v1/secrets/X", `{"value":"v"}`},
			{"GET", "/v1/jobs", ""},
			{"GET", "/v1/services", ""},
			{"GET", "/v1/volumes", ""},
			{"GET", "/v1/orgs", ""},
		} {
			code, ec := e.codeOf(r.method, r.path, bobKey, r.body, api.OrgHeader, org)
			if code != http.StatusNotFound || ec != api.CodeNotFound {
				t.Errorf("bob %s %s with %s: %s: %d %s; want 404 not_found", r.method, r.path, api.OrgHeader, org, code, ec)
			}
		}
	}
	if _, err := alice.WithOrg("aa").Sandbox(ctx, sbA.ID); err != nil {
		t.Errorf("the sandbox did not survive bob's attempts: %v", err)
	}
	// Given twice, it is refused rather than one of them picked.
	resp := e.raw("GET", "/v1/sandboxes", bobKey, "")
	resp.Body.Close()
	req, _ := http.NewRequest("GET", e.ts.URL+"/v1/sandboxes", nil)
	req.Header.Set("Authorization", "Bearer "+bobKey)
	req.Header.Add(api.OrgHeader, "default")
	req.Header.Add(api.OrgHeader, "aa")
	resp, err = e.ts.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("two %s headers: %d; want 400", api.OrgHeader, resp.StatusCode)
	}
}

// A key issued before organisations — whose user has no membership — is
// exactly what it was: its own tenant, with or without the header, and
// nothing else, even a tenant that exists.
func TestOrgHeaderChangesNothingForAKeyWithoutMemberships(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1", allCaps...))
	ctx := ctxT(t)
	carolKey, carol := tg.keyed("carol", "team-c", ScopeRead, ScopeCreate, ScopeDelete)
	_, dave := tg.keyed("dave", "team-d", ScopeRead, ScopeCreate, ScopeDelete)
	_, erin := tg.keyed("erin", "", ScopeRead, ScopeCreate, ScopeDelete)
	mine, err := carol.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	theirs, err := dave.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := erin.CreateSandbox(ctx, api.CreateSandboxRequest{}); err != nil {
		t.Fatal(err)
	}
	for _, org := range []string{"team-d", "default", "team-x"} {
		code, ec := tg.codeOf("GET", "/v1/sandboxes", carolKey, "", api.OrgHeader, org)
		if code != http.StatusNotFound || ec != api.CodeNotFound {
			t.Errorf("carol selecting %s: %d %s; want 404", org, code, ec)
		}
		code, _ = tg.codeOf("GET", "/v1/sandboxes/"+theirs.ID, carolKey, "", api.OrgHeader, org)
		if code != http.StatusNotFound {
			t.Errorf("carol selecting %s reached dave's sandbox: %d", org, code)
		}
	}
	for _, c := range []*api.Client{carol, carol.WithOrg("team-c")} {
		got := sandboxIDs(t, c)
		if len(got) != 1 || !got[mine.ID] {
			t.Errorf("carol (org %q) lists %v; want only her own", c.Org(), got)
		}
		w, err := c.Whoami(ctx)
		if err != nil || w.Tenant != "team-c" || w.Org != "team-c" {
			t.Errorf("whoami: %+v %v", w, err)
		}
		orgs, err := c.Orgs(ctx)
		if err != nil || len(orgs) != 1 || orgs[0].Name != "team-c" || !orgs[0].Current || orgs[0].Role != api.RoleMember {
			t.Errorf("orgs: %+v %v", orgs, err)
		}
	}
	// The default tenant is called default.
	if w, err := erin.WithOrg("default").Whoami(ctx); err != nil || w.Org != "default" || w.Tenant != "" {
		t.Errorf("erin's whoami with default: %+v %v", w, err)
	}
}

// The header chooses a tenant and nothing else: a member's key is no more
// an admin's in an organisation than outside one.
func TestOrgHeaderCannotReachAdminEndpoints(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1", allCaps...))
	key, alice := tg.keyed("alice", "", orgScopes...)
	mustOrg(t, alice, "aa")
	for _, org := range []string{"aa", "default"} {
		for _, path := range []string{"/v1/admin/keys", "/v1/admin/nodes", "/v1/admin/orgs", "/v1/admin/audit"} {
			if code, ec := tg.codeOf("GET", path, key, "", api.OrgHeader, org); code != http.StatusForbidden || ec != api.CodeRefused {
				t.Errorf("%s with %s: %d %s; want 403 refused", path, org, code, ec)
			}
		}
	}
	// An admin may act in any organisation, and lists them.
	if w, err := tg.admin.WithOrg("aa").Whoami(ctxT(t)); err != nil || w.Org != "aa" {
		t.Errorf("admin in aa: %+v %v", w, err)
	}
	all, err := tg.admin.AdminOrgs(ctxT(t))
	if err != nil || len(all) != 1 || all[0].Name != "aa" || all[0].Members != 1 || all[0].Owners != 1 || all[0].CreatedBy != "alice" {
		t.Errorf("admin orgs: %+v %v", all, err)
	}
	// Still a 404 for one that does not exist.
	_, err = tg.admin.WithOrg("zz").Whoami(ctxT(t))
	wantCode(t, err, api.CodeNotFound)
}

func TestCreateOrg(t *testing.T) {
	tg := startGateway(t, func(c *Config) { c.MaxOrgsPerUser = 3 }, startNode(t, "n1", allCaps...))
	ctx := ctxT(t)
	_, plain := tg.keyed("pat", "", ScopeRead, ScopeCreate)
	_, err := plain.CreateOrg(ctx, "nope")
	wantCode(t, err, api.CodeRefused)

	_, alice := tg.keyed("alice", "", orgScopes...)
	for _, bad := range []string{"", "A", "Aa", "1a", "-a", "a-", "a--b", "a_b", "a.b", "default", "admin",
		strings.Repeat("a", 31), "a b", "ä"} {
		_, err := alice.CreateOrg(ctx, bad)
		wantCode(t, err, api.CodeInvalidRequest)
	}
	// Taken: by a key's tenant (in any case), a recorded sandbox's tenant, a
	// secret's, and another organisation.
	if _, _, err := tg.store.CreateKey("x", "team-a", []string{ScopeRead}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := tg.store.CreateKey("x", "Team-Z", []string{ScopeRead}); err != nil {
		t.Fatal(err)
	}
	if err := tg.store.SetOwner("sbx_n1_00000000000000aa", Owner{User: "old", Tenant: "legacy", Node: "n1"}); err != nil {
		t.Fatal(err)
	}
	if err := tg.store.PutSecret("vault", "S", []byte("sealed")); err != nil {
		t.Fatal(err)
	}
	mustOrg(t, alice, "a")
	for _, taken := range []string{"team-a", "team-z", "legacy", "vault", "a"} {
		_, err := alice.CreateOrg(ctx, taken)
		wantCode(t, err, api.CodeConflict)
	}
	if o, err := alice.CreateOrg(ctx, "team-x1"); err != nil || o.Role != api.RoleOwner || o.Name != "team-x1" {
		t.Fatalf("create: %+v %v", o, err)
	}
	mustOrg(t, alice, "third")
	// The cap counts what alice made or owns.
	_, err = alice.CreateOrg(ctx, "fourth")
	wantCode(t, err, api.CodeRefused)
	_, bob := tg.keyed("bob", "", orgScopes...)
	mustOrg(t, bob, "bobs")

	orgs, err := alice.Orgs(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, o := range orgs {
		names = append(names, fmt.Sprintf("%s:%s:%v", o.Name, o.Role, o.Current))
	}
	if got := strings.Join(names, " "); got != "default:member:true a:owner:false team-x1:owner:false third:owner:false" {
		t.Errorf("alice's orgs: %s", got)
	}
}

func TestOrgMembers(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	tg := startGateway(t, func(c *Config) { c.Audit = NewAuditLog(auditPath) }, startNode(t, "n1", allCaps...))
	ctx := ctxT(t)
	_, alice := tg.keyed("alice", "", orgScopes...)
	_, bob := tg.keyed("bob", "", ScopeRead, ScopeCreate)
	carolKey, carol := tg.keyed("carol", "", ScopeRead, ScopeCreate)
	mustOrg(t, alice, "aa")

	if m, err := alice.SetOrgMember(ctx, "aa", api.OrgMemberRequest{User: "bob"}); err != nil || m.Role != api.RoleMember {
		t.Fatalf("add bob: %+v %v", m, err)
	}
	if ms, err := bob.OrgMembers(ctx, "aa"); err != nil || len(ms) != 2 {
		t.Errorf("bob reads the members: %+v %v", ms, err)
	}
	// A member may not change the members; someone else does not see it.
	_, err := bob.SetOrgMember(ctx, "aa", api.OrgMemberRequest{User: "carol"})
	wantCode(t, err, api.CodeRefused)
	wantCode(t, bob.RemoveOrgMember(ctx, "aa", "alice", ""), api.CodeRefused)
	_, err = carol.OrgMembers(ctx, "aa")
	wantCode(t, err, api.CodeNotFound)
	_, err = carol.SetOrgMember(ctx, "aa", api.OrgMemberRequest{User: "carol", Role: api.RoleOwner})
	wantCode(t, err, api.CodeNotFound)
	if code, _ := tg.codeOf("POST", "/v1/orgs/aa/members", carolKey, `{"user":"carol"}`, api.OrgHeader, "aa"); code != http.StatusNotFound {
		t.Errorf("carol with the header: %d", code)
	}
	_, err = alice.OrgMembers(ctx, "zz")
	wantCode(t, err, api.CodeNotFound)

	// The last owner stays.
	wantCode(t, alice.RemoveOrgMember(ctx, "aa", "alice", ""), api.CodeConflict)
	_, err = alice.SetOrgMember(ctx, "aa", api.OrgMemberRequest{User: "alice", Role: api.RoleMember})
	wantCode(t, err, api.CodeConflict)
	_, err = alice.SetOrgMember(ctx, "aa", api.OrgMemberRequest{User: "x", Role: "admin"})
	wantCode(t, err, api.CodeInvalidRequest)
	// A second alice, of another tenant, would share the first one's
	// sandboxes in aa: refused.
	_, err = alice.SetOrgMember(ctx, "aa", api.OrgMemberRequest{User: "alice", Tenant: "team-x"})
	wantCode(t, err, api.CodeConflict)

	if m, err := alice.SetOrgMember(ctx, "aa", api.OrgMemberRequest{User: "bob", Role: api.RoleOwner}); err != nil || m.Role != api.RoleOwner {
		t.Fatalf("promote bob: %+v %v", m, err)
	}
	if err := bob.RemoveOrgMember(ctx, "aa", "alice", ""); err != nil {
		t.Fatalf("bob removes alice: %v", err)
	}
	if _, err := alice.WithOrg("aa").Whoami(ctx); !api.IsCode(err, api.CodeNotFound) {
		t.Errorf("alice after removal: %v", err)
	}
	wantCode(t, bob.RemoveOrgMember(ctx, "aa", "alice", ""), api.CodeNotFound)

	_, entries := readAudit(t, auditPath)
	var got []string
	for _, e := range entries {
		if e.Kind == "org" {
			got = append(got, e.Action+" "+e.Target+" "+e.Result+" by "+e.User)
			if e.KeyID == "" || e.Tenant != "aa" {
				t.Errorf("org entry %+v", e)
			}
		}
	}
	want := "org.created aa ok by alice|org.member_added bob member by alice|org.member_role bob owner by alice|org.member_removed alice ok by bob"
	if strings.Join(got, "|") != want {
		t.Errorf("audit:\n%s\nwant\n%s", strings.Join(got, "|"), want)
	}
	for _, e := range entries {
		if strings.Contains(fmt.Sprint(e), "sgk_") {
			t.Errorf("a secret in the audit record: %+v", e)
		}
	}
}

// Removing a member ends, before the call returns, what they had open in
// the organisation: a followed output stream, SSH connections by token and
// by key, a running job. What they hold in their own tenant goes on.
func TestRemovingAMemberEndsTheirAccessAtOnce(t *testing.T) {
	auditPath := filepath.Join(t.TempDir(), "audit.jsonl")
	tg, srv, addr := sshGateway(t, auditPath)
	ctx := ctxT(t)
	_, alice := tg.keyed("alice", "", orgScopes...)
	_, bob := tg.keyed("bob", "", ScopeRead, ScopeCreate, ScopeDelete, ScopeSSH)
	mustOrg(t, alice, "aa")
	if _, err := alice.SetOrgMember(ctx, "aa", api.OrgMemberRequest{User: "bob"}); err != nil {
		t.Fatal(err)
	}
	inA := bob.WithOrg("aa")
	sb, err := inA.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	home, err := bob.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	follow := func(c *api.Client, id string) chan error {
		pr, err := c.StartProcess(ctx, id, api.RunRequest{Argv: []string{"sleep", "60"}})
		if err != nil {
			t.Fatal(err)
		}
		ch := make(chan error, 1)
		go func() { ch <- c.FollowOutput(ctx, id, pr.PID, func(api.OutputEvent) error { return nil }) }()
		return ch
	}
	inOrg, inHome := follow(inA, sb.ID), follow(bob, home.ID)
	waitFor(t, "both streams to open", func() bool { return tg.g.live.len() == 2 })

	acc, err := inA.SSHAccess(ctx, sb.ID, 0)
	if err != nil {
		t.Fatal(err)
	}
	k, _ := newKey(t)
	if _, err := bob.AddSSHKey(ctx, strings.TrimSpace(string(ssh.MarshalAuthorizedKey(k.PublicKey()))), ""); err != nil {
		t.Fatal(err)
	}
	byToken := dialAs(t, srv, addr, acc.User)
	byKey := dialAs(t, srv, addr, sb.ID, ssh.PublicKeys(k))
	byKeyHome := dialAs(t, srv, addr, home.ID, ssh.PublicKeys(k))
	job, err := inA.CreateJob(ctx, api.JobSpec{Command: []string{"sleep", "60"}})
	if err != nil {
		t.Fatal(err)
	}
	waitJob(t, inA, job.ID, "the run to start", func(j api.Job) bool { return j.Runs[0].PID != 0 })

	if err := alice.RemoveOrgMember(ctx, "aa", "bob", ""); err != nil {
		t.Fatal(err)
	}
	if _, ok := ended(inOrg); !ok {
		t.Error("bob's stream in aa stayed open after he was removed")
	}
	if !waitClosed(byToken) || !waitClosed(byKey) {
		t.Error("bob's SSH connection to aa's sandbox stayed open after he was removed")
	}
	waitFor(t, "bob's job in aa to be cancelled", func() bool { r, _ := tg.store.Job(job.ID); return r.State != api.JobRunning })
	if r, _ := tg.store.Job(job.ID); r.State != api.JobCancelled || r.Error != reasonOwnerRevoked {
		t.Errorf("job = %+v", r)
	}
	// What is his own goes on.
	select {
	case err := <-inHome:
		t.Errorf("bob's stream in his own tenant ended: %v", err)
	default:
	}
	if _, _, err := byKeyHome.SendRequest("keepalive@openssh.com", true, nil); err != nil {
		t.Errorf("bob's SSH connection to his own sandbox ended: %v", err)
	}
	// And he cannot come back in.
	_, err = inA.Sandboxes(ctx)
	wantCode(t, err, api.CodeNotFound)
	if _, err := tryDial(t, srv, addr, acc.User); err == nil {
		t.Error("bob's token logged in after he was removed")
	}
	if _, err := tryDial(t, srv, addr, sb.ID, ssh.PublicKeys(k)); err == nil {
		t.Error("bob's key logged in to aa's sandbox after he was removed")
	}
}

// A quota is per organisation: each holds its own.
func TestQuotaIsPerOrg(t *testing.T) {
	tg := startGateway(t, func(c *Config) { c.Quota = Quota{Sandboxes: 1} }, startNode(t, "n1", allCaps...))
	ctx := ctxT(t)
	_, alice := tg.keyed("alice", "", orgScopes...)
	mustOrg(t, alice, "aa")
	for name, c := range map[string]*api.Client{"home": alice, "aa": alice.WithOrg("aa")} {
		if _, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{}); err != nil {
			t.Fatalf("%s: first: %v", name, err)
		}
		_, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{})
		wantCode(t, err, api.CodeRefused)
	}
}

// A state file from before organisations loads, and keeps working.
func TestStateFileFromBeforeOrgsLoads(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	old := `{
  "version": 1,
  "keys": [{"id": "key_0011223344556677", "user": "alice", "tenant": "", "scopes": ["sandbox:read"],
            "hash": "` + hashSecret("sgk_old") + `", "created": "2026-01-01T00:00:00Z"}],
  "ssh_keys": [],
  "ssh_tokens": [],
  "sandboxes": {},
  "volumes": {},
  "snapshots": {},
  "nodes": []
}
`
	if err := os.WriteFile(path, []byte(old), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := OpenFileStore(path)
	if err != nil {
		t.Fatalf("an old state file: %v", err)
	}
	if k, ok := st.KeyBySecret("sgk_old"); !ok || k.User != "alice" {
		t.Errorf("the old key: %+v %v", k, ok)
	}
	if len(st.Orgs()) != 0 || len(st.MembershipsOf("alice", "")) != 0 {
		t.Error("an old state file has organisations")
	}
	if _, err := st.CreateOrg("aa", "alice", "", 0); err != nil {
		t.Fatal(err)
	}
	st.Close()
	st, err = OpenFileStore(path)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if role, ok := st.MemberRole("aa", "alice", ""); !ok || role != api.RoleOwner {
		t.Errorf("after a reopen: %q %v", role, ok)
	}
	fi, err := os.Stat(path)
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("state file mode: %v %v", fi.Mode(), err)
	}
}

// An admin cannot issue a key that would make a second user of one name in
// an organisation, either.
func TestKeyIntoAnOrgOfAMembersName(t *testing.T) {
	tg := startGateway(t, nil)
	_, alice := tg.keyed("alice", "", orgScopes...)
	mustOrg(t, alice, "aa")
	if _, _, err := tg.store.CreateKey("alice", "aa", []string{ScopeRead}); err == nil {
		t.Error("a key for a second alice in aa was issued")
	}
	if _, _, err := tg.store.CreateKey("ci", "aa", []string{ScopeRead}); err != nil {
		t.Errorf("a key into aa: %v", err)
	}
	_ = context.Background
}

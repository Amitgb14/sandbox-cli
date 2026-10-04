package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// adminDo sends an admin request and decodes the reply.
func (tg *testGateway) adminDo(method, path string, in, out any) int {
	tg.t.Helper()
	secret, _, err := tg.store.CreateKey("root", "", []string{ScopeAdmin})
	if err != nil {
		tg.t.Fatal(err)
	}
	return tg.do(secret, method, path, in, out)
}

func (tg *testGateway) do(secret, method, path string, in, out any) int {
	tg.t.Helper()
	body := ""
	if in != nil {
		b, _ := json.Marshal(in)
		body = string(b)
	}
	resp := tg.raw(method, path, secret, body)
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	if out != nil && resp.StatusCode < 300 {
		if err := json.Unmarshal(data, out); err != nil {
			tg.t.Fatalf("%s %s: %v: %s", method, path, err, data)
		}
	}
	if eb, ok := out.(*api.ErrorBody); ok && resp.StatusCode >= 300 {
		_ = json.Unmarshal(data, eb)
	}
	return resp.StatusCode
}

func nodeOf(tg *testGateway, id string) string {
	o, ok := tg.g.ownerOf(id)
	if !ok {
		tg.t.Fatalf("no owner recorded for %s", id)
	}
	return o.Node
}

// Names are per user, so a node refusing one because another user's sandbox
// there has it is not the answer: the next node is tried.
func TestNameClashOnANodeTriesAnother(t *testing.T) {
	big := startNode(t, "n1", allCaps...)
	big.capacity.MemoryMB = 1 << 20 // every first choice is n1
	tg := startGateway(t, nil, big, startNode(t, "n2", allCaps...))
	ctx := ctxT(t)
	a, err := tg.user("alice").CreateSandbox(ctx, api.CreateSandboxRequest{Name: "web"})
	if err != nil {
		t.Fatal(err)
	}
	b, err := tg.user("bob").CreateSandbox(ctx, api.CreateSandboxRequest{Name: "web"})
	if err != nil {
		t.Fatalf("bob's web: %v", err)
	}
	if nodeOf(tg, a.ID) != "n1" || nodeOf(tg, b.ID) != "n2" {
		t.Fatalf("alice on %s, bob on %s", nodeOf(tg, a.ID), nodeOf(tg, b.ID))
	}
	// A third user with nowhere left to go hears the node's conflict.
	_, err = tg.user("carol").CreateSandbox(ctx, api.CreateSandboxRequest{Name: "web"})
	wantCode(t, err, api.CodeConflict)
}

// After a DELETE the gateway forgets the sandbox, but its owner can still
// read its final state and its events, and delete it again, as on a node.
func TestDeleteForgetsAndStillAnswersTheOwner(t *testing.T) {
	tg := startGateway(t, func(c *Config) { c.Quota.Sandboxes = 5 }, startNode(t, "n1", allCaps...))
	alice := tg.user("alice")
	ctx := ctxT(t)
	sb, err := alice.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if u := tg.store.UsageOf(""); u.Sandboxes != 1 || u.CPUs != sb.CPUs || u.MemoryMB != sb.MemoryMB {
		t.Fatalf("usage %+v after one create of %+v", u, sb)
	}
	if err := alice.TerminateSandbox(ctx, sb.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := tg.store.OwnerOf(sb.ID); ok {
		t.Fatal("the store still holds a deleted sandbox")
	}
	if u := tg.store.UsageOf(""); u.Sandboxes != 0 {
		t.Fatalf("a deleted sandbox still counts: %+v", u)
	}
	got, err := alice.Sandbox(ctx, sb.ID)
	if err != nil || got.State != api.StateTerminated {
		t.Fatalf("after delete: %+v, %v", got, err)
	}
	if err := alice.TerminateSandbox(ctx, sb.ID); err != nil {
		t.Fatalf("second delete: %v", err)
	}
	if _, err := alice.Events(ctx, sb.ID); err != nil {
		t.Fatalf("events after delete: %v", err)
	}
	_, err = tg.user("bob").Sandbox(ctx, sb.ID)
	wantCode(t, err, api.CodeNotFound)
}

// A node that stops answering gets no new sandboxes; what it holds answers
// "not answering", and it comes back when it answers again.
func TestUnhealthyNodeGetsNoNewSandboxes(t *testing.T) {
	n1, n2 := startNode(t, "n1", allCaps...), startNode(t, "n2", allCaps...)
	tg := startGateway(t, nil, n1, n2)
	alice := tg.user("alice")
	ctx := ctxT(t)
	on1 := ""
	for on1 == "" {
		sb, err := alice.CreateSandbox(ctx, api.CreateSandboxRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if nodeOf(tg, sb.ID) == "n1" {
			on1 = sb.ID
		}
	}
	n1.down.Store(true)
	waitFor(t, "n1 to be unhealthy", func() bool { return !tg.g.nodes.get("n1").isHealthy() })
	for range 5 {
		sb, err := alice.CreateSandbox(ctx, api.CreateSandboxRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if nodeOf(tg, sb.ID) != "n2" {
			t.Fatal("a sandbox was placed on an unhealthy node")
		}
	}
	resp := tg.raw(http.MethodGet, "/v1/sandboxes/"+on1, "", "")
	resp.Body.Close()
	_, err := alice.Sandbox(ctx, on1)
	// unavailable, not internal: a node that is down is not a bug, and a
	// client may try again.
	if e, ok := err.(*api.Error); !ok || e.Status != http.StatusServiceUnavailable || e.Code != api.CodeUnavailable {
		t.Fatalf("a sandbox on a down node: %v", err)
	}
	var info api.NodeList
	tg.adminDo(http.MethodGet, "/v1/admin/nodes", nil, &info)
	if info.Nodes[0].Healthy || info.Nodes[0].Error == "" || !info.Nodes[1].Healthy {
		t.Fatalf("nodes %+v", info.Nodes)
	}
	n1.down.Store(false)
	waitFor(t, "n1 to come back", func() bool { return tg.g.nodes.get("n1").isHealthy() })
	if _, err := alice.Sandbox(ctx, on1); err != nil {
		t.Fatal(err)
	}
}

// A node reporting another node's name would make ids route wrongly; it is
// unhealthy until that is fixed.
func TestNodeReportingAnotherNameIsUnhealthy(t *testing.T) {
	n1 := startNode(t, "n1", allCaps...)
	other := "n9"
	n1.reportAs.Store(&other)
	tg := startGateway(t, nil, n1)
	var list api.NodeList
	tg.adminDo(http.MethodGet, "/v1/admin/nodes", nil, &list)
	if list.Nodes[0].Healthy || !strings.Contains(list.Nodes[0].Error, "n9") {
		t.Fatalf("node %+v", list.Nodes[0])
	}
	same := "n1"
	n1.reportAs.Store(&same)
	tg.g.PollNow(context.Background())
	if !tg.g.nodes.get("n1").isHealthy() {
		t.Fatal("a node reporting its own name is not healthy")
	}
}

func TestCordonThroughTheAdminAPI(t *testing.T) {
	n1, n2 := startNode(t, "n1", allCaps...), startNode(t, "n2", allCaps...)
	tg := startGateway(t, nil, n1, n2)
	alice := tg.user("alice")
	var eb api.ErrorBody
	secret, _, _ := tg.store.CreateKey("alice", "", []string{ScopeRead, ScopeCreate})
	if code := tg.do(secret, http.MethodPost, "/v1/admin/nodes/n1/cordon", api.CordonRequest{Cordoned: true}, &eb); code != http.StatusForbidden {
		t.Fatalf("cordon without admin: %d", code)
	}
	var info api.NodeInfo
	if code := tg.adminDo(http.MethodPost, "/v1/admin/nodes/n1/cordon", api.CordonRequest{Cordoned: true}, &info); code != http.StatusOK {
		t.Fatalf("cordon: %d", code)
	}
	if !n1.cordoned.Load() || info.Status == nil || !info.Status.Cordoned {
		t.Fatalf("the node was not cordoned: %+v", info)
	}
	for range 4 {
		sb, err := alice.CreateSandbox(ctxT(t), api.CreateSandboxRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if nodeOf(tg, sb.ID) != "n2" {
			t.Fatal("a sandbox was placed on a cordoned node")
		}
	}
	if code := tg.adminDo(http.MethodPost, "/v1/admin/nodes/nope/cordon", api.CordonRequest{}, nil); code != http.StatusNotFound {
		t.Fatalf("cordon an unknown node: %d", code)
	}
}

func TestQuota(t *testing.T) {
	tg := startGateway(t, func(c *Config) { c.Quota = Quota{Sandboxes: 2, CPUs: 3} }, startNode(t, "n1", allCaps...))
	a := tg.client("alice", "acme", ScopeRead, ScopeCreate, ScopeDelete)
	b := tg.client("bob", "acme", ScopeRead, ScopeCreate, ScopeDelete)
	other := tg.client("carol", "globex", ScopeRead, ScopeCreate, ScopeDelete)
	ctx := ctxT(t)
	first, err := a.CreateSandbox(ctx, api.CreateSandboxRequest{CPUs: 2})
	if err != nil {
		t.Fatal(err)
	}
	// The quota is the tenant's, so bob's create counts alice's.
	_, err = b.CreateSandbox(ctx, api.CreateSandboxRequest{CPUs: 2})
	wantCode(t, err, api.CodeRefused)
	if _, err := b.CreateSandbox(ctx, api.CreateSandboxRequest{CPUs: 1}); err != nil {
		t.Fatal(err)
	}
	_, err = a.CreateSandbox(ctx, api.CreateSandboxRequest{CPUs: 0.5})
	wantCode(t, err, api.CodeRefused)
	if _, err := other.CreateSandbox(ctx, api.CreateSandboxRequest{CPUs: 2}); err != nil {
		t.Fatalf("another tenant: %v", err)
	}
	if err := a.TerminateSandbox(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := a.CreateSandbox(ctx, api.CreateSandboxRequest{CPUs: 2}); err != nil {
		t.Fatalf("after a delete frees room: %v", err)
	}
}

type fakeSSH struct{}

func (fakeSSH) Info() api.SSHInfo {
	return api.SSHInfo{Host: "ssh.example.test", Port: 2222, HostKeys: []string{"ssh-ed25519 AAAA"}, Fingerprint: "SHA256:x"}
}

func TestSSHEndpoints(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1", allCaps...))
	ctx := ctxT(t)
	alice := tg.user("alice")
	sb, err := alice.CreateSandbox(ctx, api.CreateSandboxRequest{Name: "box"})
	if err != nil {
		t.Fatal(err)
	}
	bsb, err := tg.user("bob").CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	aliceKey, _, _ := tg.store.CreateKey("alice", "", []string{ScopeSSH, ScopeRead})
	bobKey, _, _ := tg.store.CreateKey("bob", "", []string{ScopeSSH})
	noSSH, _, _ := tg.store.CreateKey("alice", "", []string{ScopeRead, ScopeCreate})

	var eb api.ErrorBody
	if code := tg.do(aliceKey, http.MethodGet, "/v1/ssh", nil, &eb); code != http.StatusNotFound || eb.Error.Code != api.CodeUnsupported {
		t.Fatalf("ssh off: %d %+v", code, eb)
	}
	if code := tg.do(aliceKey, http.MethodPost, "/v1/sandboxes/box/ssh-access", api.SSHAccessRequest{}, &eb); code != http.StatusNotFound || eb.Error.Code != api.CodeUnsupported {
		t.Fatalf("ssh-access with ssh off: %d %+v", code, eb)
	}
	tg.g.SetSSH(fakeSSH{})
	var info api.SSHInfo
	if code := tg.do(aliceKey, http.MethodGet, "/v1/ssh", nil, &info); code != http.StatusOK || info.Port != 2222 {
		t.Fatalf("ssh info: %d %+v", code, info)
	}

	var acc api.SSHAccess
	before := time.Now()
	if code := tg.do(aliceKey, http.MethodPost, "/v1/sandboxes/box/ssh-access", api.SSHAccessRequest{}, &acc); code != http.StatusCreated {
		t.Fatalf("ssh-access: %d", code)
	}
	if !strings.HasPrefix(acc.User, "sgt_") || acc.Command != "ssh -p 2222 "+acc.User+"@ssh.example.test" {
		t.Fatalf("access %+v", acc)
	}
	if d := acc.ExpiresAt.Sub(before); d < 14*time.Minute || d > 16*time.Minute {
		t.Fatalf("default lifetime %v", d)
	}
	tok, ok := tg.store.RedeemSSHToken(acc.User)
	if !ok || tok.Sandbox != sb.ID || tok.User != "alice" {
		t.Fatalf("redeemed %+v %v", tok, ok)
	}
	if code := tg.do(aliceKey, http.MethodPost, "/v1/sandboxes/box/ssh-access", api.SSHAccessRequest{TTLSecs: 25 * 3600}, &eb); code != http.StatusBadRequest {
		t.Fatalf("ttl over the maximum: %d", code)
	}
	if code := tg.do(aliceKey, http.MethodPost, "/v1/sandboxes/box/ssh-access", api.SSHAccessRequest{TTLSecs: 60}, &acc); code != http.StatusCreated {
		t.Fatalf("ttl 60: %d", code)
	}
	if code := tg.do(aliceKey, http.MethodPost, "/v1/sandboxes/"+bsb.ID+"/ssh-access", api.SSHAccessRequest{}, &eb); code != http.StatusNotFound {
		t.Fatalf("access to bob's sandbox: %d", code)
	}
	if code := tg.do(noSSH, http.MethodPost, "/v1/sandboxes/box/ssh-access", api.SSHAccessRequest{}, &eb); code != http.StatusForbidden {
		t.Fatalf("access without the ssh scope: %d", code)
	}

	// SSH keys.
	if code := tg.do(aliceKey, http.MethodPost, "/v1/ssh-keys", api.SSHKeyRequest{Key: `command="sh" ` + ed25519Line(t, "")}, &eb); code != http.StatusBadRequest {
		t.Fatalf("a key with options: %d", code)
	}
	var k api.SSHKeyInfo
	line := ed25519Line(t, "alice@laptop")
	if code := tg.do(aliceKey, http.MethodPost, "/v1/ssh-keys", api.SSHKeyRequest{Key: line, Sandbox: "box"}, &k); code != http.StatusCreated {
		t.Fatalf("add key: %d", code)
	}
	if k.Sandbox != sb.ID || k.Key != line {
		t.Fatalf("key %+v: want it limited to %s by id", k, sb.ID)
	}
	if code := tg.do(bobKey, http.MethodPost, "/v1/ssh-keys", api.SSHKeyRequest{Key: line}, &eb); code != http.StatusConflict {
		t.Fatalf("bob registering alice's key: %d", code)
	}
	if code := tg.do(bobKey, http.MethodPost, "/v1/ssh-keys", api.SSHKeyRequest{Key: ed25519Line(t, ""), Sandbox: sb.ID}, &eb); code != http.StatusNotFound {
		t.Fatalf("bob limiting a key to alice's sandbox: %d", code)
	}
	var list api.SSHKeyList
	tg.do(aliceKey, http.MethodGet, "/v1/ssh-keys", nil, &list)
	if len(list.Keys) != 1 {
		t.Fatalf("alice's keys %+v", list)
	}
	tg.do(bobKey, http.MethodGet, "/v1/ssh-keys", nil, &list)
	if len(list.Keys) != 0 {
		t.Fatalf("bob sees %d keys", len(list.Keys))
	}
	if code := tg.do(bobKey, http.MethodDelete, "/v1/ssh-keys/"+k.ID, nil, nil); code != http.StatusNotFound {
		t.Fatalf("bob deleting alice's key: %d", code)
	}
	if code := tg.do(aliceKey, http.MethodDelete, "/v1/ssh-keys/"+k.ID, nil, nil); code != http.StatusNoContent {
		t.Fatalf("delete key: %d", code)
	}
}

func TestAdminKeysAndNodes(t *testing.T) {
	n1 := startNode(t, "n1", allCaps...)
	filesDir := t.TempDir()
	tg := startGateway(t, func(c *Config) { c.NodeFilesDir = filesDir }, n1)
	var created api.CreatedKey
	if code := tg.adminDo(http.MethodPost, "/v1/admin/keys", api.CreateKeyRequest{User: "dana", Scopes: []string{ScopeRead}}, &created); code != http.StatusCreated {
		t.Fatalf("create key: %d", code)
	}
	if !strings.HasPrefix(created.Secret, "sgk_") {
		t.Fatalf("created %+v", created)
	}
	var who api.Whoami
	if code := tg.do(created.Secret, http.MethodGet, "/v1/whoami", nil, &who); code != http.StatusOK || who.User != "dana" || who.KeyID != created.ID {
		t.Fatalf("whoami %d %+v", code, who)
	}
	if code := tg.adminDo(http.MethodPost, "/v1/admin/keys", api.CreateKeyRequest{User: "dana", Scopes: []string{"everything"}}, nil); code != http.StatusBadRequest {
		t.Fatalf("an unknown scope: %d", code)
	}
	resp := tg.raw(http.MethodGet, "/v1/admin/keys", created.Secret, "")
	resp.Body.Close()
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("list keys without admin: %d", resp.StatusCode)
	}
	secret, _, _ := tg.store.CreateKey("root", "", []string{ScopeAdmin})
	resp = tg.raw(http.MethodGet, "/v1/admin/keys", secret, "")
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if strings.Contains(string(body), created.Secret) || strings.Contains(string(body), `"hash"`) {
		t.Fatal("the key listing carries a secret or a hash")
	}
	if code := tg.adminDo(http.MethodDelete, "/v1/admin/keys/"+created.ID, nil, nil); code != http.StatusNoContent {
		t.Fatalf("revoke: %d", code)
	}
	if code := tg.do(created.Secret, http.MethodGet, "/v1/whoami", nil, nil); code != http.StatusUnauthorized {
		t.Fatalf("a revoked key: %d", code)
	}
	if code := tg.adminDo(http.MethodDelete, "/v1/admin/keys/key_nope", nil, nil); code != http.StatusNotFound {
		t.Fatalf("revoke unknown: %d", code)
	}

	// Nodes: a second node, its token file inside the node files directory.
	n2 := startNode(t, "n2", allCaps...)
	outside := n2.config()
	var eb api.ErrorBody
	if code := tg.adminDo(http.MethodPost, "/v1/admin/nodes", api.NodeSpec{Name: "n2", Endpoint: outside.Endpoint, TokenFile: outside.TokenFile}, &eb); code != http.StatusForbidden {
		t.Fatalf("a token file outside the node files directory: %d %+v", code, eb)
	}
	inside := filepath.Join(filesDir, "n2.token")
	if err := os.WriteFile(inside, []byte(n2.token), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside.TokenFile, filepath.Join(filesDir, "link")); err != nil {
		t.Fatal(err)
	}
	if code := tg.adminDo(http.MethodPost, "/v1/admin/nodes", api.NodeSpec{Name: "n2", Endpoint: outside.Endpoint, TokenFile: filepath.Join(filesDir, "link")}, &eb); code != http.StatusForbidden {
		t.Fatalf("a symlink out of the node files directory: %d", code)
	}
	if code := tg.adminDo(http.MethodPost, "/v1/admin/nodes", api.NodeSpec{Name: "n2", Endpoint: "http://192.0.2.1:7070"}, &eb); code != http.StatusBadRequest {
		t.Fatalf("plain http to another machine: %d", code)
	}
	var info api.NodeInfo
	if code := tg.adminDo(http.MethodPost, "/v1/admin/nodes", api.NodeSpec{Name: "n2", Endpoint: outside.Endpoint, TokenFile: inside}, &info); code != http.StatusCreated {
		t.Fatalf("add node: %d", code)
	}
	if !info.Healthy || info.Status == nil {
		t.Fatalf("added node %+v", info)
	}
	if len(tg.store.Nodes()) != 1 {
		t.Fatal("the added node is not in the state")
	}
	var list api.NodeList
	tg.adminDo(http.MethodGet, "/v1/admin/nodes", nil, &list)
	if len(list.Nodes) != 2 {
		t.Fatalf("nodes %+v", list)
	}
	if code := tg.adminDo(http.MethodDelete, "/v1/admin/nodes/n1", nil, nil); code != http.StatusConflict {
		t.Fatalf("remove a node from the config file: %d", code)
	}
	if code := tg.adminDo(http.MethodPost, "/v1/admin/nodes", api.NodeSpec{Name: "n1", Endpoint: outside.Endpoint}, nil); code != http.StatusConflict {
		t.Fatalf("replace a node from the config file: %d", code)
	}
	if code := tg.adminDo(http.MethodDelete, "/v1/admin/nodes/n2", nil, nil); code != http.StatusNoContent {
		t.Fatalf("remove node: %d", code)
	}
	if len(tg.store.Nodes()) != 0 || tg.g.nodes.get("n2") != nil {
		t.Fatal("the removed node is still known")
	}
}

// A sandbox that ends without a DELETE through the gateway — here, an idle
// timeout on its node — is forgotten by the reconcile, and stops counting.
func TestReconcileForgetsSandboxesThatEnded(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1", allCaps...))
	alice := tg.user("alice")
	ctx := ctxT(t)
	sb, err := alice.CreateSandbox(ctx, api.CreateSandboxRequest{IdleTimeoutSecs: 1})
	if err != nil {
		t.Fatal(err)
	}
	keep, err := alice.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	// Watched through the node's listing: a GET would touch it and keep it alive.
	waitFor(t, "the idle sandbox to end", func() bool {
		var list api.SandboxList
		tg.nodes[0].inner("/v1/sandboxes", &list)
		for _, s := range list.Sandboxes {
			if s.ID == sb.ID {
				return s.State == api.StateTerminated
			}
		}
		return false
	})
	tg.g.reconcile(ctx)
	if _, ok := tg.store.OwnerOf(sb.ID); ok {
		t.Fatal("an ended sandbox is still recorded")
	}
	if _, ok := tg.store.OwnerOf(keep.ID); !ok {
		t.Fatal("a running sandbox was forgotten")
	}
	if got, err := alice.Sandbox(ctx, sb.ID); err != nil || got.State != api.StateTerminated {
		t.Fatalf("the owner can no longer read it: %+v %v", got, err)
	}
	// One recorded on the node and gone from it (a node restart) is forgotten too.
	if err := tg.store.SetOwner("sbx_00000000000000aa", Owner{User: "alice", Node: "n1"}); err != nil {
		t.Fatal(err)
	}
	tg.g.reconcile(ctx)
	if _, ok := tg.store.OwnerOf("sbx_00000000000000aa"); ok {
		t.Fatal("a sandbox the node does not have is still recorded")
	}
}

func TestVolumesAndSnapshotsAreOwned(t *testing.T) {
	tg := startGateway(t, nil, startNode(t, "n1", allCaps...), startNode(t, "n2", allCaps...), startNode(t, "n3", allCaps...))
	alice, bob := tg.user("alice"), tg.user("bob")
	ctx := ctxT(t)
	if _, err := alice.CreateVolume(ctx, api.CreateVolumeRequest{Name: "data", SizeMB: 64}); err != nil {
		t.Fatal(err)
	}
	if _, err := alice.CreateVolume(ctx, api.CreateVolumeRequest{Name: "more", SizeMB: 64}); err != nil {
		t.Fatal(err)
	}
	vo, _ := tg.store.VolumeOwner("data")
	mo, _ := tg.store.VolumeOwner("more")
	if vo.Node != mo.Node {
		t.Fatalf("alice's volumes are on %s and %s; they go together", vo.Node, mo.Node)
	}
	_, err := bob.CreateVolume(ctx, api.CreateVolumeRequest{Name: "data"})
	wantCode(t, err, api.CodeConflict)
	wantCode(t, bob.DeleteVolume(ctx, "data"), api.CodeNotFound)
	_, err = bob.CreateSandbox(ctx, api.CreateSandboxRequest{Volumes: []api.VolumeMount{{Name: "data", Path: "/data"}}})
	wantCode(t, err, api.CodeNotFound)
	if vols, _ := bob.Volumes(ctx); len(vols) != 0 {
		t.Fatalf("bob sees %v", vols)
	}
	sb, err := alice.CreateSandbox(ctx, api.CreateSandboxRequest{Volumes: []api.VolumeMount{{Name: "data", Path: "/data"}, {Name: "more", Path: "/more"}}})
	if err != nil {
		t.Fatal(err)
	}
	if nodeOf(tg, sb.ID) != vo.Node {
		t.Fatalf("the sandbox is on %s, its volumes on %s", nodeOf(tg, sb.ID), vo.Node)
	}

	src, err := alice.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	snap, err := alice.CreateSnapshot(ctx, src.ID)
	if err != nil {
		t.Fatal(err)
	}
	if so, ok := tg.store.SnapshotOwner(snap.ID); !ok || so.User != "alice" || so.Node != nodeOf(tg, src.ID) {
		t.Fatalf("snapshot owner %+v %v", so, ok)
	}
	if list, _ := bob.Snapshots(ctx); len(list) != 0 {
		t.Fatalf("bob sees snapshots %v", list)
	}
	_, err = bob.CreateSandbox(ctx, api.CreateSandboxRequest{SnapshotID: snap.ID})
	wantCode(t, err, api.CodeNotFound)
	wantCode(t, bob.DeleteSnapshot(ctx, snap.ID), api.CodeNotFound)
	fork, err := alice.CreateSandbox(ctx, api.CreateSandboxRequest{SnapshotID: snap.ID})
	if err != nil {
		t.Fatal(err)
	}
	if nodeOf(tg, fork.ID) != nodeOf(tg, src.ID) {
		t.Fatal("the fork is not on its snapshot's node")
	}
	if err := alice.DeleteSnapshot(ctx, snap.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := tg.store.SnapshotOwner(snap.ID); ok {
		t.Fatal("a deleted snapshot is still recorded")
	}
}

// In a fleet where only some nodes have volumes and snapshots, the fleet's
// combined capabilities have neither, and the create path once skipped the
// ownership check on that account: alice, naming bob's volume or snapshot,
// was placed on the node holding it and got it mounted or restored.
func TestMixedFleetVolumesAndSnapshotsStayOwned(t *testing.T) {
	full := startNode(t, "n1", allCaps...)
	bare := startNode(t, "n2", api.CapNetworkPolicyUpdate, api.CapEgressAllowlist, api.CapSuspend)
	tg := startGateway(t, nil, full, bare)
	alice, bob := tg.user("alice"), tg.user("bob")
	ctx := ctxT(t)
	if caps, err := alice.Capabilities(ctx); err != nil || caps.Has(api.CapVolumes) || caps.Has(api.CapMemorySnapshot) {
		t.Fatalf("precondition: the fleet's combined capabilities have volumes or snapshots (%v)", err)
	}
	if _, err := bob.CreateVolume(ctx, api.CreateVolumeRequest{Name: "data", SizeMB: 64}); err != nil {
		t.Fatal(err)
	}
	// The bare node cordoned, so a create that is not pinned still lands
	// where bob's things are: the trap is armed whatever the scheduler picks.
	bare.cordoned.Store(true)
	tg.g.PollNow(ctx)
	src, err := bob.CreateSandbox(ctx, api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if nodeOf(tg, src.ID) != "n1" {
		t.Fatal("precondition: bob's sandbox is not on n1")
	}
	snap, err := bob.CreateSnapshot(ctx, src.ID)
	if err != nil {
		t.Fatal(err)
	}

	_, err = alice.CreateSandbox(ctx, api.CreateSandboxRequest{Volumes: []api.VolumeMount{{Name: "data", Path: "/data"}}})
	wantCode(t, err, api.CodeNotFound)
	_, err = alice.CreateSandbox(ctx, api.CreateSandboxRequest{SnapshotID: snap.ID})
	wantCode(t, err, api.CodeNotFound)

	// Bob's own still work, and go to the node that holds them even with
	// the other one taking sandboxes again. Volumes are made, and listed,
	// where some node has them.
	bare.cordoned.Store(false)
	tg.g.PollNow(ctx)
	for _, name := range []string{"v1", "v2", "v3"} {
		if _, err := bob.CreateVolume(ctx, api.CreateVolumeRequest{Name: name, SizeMB: 8}); err != nil {
			t.Fatalf("volume %s in a mixed fleet: %v", name, err)
		}
		if o, _ := tg.store.VolumeOwner(name); o.Node != "n1" {
			t.Fatalf("volume %s went to %s, which has no volumes", name, o.Node)
		}
	}
	if vols, err := bob.Volumes(ctx); err != nil || len(vols) != 4 {
		t.Fatalf("bob's volumes in a mixed fleet: %v %v", vols, err)
	}
	for i := 0; i < 4; i++ {
		sb, err := bob.CreateSandbox(ctx, api.CreateSandboxRequest{SnapshotID: snap.ID})
		if err != nil {
			t.Fatal(err)
		}
		if nodeOf(tg, sb.ID) != "n1" {
			t.Fatalf("a fork of bob's snapshot went to %s", nodeOf(tg, sb.ID))
		}
		sb, err = bob.CreateSandbox(ctx, api.CreateSandboxRequest{Volumes: []api.VolumeMount{{Name: "data", Path: "/data", ReadOnly: true}}})
		if err != nil {
			t.Fatal(err)
		}
		if nodeOf(tg, sb.ID) != "n1" {
			t.Fatalf("a mount of bob's volume went to %s", nodeOf(tg, sb.ID))
		}
	}
}

func TestNoNodeAnswering(t *testing.T) {
	n1 := startNode(t, "n1", allCaps...)
	n1.down.Store(true)
	tg := startGateway(t, nil, n1)
	alice := tg.user("alice")
	_, err := alice.Capabilities(ctxT(t))
	if e, ok := err.(*api.Error); !ok || e.Status != http.StatusServiceUnavailable || e.Code != api.CodeUnavailable {
		t.Fatalf("capabilities with no node: %v", err)
	}
	_, err = alice.CreateSandbox(ctxT(t), api.CreateSandboxRequest{})
	if e, ok := err.(*api.Error); !ok || e.Status != http.StatusServiceUnavailable || e.Code != api.CodeUnavailable {
		t.Fatalf("create with no node: %v", err)
	}
	_, err = alice.CreateVolume(ctxT(t), api.CreateVolumeRequest{Name: "cache"})
	if e, ok := err.(*api.Error); !ok || e.Status != http.StatusServiceUnavailable || e.Code != api.CodeUnavailable {
		t.Fatalf("volume with no node: %v", err)
	}
}

// A fleet whose every node is cordoned answers unavailable, as one cordoned
// node does: the client is told to wait or go elsewhere, not that it hit a bug.
func TestEveryNodeCordoned(t *testing.T) {
	n1 := startNode(t, "n1", allCaps...)
	n1.cordoned.Store(true)
	tg := startGateway(t, nil, n1)
	_, err := tg.user("alice").CreateSandbox(ctxT(t), api.CreateSandboxRequest{})
	if e, ok := err.(*api.Error); !ok || e.Status != http.StatusServiceUnavailable || e.Code != api.CodeUnavailable {
		t.Fatalf("create on a cordoned fleet: %v", err)
	}
}

// A node may make ids that name it; routing then agrees with the store.
func TestNodeConfigChecks(t *testing.T) {
	for _, c := range []struct {
		cfg NodeConfig
		ok  bool
	}{
		{NodeConfig{Name: "n1", Endpoint: "https://node1.example:7443"}, true},
		{NodeConfig{Name: "n1", Endpoint: "unix:///run/sandboxd.sock"}, true},
		{NodeConfig{Name: "n1", Endpoint: "http://127.0.0.1:7070"}, true},
		{NodeConfig{Name: "n1", Endpoint: "http://node1.example:7070"}, false},
		{NodeConfig{Name: "N1", Endpoint: "unix:///x"}, false},
		{NodeConfig{Name: "n_1", Endpoint: "unix:///x"}, false},
		{NodeConfig{Name: "n1", Endpoint: "unix://"}, false},
		{NodeConfig{Name: "n1", Endpoint: "ftp://x"}, false},
		{NodeConfig{Name: "n1", Endpoint: "https://x", CertFile: "c"}, false},
		{NodeConfig{Name: "n1", Endpoint: "http://127.0.0.1:1", CAFile: "ca"}, false},
	} {
		if err := CheckNodeConfig(c.cfg); (err == nil) != c.ok {
			t.Errorf("%+v: err = %v", c.cfg, err)
		}
	}
	dir := t.TempDir()
	tok := filepath.Join(dir, "token")
	if err := os.WriteFile(tok, []byte("secret-token-value\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := NewNodeClient(NodeConfig{Name: "n1", Endpoint: "unix:///x", TokenFile: tok}); err == nil || !strings.Contains(err.Error(), "readable by others") {
		t.Fatalf("a token file others can read: %v", err)
	}
}

// A node that stops answering between polls is still marked healthy for a
// while. A create placed on it must not fail when the request never reached
// it: the next node is tried. A call to a sandbox on it is unavailable, as
// once the gateway knows the node is down — not a bug, and worth a retry.
func TestNodeGoneBetweenPolls(t *testing.T) {
	n1 := startNode(t, "n1", allCaps...)
	n2 := startNode(t, "n2", allCaps...)
	tg := startGateway(t, func(c *Config) { c.PollInterval = time.Hour }, n1, n2)
	alice := tg.user("alice")
	ctx := ctxT(t)
	var onN2 string
	for range 6 {
		sb, err := alice.CreateSandbox(ctx, api.CreateSandboxRequest{})
		if err != nil {
			t.Fatal(err)
		}
		if nodeOf(tg, sb.ID) == "n2" {
			onN2 = sb.ID
			break
		}
	}
	if onN2 == "" {
		t.Fatal("no sandbox was placed on n2")
	}
	n2.ts.Close() // connections to it are refused from now on

	for i := range 6 {
		sb, err := alice.CreateSandbox(ctx, api.CreateSandboxRequest{})
		if err != nil {
			t.Fatalf("create %d with n2 gone but not yet noticed: %v", i, err)
		}
		if nodeOf(tg, sb.ID) != "n1" {
			t.Fatalf("create %d landed on %s", i, sb.ID)
		}
	}
	_, err := alice.Sandbox(ctx, onN2)
	if e, ok := err.(*api.Error); !ok || e.Status != http.StatusServiceUnavailable || e.Code != api.CodeUnavailable {
		t.Fatalf("a sandbox on a node that stopped answering: %v", err)
	}
	// The listing leaves the node out, as it does once a poll marks it
	// down, rather than failing for every user.
	list, err := alice.Sandboxes(ctx)
	if err != nil {
		t.Fatalf("listing with a node that stopped answering: %v", err)
	}
	for _, sb := range list {
		if nodeOf(tg, sb.ID) != "n1" {
			t.Fatalf("listed %s", sb.ID)
		}
	}
	if len(list) < 6 {
		t.Fatalf("listed %d sandboxes, want n1's (at least 6)", len(list))
	}
}

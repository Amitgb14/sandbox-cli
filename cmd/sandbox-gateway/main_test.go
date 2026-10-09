package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/gateway"
)

func run(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := newRoot()
	var out, errOut bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&errOut)
	root.SetArgs(args)
	err := root.Execute()
	return out.String(), err
}

// A gateway reachable from other machines carries API keys on every
// request; without TLS they are anyone's on the path.
func TestOpenListenerRefusesPlainHTTPOffLoopback(t *testing.T) {
	for _, tc := range []struct {
		addr    string
		tls     bool
		refused string
	}{
		{addr: "127.0.0.1:0"},
		{addr: "localhost:0"},
		{addr: "0.0.0.0:0", refused: "--tls-cert"},
		{addr: "0.0.0.0:0", tls: true},
		{addr: "unix:///tmp/x.sock", refused: "TCP"},
		{addr: "nonsense", refused: "--listen"},
	} {
		ln, _, err := openListener(tc.addr, tc.tls)
		if ln != nil {
			ln.Close()
		}
		switch {
		case tc.refused == "" && err != nil:
			t.Errorf("%s tls=%v: %v", tc.addr, tc.tls, err)
		case tc.refused != "" && (err == nil || !strings.Contains(err.Error(), tc.refused)):
			t.Errorf("%s tls=%v: %v; want a refusal naming %q", tc.addr, tc.tls, err, tc.refused)
		}
	}
}

func TestKeysCommands(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	if _, err := run(t, "keys", "create", "--user", "alice"); err == nil {
		t.Fatal("keys create without --state succeeded")
	}
	if _, err := run(t, "--state", state, "keys", "create", "--user", "alice", "--scope", "everything"); err == nil {
		t.Fatal("an unknown scope was accepted")
	}
	out, err := run(t, "--state", state, "keys", "create", "--user", "alice", "--tenant", "acme", "--scope", "admin")
	if err != nil {
		t.Fatal(err)
	}
	secret := regexp.MustCompile(`secret: (sgk_\S+)`).FindStringSubmatch(out)
	id := regexp.MustCompile(`id: +(key_\S+)`).FindStringSubmatch(out)
	if secret == nil || id == nil {
		t.Fatalf("output %q", out)
	}
	data, _ := os.ReadFile(state)
	if strings.Contains(string(data), secret[1]) {
		t.Fatal("the secret is in the state file")
	}
	out, err = run(t, "--state", state, "keys", "list")
	if err != nil || !strings.Contains(out, id[1]) || strings.Contains(out, secret[1]) || !strings.Contains(out, "active") {
		t.Fatalf("list %q %v", out, err)
	}
	if _, err := run(t, "--state", state, "keys", "revoke", id[1]); err != nil {
		t.Fatal(err)
	}
	if out, _ := run(t, "--state", state, "keys", "list"); !strings.Contains(out, "revoked") {
		t.Fatalf("after revoke: %q", out)
	}
	if _, err := run(t, "--state", state, "keys", "revoke", "key_nope"); err == nil {
		t.Fatal("revoking an unknown key succeeded")
	}
}

// --invite-url prints the link hosted Studio signs a user in with, and is
// checked before a key is minted, so a mistake there leaves no key behind.
func TestKeysCreateInviteURL(t *testing.T) {
	state := filepath.Join(t.TempDir(), "state.json")
	for _, bad := range []string{"studio.example.com", "https://studio.example.com/app", "https://studio.example.com/?x=1", "ftp://studio.example.com"} {
		if _, err := run(t, "--state", state, "keys", "create", "--user", "alice", "--scope", "sandbox:read", "--invite-url", bad); err == nil {
			t.Errorf("--invite-url %q was accepted", bad)
		}
	}
	if _, err := run(t, "--state", state, "keys", "create", "--user", "ops", "--scope", "admin", "--invite-url", "https://studio.example.com"); err == nil {
		t.Error("an invite for an admin key was made")
	}
	if out, _ := run(t, "--state", state, "keys", "list"); strings.Contains(out, "key_") {
		t.Fatalf("a refused create minted a key: %q", out)
	}
	out, err := run(t, "--state", state, "keys", "create", "--user", "alice", "--tenant", "alice", "--scope", "sandbox:read", "--invite-url", "https://studio.example.com/")
	if err != nil {
		t.Fatal(err)
	}
	secret := regexp.MustCompile(`secret: (sgk_\S+)`).FindStringSubmatch(out)
	if secret == nil || !strings.Contains(out, "invite: https://studio.example.com/#key="+secret[1]+"\n") {
		t.Fatalf("output %q", out)
	}
}

func TestNodesCommands(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state.json")
	tok := filepath.Join(dir, "n1.token")
	if err := os.WriteFile(tok, []byte("a-node-token-0123456789\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "--state", state, "nodes", "add", "n1", "http://node1.example:7070", "--token-file", tok); err == nil {
		t.Fatal("plain http to another machine was accepted")
	}
	if _, err := run(t, "--state", state, "nodes", "add", "N_1", "https://node1.example:7443"); err == nil {
		t.Fatal("a node name that cannot appear in an id was accepted")
	}
	if err := os.Chmod(tok, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "--state", state, "nodes", "add", "n1", "https://node1.example:7443", "--token-file", tok); err == nil {
		t.Fatal("a token file others can read was accepted")
	}
	_ = os.Chmod(tok, 0o600)
	if _, err := run(t, "--state", state, "nodes", "add", "n1", "https://node1.example:7443", "--token-file", tok); err != nil {
		t.Fatal(err)
	}
	out, err := run(t, "--state", state, "nodes", "list")
	if err != nil || !strings.Contains(out, "n1") || !strings.Contains(out, tok) {
		t.Fatalf("list %q %v", out, err)
	}
	if strings.Contains(out, "a-node-token") {
		t.Fatal("the node list shows a token")
	}
	if _, err := run(t, "--state", state, "nodes", "remove", "n1"); err != nil {
		t.Fatal(err)
	}
	if _, err := run(t, "--state", state, "nodes", "remove", "n1"); err == nil {
		t.Fatal("removing twice succeeded")
	}
}

func TestLoadNodeConfig(t *testing.T) {
	dir := t.TempDir()
	good := filepath.Join(dir, "nodes.yaml")
	os.WriteFile(good, []byte("nodes:\n  - name: n1\n    endpoint: https://n1.example:7443\n    token_file: /etc/gw/n1.token\n    ca_file: /etc/gw/ca.pem\n"), 0o600)
	nodes, err := loadNodeConfig(good)
	if err != nil || len(nodes) != 1 || nodes[0].CAFile != "/etc/gw/ca.pem" || nodes[0].TokenFile != "/etc/gw/n1.token" {
		t.Fatalf("%+v %v", nodes, err)
	}
	// A misspelt key would quietly mean the system's CAs are trusted.
	bad := filepath.Join(dir, "bad.yaml")
	os.WriteFile(bad, []byte("nodes:\n  - name: n1\n    endpoint: https://n1.example:7443\n    cafile: /etc/gw/ca.pem\n"), 0o600)
	if _, err := loadNodeConfig(bad); err == nil {
		t.Fatal("an unknown key was accepted")
	}
	insecure := filepath.Join(dir, "insecure.yaml")
	os.WriteFile(insecure, []byte("nodes:\n  - name: n1\n    endpoint: http://n1.example:7070\n"), 0o600)
	if _, err := loadNodeConfig(insecure); err == nil {
		t.Fatal("plain http to another machine was accepted")
	}
}

// Asked to serve SSH and unable to, the gateway does not start: it never
// serves without a control it was asked for.
func TestServeFailsWhenSSHCannotStart(t *testing.T) {
	dir := t.TempDir()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := false
	err := serve(ctx, filepath.Join(dir, "state.json"), serveOptions{
		listen: "127.0.0.1:0", sshListen: "127.0.0.1:0", sshPublicHost: "gw.example", sshPublicPort: 2222,
		// A host key path that cannot be created: whatever the SSH server
		// is, it cannot start with it.
		sshHostKey: filepath.Join(dir, "missing-dir", "\x00key"),
	}, t.Logf, func(string) { served = true; cancel() })
	if served {
		t.Fatal("the API served although SSH could not start")
	}
	if !strings.Contains(err.Error(), "ssh") {
		t.Fatalf("err = %v", err)
	}
}

func TestSSHConfigDefaults(t *testing.T) {
	cfg, err := sshConfig("/var/lib/gw/state.json", serveOptions{sshListen: "gw.example:2222"})
	if err != nil || cfg.Host != "gw.example" || cfg.Port != 2222 || cfg.HostKeyFile != "/var/lib/gw/ssh_host_ed25519_key" {
		t.Fatalf("%+v %v", cfg, err)
	}
	if _, err := sshConfig("/s", serveOptions{sshListen: "0.0.0.0:2222"}); err == nil {
		t.Fatal("an unspecified address was advertised")
	}
	if _, err := sshConfig("/s", serveOptions{sshListen: "gw.example:0"}); err == nil {
		t.Fatal("port 0 was advertised")
	}
}

// serve end to end, with no nodes: the key made with the CLI before it
// started works, and while it serves the CLI cannot change its state.
func TestServe(t *testing.T) {
	dir := t.TempDir()
	state := filepath.Join(dir, "state.json")
	out, err := run(t, "--state", state, "keys", "create", "--user", "root", "--scope", "admin")
	if err != nil {
		t.Fatal(err)
	}
	secret := regexp.MustCompile(`secret: (sgk_\S+)`).FindStringSubmatch(out)[1]
	ctx, cancel := context.WithCancel(context.Background())
	where := make(chan string, 1)
	done := make(chan error, 1)
	go func() {
		done <- serve(ctx, state, serveOptions{listen: "127.0.0.1:0", pollInterval: time.Second}, t.Logf, func(w string) { where <- w })
	}()
	var base string
	select {
	case base = <-where:
	case err := <-done:
		t.Fatal(err)
	}
	c := api.NewClientWithHTTP(base, secret, http.DefaultClient)
	req, _ := http.NewRequest(http.MethodGet, base+"/v1/whoami", nil)
	req.Header.Set("Authorization", "Bearer "+secret)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	var who api.Whoami
	json.NewDecoder(resp.Body).Decode(&who)
	resp.Body.Close()
	if who.User != "root" {
		t.Fatalf("whoami %+v", who)
	}
	if _, err := c.Capabilities(context.Background()); !api.IsCode(err, api.CodeUnavailable) {
		t.Fatalf("capabilities with no nodes: %v", err)
	}
	if _, err := run(t, "--state", state, "keys", "revoke", "key_x"); err == nil || !strings.Contains(err.Error(), "admin API") {
		t.Fatalf("the CLI changed a serving gateway's state: %v", err)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

// The router carries whatever a public service's users send, cookies and
// credentials among it: off loopback it is served only over TLS, as the API
// is, and a gateway asked to route and unable to does not start.
func TestRouterFlags(t *testing.T) {
	for _, tc := range []struct {
		name    string
		o       serveOptions
		refused string
		want    gateway.RouterConfig
	}{
		{name: "off", o: serveOptions{}},
		{name: "domain without listen", o: serveOptions{routerDomain: "apps.example.com"}, refused: "--router-listen"},
		{name: "no domain", o: serveOptions{routerListen: "127.0.0.1:0"}, refused: "--router-domain"},
		{name: "wildcard domain", o: serveOptions{routerListen: "127.0.0.1:0", routerDomain: "*.apps.example.com"}, refused: "--router-domain"},
		{name: "cert without key", o: serveOptions{routerListen: "127.0.0.1:0", routerDomain: "apps.example.com", routerCert: "c"}, refused: "go together"},
		{name: "loopback", o: serveOptions{routerListen: "127.0.0.1:8080", routerDomain: "Apps.Example.COM."}, want: gateway.RouterConfig{Domain: "apps.example.com", Scheme: "http", Port: 8080}},
		{name: "behind a proxy", o: serveOptions{routerListen: "127.0.0.1:8080", routerDomain: "apps.example.com", routerScheme: "https", routerPort: 443}, want: gateway.RouterConfig{Domain: "apps.example.com", Scheme: "https", Port: 443}},
		{name: "bad scheme", o: serveOptions{routerListen: "127.0.0.1:8080", routerDomain: "apps.example.com", routerScheme: "ftp"}, refused: "http or https"},
	} {
		rc, err := routerConfig(tc.o)
		switch {
		case tc.refused != "" && (err == nil || !strings.Contains(err.Error(), tc.refused)):
			t.Errorf("%s: %v; want a refusal naming %q", tc.name, err, tc.refused)
		case tc.refused == "" && err != nil:
			t.Errorf("%s: %v", tc.name, err)
		case tc.want != (gateway.RouterConfig{}) && rc != tc.want:
			t.Errorf("%s: %+v; want %+v", tc.name, rc, tc.want)
		}
	}

	_, err := listenTLS("0.0.0.0:0", "", "", "router-")
	if err == nil || !strings.Contains(err.Error(), "--router-listen") || !strings.Contains(err.Error(), "--router-tls-cert") {
		t.Errorf("plain http off loopback: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	served := false
	err = serve(ctx, filepath.Join(t.TempDir(), "state.json"), serveOptions{
		listen: "127.0.0.1:0", routerListen: "0.0.0.0:0", routerDomain: "apps.example.com",
	}, t.Logf, func(string) { served = true; cancel() })
	if served || err == nil || !strings.Contains(err.Error(), "router") {
		t.Fatalf("served %v, err %v; the gateway must not start without the router it was asked for", served, err)
	}
}

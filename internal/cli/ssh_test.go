package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/server"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// A key made once with `ssh-keygen -t ed25519 -C test@vec`, and the
// fingerprint `ssh-keygen -lf` printed for it.
const (
	vecKey = "ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAILClwrW5idnux05LkKqDvmh8gKMDJde3zppOA/WIbBV8 test@vec"
	vecFP  = "SHA256:w/N51SPOiyhxi+UVSnx13vYkM72c9eNIa9RVwxHoVqM"
)

func TestFingerprintMatchesSSHKeygen(t *testing.T) {
	k, err := parsePublicKey(vecKey)
	if err != nil {
		t.Fatal(err)
	}
	if got := k.Fingerprint(); got != vecFP {
		t.Errorf("fingerprint %s, want %s", got, vecFP)
	}
	if k.Type != "ssh-ed25519" || k.Comment != "test@vec" || k.Line() != vecKey {
		t.Errorf("parsed %+v, line %q", k, k.Line())
	}

	// And against whatever ssh-keygen is installed, for each key type.
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("no ssh-keygen; the fixed vector above stands")
	}
	for _, typ := range []string{"ed25519", "ecdsa", "rsa"} {
		dir := t.TempDir()
		f := filepath.Join(dir, "k")
		if out, err := exec.Command("ssh-keygen", "-q", "-t", typ, "-N", "", "-C", "c", "-f", f).CombinedOutput(); err != nil {
			t.Fatalf("ssh-keygen -t %s: %v %s", typ, err, out)
		}
		out, err := exec.Command("ssh-keygen", "-l", "-E", "sha256", "-f", f+".pub").Output()
		if err != nil {
			t.Fatal(err)
		}
		want := strings.Fields(string(out))[1]
		k, err := readPublicKey(f + ".pub")
		if err != nil {
			t.Fatal(err)
		}
		if got := k.Fingerprint(); got != want {
			t.Errorf("%s: fingerprint %s, ssh-keygen says %s", typ, got, want)
		}
	}
}

func TestParsePublicKeyRefuses(t *testing.T) {
	blob := strings.Fields(vecKey)[1]
	for name, line := range map[string]string{
		"empty":               "",
		"type only":           "ssh-ed25519",
		"options":             `command="sh" ` + vecKey,
		"from option":         `from="*" ` + vecKey,
		"private key":         "-----BEGIN OPENSSH PRIVATE KEY-----",
		"bad base64":          "ssh-ed25519 !!!",
		"type and blob apart": "ssh-rsa " + blob,
		"unknown type":        "ssh-dss " + blob,
		"truncated blob":      "ssh-ed25519 AAAA",
	} {
		if _, err := parsePublicKey(line); err == nil {
			t.Errorf("%s: accepted", name)
		} else if strings.Contains(err.Error(), blob) {
			t.Errorf("%s: the error quotes the key: %v", name, err)
		}
	}
}

func TestKnownHostsLine(t *testing.T) {
	k, _ := parsePublicKey(vecKey)
	b64 := strings.Fields(vecKey)[1]
	if got, want := knownHostsLine("gw.example", 2222, k), "[gw.example]:2222 ssh-ed25519 "+b64; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	// Port 22 is looked up by the bare name; the comment never goes in.
	if got, want := knownHostsLine("gw.example", 22, k), "gw.example ssh-ed25519 "+b64; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// Pinning replaces this gateway's entries and leaves everyone else's.
func TestPinHostKeys(t *testing.T) {
	k, _ := parsePublicKey(vecKey)
	path := filepath.Join(t.TempDir(), "sub", "known_hosts")
	other := "[other.example]:2222 ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIOther"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(other+"\n[gw.example]:2222 ssh-rsa AAAAstale\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := pinHostKeys(path, "gw.example", 2222, []pubKey{k}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(path)
	want := other + "\n" + knownHostsLine("gw.example", 2222, k) + "\n"
	if string(got) != want {
		t.Errorf("known_hosts:\n%s\nwant:\n%s", got, want)
	}
	st, _ := os.Stat(path)
	if st.Mode().Perm() != 0o600 {
		t.Errorf("mode %v", st.Mode().Perm())
	}
}

func TestGatewaySSHTarget(t *testing.T) {
	good := api.SSHInfo{Host: "gw.example", Port: 2222, HostKeys: []string{"ssh-ed25519 " + strings.Fields(vecKey)[1]}}
	tg, err := gatewaySSHTarget(good, "api.example")
	if err != nil || tg.Host != "gw.example" || tg.Port != 2222 || len(tg.Keys) != 1 {
		t.Fatalf("target %+v, %v", tg, err)
	}
	noHost := good
	noHost.Host = ""
	if tg, _ := gatewaySSHTarget(noHost, "api.example"); tg.Host != "api.example" {
		t.Errorf("an unnamed host is the endpoint's: got %q", tg.Host)
	}
	for name, mut := range map[string]func(*api.SSHInfo){
		"a host that is an ssh option": func(i *api.SSHInfo) { i.Host = "-oProxyCommand=sh" },
		"a host with a space":          func(i *api.SSHInfo) { i.Host = "gw example" },
		"a wildcard host":              func(i *api.SSHInfo) { i.Host = "*" },
		"a host list":                  func(i *api.SSHInfo) { i.Host = "a,b" },
		"no port":                      func(i *api.SSHInfo) { i.Port = 0 },
		"no host key":                  func(i *api.SSHInfo) { i.HostKeys = nil },
		"a marker for a host key":      func(i *api.SSHInfo) { i.HostKeys = []string{"@cert-authority * " + vecKey} },
		"a second line in a host key":  func(i *api.SSHInfo) { i.HostKeys = []string{"ssh-ed25519 x\n* ssh-ed25519 y"} },
	} {
		info := good
		info.HostKeys = append([]string(nil), good.HostKeys...)
		mut(&info)
		if _, err := gatewaySSHTarget(info, "api.example"); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestSSHArgv(t *testing.T) {
	tg := sshTarget{Host: "gw.example", Port: 2222}
	got := sshArgv(tg, "demo", "/c/known_hosts", "/h/.ssh/id_ed25519", []string{"uname", "-a"})
	want := []string{
		"-p", "2222",
		"-o", "UserKnownHostsFile=/c/known_hosts",
		"-o", "GlobalKnownHostsFile=" + os.DevNull,
		"-o", "StrictHostKeyChecking=yes",
		"-i", "/h/.ssh/id_ed25519", "-o", "IdentitiesOnly=yes",
		"--", "demo@gw.example", "uname", "-a",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("argv\n %q\nwant\n %q", got, want)
	}
	got = sshArgv(tg, "demo", "/c/kh", "", nil)
	if strings.Contains(strings.Join(got, " "), "-i") || got[len(got)-1] != "demo@gw.example" || got[len(got)-2] != "--" {
		t.Errorf("no identity, no command: %q", got)
	}
}

func TestValidSSHUser(t *testing.T) {
	for ref, want := range map[string]bool{
		"demo": true, "sbx_0123456789abcdef": true, "sbx_n1_0123456789abcdef": true,
		"-oProxyCommand=x": false, "a@b": false, "a b": false, "": false, "Demo": false,
	} {
		if validSSHUser(ref) != want {
			t.Errorf("validSSHUser(%q) = %v", ref, !want)
		}
	}
}

func TestResolveIdentity(t *testing.T) {
	dir := t.TempDir()
	if _, err := resolveIdentity("", dir); err == nil {
		t.Error("no keys at all: accepted")
	}
	write := func(name string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(vecKey+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		return p
	}
	rsa := write("id_rsa.pub")
	if id, _ := resolveIdentity("", dir); id.Pub != rsa || id.Offer != rsa {
		t.Errorf("only an rsa .pub: %+v (with no private half, the .pub is offered for the agent)", id)
	}
	ed := write("id_ed25519.pub")
	edPriv := write("id_ed25519")
	if id, _ := resolveIdentity("", dir); id.Pub != ed || id.Offer != edPriv {
		t.Errorf("ed25519 comes first: %+v", id)
	}
	// --identity takes either half.
	if id, _ := resolveIdentity(edPriv, dir); id.Pub != ed || id.Offer != edPriv {
		t.Errorf("--identity private: %+v", id)
	}
	if id, _ := resolveIdentity(ed, dir); id.Pub != ed || id.Offer != edPriv {
		t.Errorf("--identity public: %+v", id)
	}
}

// gatewayStandIn answers the gateway endpoints `ssh` uses, with keys already
// registered, and records every POST to /v1/ssh-keys.
type gatewayStandIn struct {
	mu    sync.Mutex
	keys  []api.SSHKeyInfo
	added []api.SSHKeyRequest
}

func (g *gatewayStandIn) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	g.mu.Lock()
	defer g.mu.Unlock()
	w.Header().Set("Content-Type", "application/json")
	switch r.Method + " " + r.URL.Path {
	case "GET /v1/whoami":
		_ = json.NewEncoder(w).Encode(api.Whoami{User: "ana", Tenant: "t1", KeyID: "key_1", Scopes: []string{"sandbox:ssh"}})
	case "GET /v1/ssh":
		_ = json.NewEncoder(w).Encode(api.SSHInfo{Host: "gw.example", Port: 2222,
			HostKeys: []string{"ssh-ed25519 " + strings.Fields(vecKey)[1]}, Fingerprint: vecFP})
	case "GET /v1/ssh-keys":
		_ = json.NewEncoder(w).Encode(api.SSHKeyList{Keys: g.keys})
	case "GET /v1/sandboxes/demo":
		// Only in organisation acme: what ssh --org looks the name up in.
		if r.Header.Get(api.OrgHeader) != "acme" {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no such sandbox"}}`))
			return
		}
		_ = json.NewEncoder(w).Encode(api.Sandbox{ID: "sbx_n1_00000000000000d0", Name: "demo"})
	case "POST /v1/ssh-keys":
		var req api.SSHKeyRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		g.added = append(g.added, req)
		k, _ := parsePublicKey(req.Key)
		info := api.SSHKeyInfo{ID: "sk_new", Fingerprint: k.Fingerprint(), Key: req.Key, Sandbox: req.Sandbox}
		g.keys = append(g.keys, info)
		_ = json.NewEncoder(w).Encode(info)
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no such endpoint"}}`))
	}
}

func TestEnsureSSHKeyRegistersOnlyWhenAbsent(t *testing.T) {
	k, _ := parsePublicKey(vecKey)
	for _, tc := range []struct {
		name string
		have []api.SSHKeyInfo
		adds bool
	}{
		{"none registered", nil, true},
		{"another key registered", []api.SSHKeyInfo{{ID: "sk_1", Fingerprint: "SHA256:other"}}, true},
		{"registered for every sandbox", []api.SSHKeyInfo{{ID: "sk_1", Fingerprint: vecFP}}, false},
		{"registered for this sandbox", []api.SSHKeyInfo{{ID: "sk_1", Fingerprint: vecFP, Sandbox: "demo"}}, false},
		{"registered for another sandbox only", []api.SSHKeyInfo{{ID: "sk_1", Fingerprint: vecFP, Sandbox: "other"}}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := &gatewayStandIn{keys: tc.have}
			srv := httptest.NewServer(g)
			defer srv.Close()
			c, _ := api.NewClient(srv.URL, "k")
			added, err := ensureSSHKey(context.Background(), c, k, "demo")
			if err != nil {
				t.Fatal(err)
			}
			if added != tc.adds || len(g.added) != map[bool]int{true: 1, false: 0}[tc.adds] {
				t.Fatalf("added = %v, posts %v", added, g.added)
			}
			if tc.adds && g.added[0].Key != vecKey {
				t.Errorf("sent %q, want the key line", g.added[0].Key)
			}
			// Asking again finds it.
			if again, _ := ensureSSHKey(context.Background(), c, k, "demo"); again {
				t.Error("registered twice")
			}
		})
	}
}

// Only a 404 for /v1/whoami means a plain sandboxd. A refused key must not
// quietly fall back to the API, where it would fail differently and hide why.
func TestChooseSSHRoute(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no such endpoint"}}`))
	}))
	defer plain.Close()
	refused := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":{"code":"unauthorized","message":"bad key"}}`))
	}))
	defer refused.Close()
	gw := httptest.NewServer(&gatewayStandIn{})
	defer gw.Close()

	for _, tc := range []struct {
		url     string
		want    sshRoute
		wantErr bool
	}{
		{gw.URL, routeSSH, false},
		{plain.URL, routeAPI, false},
		{refused.URL, routeSSH, true},
	} {
		c, _ := api.NewClient(tc.url, "k")
		got, err := chooseSSHRoute(context.Background(), c)
		if (err != nil) != tc.wantErr || (err == nil && got != tc.want) {
			t.Errorf("%s: route %v, %v", tc.url, got, err)
		}
	}
}

// useEndpoint points the CLI's current context at url.
func useEndpoint(t *testing.T, url string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SANDBOX_CONTEXT", "")
	cf := contextFile{Current: "test", Contexts: map[string]endpointContext{"test": {Endpoint: url}}}
	if err := cf.save(); err != nil {
		t.Fatal(err)
	}
}

func runCLI(t *testing.T, args ...string) (string, error) {
	t.Helper()
	root := NewRootCmd()
	var out bytes.Buffer
	root.SetOut(&out)
	root.SetErr(&out)
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return out.String(), err
}

func TestGatewayCommandsOnAPlainSandboxd(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"error":{"code":"not_found","message":"no such endpoint"}}`))
	}))
	defer plain.Close()
	useEndpoint(t, plain.URL)

	out, err := runCLI(t, "whoami")
	if err != nil || !strings.Contains(out, "single-tenant sandboxd") {
		t.Errorf("whoami: %q, %v", out, err)
	}
	for _, args := range [][]string{{"ssh-key", "list"}, {"ssh-key", "rm", "sk_1"}, {"ssh-access", "demo"}} {
		_, err := runCLI(t, args...)
		if err == nil || !strings.Contains(err.Error(), "plain sandboxd") {
			t.Errorf("%v: %v, want a plain-sandboxd refusal", args, err)
		}
	}
}

func TestWhoamiOnAGateway(t *testing.T) {
	gw := httptest.NewServer(&gatewayStandIn{})
	defer gw.Close()
	useEndpoint(t, gw.URL)
	out, err := runCLI(t, "whoami")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"ana", "t1", "key_1", "sandbox:ssh"} {
		if !strings.Contains(out, want) {
			t.Errorf("whoami output lacks %q:\n%s", want, out)
		}
	}
}

func TestSSHKeyAddSendsThePublicHalf(t *testing.T) {
	g := &gatewayStandIn{}
	gw := httptest.NewServer(g)
	defer gw.Close()
	useEndpoint(t, gw.URL)
	dir := t.TempDir()
	priv := filepath.Join(dir, "work")
	if err := os.WriteFile(priv, []byte("-----BEGIN OPENSSH PRIVATE KEY-----\nsecret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(priv+".pub", []byte(vecKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	out, err := runCLI(t, "ssh-key", "add", priv, "--sandbox", "demo")
	if err != nil {
		t.Fatal(err)
	}
	if len(g.added) != 1 || g.added[0].Key != vecKey || g.added[0].Sandbox != "demo" {
		t.Errorf("sent %+v", g.added)
	}
	if !strings.Contains(out, vecFP) {
		t.Errorf("output %q lacks the fingerprint", out)
	}
}

// The whole path on a gateway, with a stand-in ssh that records its argv and
// exits 7: the key is registered, the host key pinned, ssh is run with the
// pinned file and the key, and its status is ours.
func TestSSHCommandRunsTheSystemSSH(t *testing.T) {
	if _, err := exec.LookPath("sh"); err != nil {
		t.Skip("no sh")
	}
	g := &gatewayStandIn{}
	gw := httptest.NewServer(g)
	defer gw.Close()
	useEndpoint(t, gw.URL)
	home := t.TempDir()
	t.Setenv("HOME", home)
	if err := os.MkdirAll(filepath.Join(home, ".ssh"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".ssh", "id_ed25519.pub"), []byte(vecKey+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	argsFile := filepath.Join(bin, "args")
	script := "#!/bin/sh\nfor a in \"$@\"; do printf '%s\\n' \"$a\"; done > " + argsFile + "\nexit 7\n"
	if err := os.WriteFile(filepath.Join(bin, "ssh"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	_, err := runCLI(t, "ssh", "demo", "uname", "-a")
	var ee exitError
	if !errors.As(err, &ee) || ee.code != 7 {
		t.Fatalf("err %v, want ssh's exit status 7", err)
	}
	if len(g.added) != 1 || g.added[0].Key != vecKey {
		t.Errorf("registered %+v", g.added)
	}
	kh := knownHostsPath()
	pinned, _ := os.ReadFile(kh)
	if !strings.HasPrefix(string(pinned), "[gw.example]:2222 ssh-ed25519 ") {
		t.Errorf("known_hosts: %q", pinned)
	}
	got, _ := os.ReadFile(argsFile)
	want := strings.Join(sshArgv(sshTarget{Host: "gw.example", Port: 2222}, "demo", kh,
		filepath.Join(home, ".ssh", "id_ed25519.pub"), []string{"uname", "-a"}), "\n") + "\n"
	if string(got) != want {
		t.Errorf("ssh ran with\n%s\nwant\n%s", got, want)
	}

	// In an organisation, the name is resolved with it selected and the
	// login is by id: the SSH server looks names up in the key's own tenant.
	if _, err := runCLI(t, "--org", "acme", "ssh", "demo", "true"); !errors.As(err, &ee) {
		t.Fatalf("ssh --org: %v", err)
	}
	got, _ = os.ReadFile(argsFile)
	if !strings.Contains(string(got), "sbx_n1_00000000000000d0@gw.example\n") {
		t.Errorf("ssh --org ran with\n%s", got)
	}

	// No ssh on PATH: a clear error, not an exec failure.
	t.Setenv("PATH", t.TempDir())
	if _, err := runCLI(t, "ssh", "demo"); err == nil || !strings.Contains(err.Error(), "no ssh client") {
		t.Errorf("without ssh: %v", err)
	}
}

// On a plain sandboxd, ssh runs the command through the API instead, and
// never looks for an ssh client.
func TestSSHFallsBackToTheAPIOnAPlainSandboxd(t *testing.T) {
	be := fake.New(api.CapEgressAllowlist)
	srv := httptest.NewServer((&server.Server{Backend: be, Policy: spec.DefaultPolicyFor(be.Capabilities())}).Handler())
	defer srv.Close()
	useEndpoint(t, srv.URL)
	t.Setenv("PATH", t.TempDir()) // no ssh: the fallback must not need one
	c, _ := api.NewClient(srv.URL, "")
	sb, err := c.CreateSandbox(context.Background(), api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = runCLI(t, "ssh", sb.ID, "--", "false")
	var ee exitError
	if !errors.As(err, &ee) || ee.code != 1 {
		t.Fatalf("err %v, want the command's exit status 1", err)
	}
	if _, err := runCLI(t, "ssh", sb.ID, "--", "true"); err != nil {
		t.Errorf("true: %v", err)
	}
}

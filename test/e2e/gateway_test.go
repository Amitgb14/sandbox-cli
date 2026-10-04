//go:build e2e

// Package e2e drives the real binaries the way a user and an operator do:
// two sandboxd nodes on the fake backend, sandbox-gateway in front of them,
// and sandbox-cli (and the system ssh, when there is one) against the
// gateway. The unit tests prove each piece against stand-ins; this proves
// the flags, the state file, the sockets and the wire formats agree when the
// pieces are separate processes.
//
//	make e2e      (go test -tags e2e -count=1 -v ./test/e2e)
//
// The fake backend runs builtins only (true, echo, sleep, cat, …), so an SSH
// exec, which goes through /bin/sh, is refused by the node after the login
// succeeds. That is what is asserted here; a real exec through the gateway is
// docs/testing/end-to-end.md's row on a real microVM.
package e2e

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// env is one running fleet: the binaries, the processes and the CLI's home.
type env struct {
	t        *testing.T
	bin      string // built binaries
	dir      string // sockets, state, the CLI's home
	state    string // the gateway's state file
	apiURL   string
	sshPort  int
	gwLog    *syncBuffer
	nodes    map[string]chan struct{} // closed when the node exits
	nodeCmds map[string]*exec.Cmd
	cliEnv   []string
	userKeys map[string]string // user -> key file
	keyIDs   map[string]string // user -> key id
}

func TestGatewayEndToEnd(t *testing.T) {
	e := start(t)

	// --- who the keys are ------------------------------------------------
	out := e.cli("alice", "whoami")
	for _, want := range []string{"user    alice", "sandbox:create", "sandbox:ssh"} {
		if !strings.Contains(out, want) {
			t.Fatalf("whoami: missing %q in\n%s", want, out)
		}
	}
	if out := e.cli("alice", "doctor"); !strings.Contains(out, "backend:   fake") {
		t.Fatalf("doctor:\n%s", out)
	}

	// --- create: ids name their node, and the fleet spreads -----------------
	idRE := regexp.MustCompile(`sbx_(n[12])_[0-9a-f]{16}`)
	e.cliOK("alice", "run", "--keep", "--name", "a1", "--", "echo", "hi")
	var alices []string
	nodesUsed := map[string]bool{}
	for range 4 {
		out := e.cli("alice", "run", "--detach", "--", "sleep", "600")
		m := idRE.FindStringSubmatch(out)
		if m == nil {
			t.Fatalf("run --detach printed no sandbox id:\n%s", out)
		}
		alices = append(alices, m[0])
		nodesUsed[m[1]] = true
	}
	if !nodesUsed["n1"] || !nodesUsed["n2"] {
		t.Fatalf("four sandboxes all landed on one node: %v", alices)
	}
	listed := e.cli("alice", "list")
	for _, id := range alices {
		if !strings.Contains(listed, id) {
			t.Fatalf("alice's list lacks %s:\n%s", id, listed)
		}
	}
	a1 := ""
	for _, line := range strings.Split(listed, "\n") {
		if f := strings.Fields(line); len(f) > 1 && f[1] == "a1" {
			a1 = f[0]
		}
	}
	if a1 == "" {
		t.Fatalf("alice's list lacks a1:\n%s", listed)
	}

	// --- another user sees and reaches none of it ---------------------------
	bobs := e.cli("bob", "run", "--detach", "--", "sleep", "600")
	bobID := idRE.FindString(bobs)
	if bobID == "" {
		t.Fatalf("bob's run printed no id:\n%s", bobs)
	}
	if out := e.cli("bob", "list"); strings.Count(out, "sbx_") != 1 || !strings.Contains(out, bobID) {
		t.Fatalf("bob's list shows more than his own:\n%s", out)
	}
	if out := e.cli("alice", "list"); strings.Contains(out, bobID) {
		t.Fatalf("alice's list shows bob's sandbox:\n%s", out)
	}
	for _, args := range [][]string{
		{"kill", a1}, {"kill", "a1"}, {"logs", a1}, {"ssh-access", a1}, {"suspend", a1},
	} {
		out, err := e.run("bob", args...)
		if err == nil || !strings.Contains(out, "not_found (404)") {
			t.Fatalf("bob %v: want not_found, got %v:\n%s", args, err, out)
		}
	}

	// --- SSH: keys, the published address, access tokens --------------------
	info := e.sshInfo("alice")
	if info.Host != "127.0.0.1" || info.Port != e.sshPort || len(info.HostKeys) != 1 {
		t.Fatalf("GET /v1/ssh: %+v (want 127.0.0.1:%d and one host key)", info, e.sshPort)
	}
	access := e.cli("alice", "ssh-access", "a1", "--ttl", "5m")
	accessRE := regexp.MustCompile(`(?m)^ssh -p (\d+) (sgt_[a-z0-9]+)@(\S+)$`)
	m := accessRE.FindStringSubmatch(access)
	if m == nil || m[1] != strconv.Itoa(info.Port) || m[3] != info.Host {
		t.Fatalf("ssh-access printed %q; want ssh -p %d sgt_…@%s, the same address GET /v1/ssh gives", access, info.Port, info.Host)
	}
	token := m[2]

	sshKeygen, errKeygen := exec.LookPath("ssh-keygen")
	sshBin, errSSH := exec.LookPath("ssh")
	if errKeygen != nil {
		t.Log("no ssh-keygen: skipping SSH keys and logins")
	} else {
		keyFile := filepath.Join(e.dir, "home", ".ssh", "id_ed25519")
		if err := os.MkdirAll(filepath.Dir(keyFile), 0o700); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(sshKeygen, "-q", "-t", "ed25519", "-N", "", "-C", "e2e", "-f", keyFile).CombinedOutput(); err != nil {
			t.Fatalf("ssh-keygen: %v\n%s", err, out)
		}
		fpOut, err := exec.Command(sshKeygen, "-l", "-E", "sha256", "-f", keyFile+".pub").Output()
		if err != nil {
			t.Fatal(err)
		}
		fp := strings.Fields(string(fpOut))[1]
		if out := e.cli("alice", "ssh-key", "add"); !strings.Contains(out, fp) {
			t.Fatalf("ssh-key add printed %q, want fingerprint %s", out, fp)
		}
		if out := e.cli("alice", "ssh-key", "list"); !strings.Contains(out, fp) || !strings.Contains(out, "(all)") {
			t.Fatalf("ssh-key list:\n%s", out)
		}
		if out := e.cli("bob", "ssh-key", "list"); strings.Contains(out, fp) {
			t.Fatalf("bob sees alice's key:\n%s", out)
		}

		if errSSH != nil {
			t.Log("no ssh client: skipping logins")
		} else {
			e.sshLogins(sshBin, keyFile, a1, bobID, token)
		}
	}

	// --- a node that stops answering: unavailable, not internal --------------
	var onN2 string
	for _, id := range alices {
		if strings.HasPrefix(id, "sbx_n2_") {
			onN2 = id
		}
	}
	e.stopNode("n2")
	// The gateway has not polled since: creates placed on n2 find it gone
	// and go to n1 instead of failing.
	for range 4 {
		out := e.cli("alice", "run", "--detach", "--", "sleep", "600")
		m := idRE.FindStringSubmatch(out)
		if m == nil || m[1] != "n1" {
			t.Fatalf("a create with n2 stopped:\n%s", out)
		}
		alices = append(alices, m[0])
	}
	deadline := time.Now().Add(15 * time.Second)
	for {
		out, err := e.run("alice", "logs", onN2)
		if err != nil && strings.Contains(out, "unavailable (503)") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("logs on a down node's sandbox: want unavailable (503), got %v:\n%s", err, out)
		}
		time.Sleep(200 * time.Millisecond)
	}
	// What the rest of the fleet holds is still reachable.
	if out := e.cli("alice", "list"); !strings.Contains(out, a1) {
		t.Fatalf("with n2 down, a1 (on %s) is gone from the list:\n%s", a1[4:6], out)
	}

	// --- terminate --------------------------------------------------------
	var onN1 []string
	for _, id := range append(alices, a1) {
		if !strings.HasPrefix(id, "sbx_n2_") {
			onN1 = append(onN1, id)
		}
	}
	e.cliOK("alice", append([]string{"kill"}, onN1...)...)
	if out := e.cli("alice", "list"); strings.Contains(out, a1) && !strings.Contains(out, "terminated") {
		t.Fatalf("after kill:\n%s", out)
	}
}

// sshLogins runs the system ssh through the gateway's SSH port.
func (e *env) sshLogins(sshBin, keyFile, a1, bobID, token string) {
	t := e.t
	knownHosts := filepath.Join(e.dir, "cfg", "sandbox", "known_hosts")

	// sandbox-cli ssh: pins the host key, then hands over to ssh. The login
	// succeeds; the fake node then refuses /bin/sh, so ssh reports the exec
	// failed and exits 255.
	out, err := e.run("alice", "ssh", "a1", "--", "echo", "hi")
	if code := exitCode(err); code != 255 || !strings.Contains(out, "exec request failed") {
		t.Fatalf("sandbox-cli ssh: exit %d, want 255 with the exec refused:\n%s", code, out)
	}
	kh, err := os.ReadFile(knownHosts)
	if err != nil {
		t.Fatal(err)
	}
	if want := fmt.Sprintf("[127.0.0.1]:%d ssh-ed25519 ", e.sshPort); !strings.HasPrefix(string(kh), want) {
		t.Fatalf("known_hosts holds %q, want a line starting %q", kh, want)
	}
	e.waitLog("logged in to " + a1)
	e.waitLog("no such command")

	base := []string{"-F", os.DevNull, "-p", strconv.Itoa(e.sshPort),
		"-o", "UserKnownHostsFile=" + knownHosts, "-o", "GlobalKnownHostsFile=" + os.DevNull,
		"-o", "StrictHostKeyChecking=yes", "-o", "BatchMode=yes", "-o", "ConnectTimeout=10"}
	ssh := func(extra ...string) (string, int) {
		cmd := exec.Command(sshBin, append(append([]string{}, base...), extra...)...)
		out, err := cmd.CombinedOutput()
		return string(out), exitCode(err)
	}
	withKey := []string{"-i", keyFile, "-o", "IdentitiesOnly=yes"}

	// Plain ssh with the pinned host key and the registered key.
	if out, code := ssh(append(withKey, "--", "a1@127.0.0.1", "true")...); code != 255 || !strings.Contains(out, "exec request failed") {
		t.Fatalf("plain ssh: exit %d:\n%s", code, out)
	}
	// By id as well as by name.
	if out, code := ssh(append(withKey, "--", a1+"@127.0.0.1", "true")...); code != 255 || !strings.Contains(out, "exec request failed") {
		t.Fatalf("plain ssh by id: exit %d:\n%s", code, out)
	}
	// Alice's key does not reach bob's sandbox: refused at authentication.
	if out, code := ssh(append(withKey, "--", bobID+"@127.0.0.1", "true")...); code != 255 || !strings.Contains(out, "Permission denied") {
		t.Fatalf("alice's key on bob's sandbox: exit %d:\n%s", code, out)
	}
	// The ssh-access token is the whole credential: no key offered.
	noKey := []string{"-o", "PubkeyAuthentication=no", "-o", "PasswordAuthentication=no", "-o", "KbdInteractiveAuthentication=no"}
	if out, code := ssh(append(noKey, "--", token+"@127.0.0.1", "true")...); code != 255 || !strings.Contains(out, "exec request failed") {
		t.Fatalf("token login: exit %d:\n%s", code, out)
	}
	e.waitLog("via a token logged in to " + a1)
	// A token that was never issued is refused.
	if out, code := ssh(append(noKey, "--", "sgt_"+strings.Repeat("a", 52)+"@127.0.0.1", "true")...); code != 255 || !strings.Contains(out, "Permission denied") {
		t.Fatalf("a made-up token: exit %d:\n%s", code, out)
	}

	// Revoking a key ends an open session, not only the next login: carol
	// holds a connection open (-N: no command, which the fake node would
	// refuse), follows a process's output (logs, which follows until the
	// process exits), and an admin revokes her key.
	e.cliOK("carol", "run", "--keep", "--name", "c1", "--", "echo", "hi")
	e.cliOK("carol", "run", "-d", "--name", "c2", "--", "sleep", "600")
	logs := exec.Command(filepath.Join(e.bin, "sandbox-cli"), withContext([]string{"logs", "c2"}, "carol")...)
	logs.Env = e.cliEnv
	var logsOut syncBuffer
	logs.Stdout, logs.Stderr = &logsOut, &logsOut
	if err := logs.Start(); err != nil {
		t.Fatal(err)
	}
	logsEnded := make(chan struct{})
	go func() { _ = logs.Wait(); close(logsEnded) }()
	t.Cleanup(func() { _ = logs.Process.Kill() })
	m := regexp.MustCompile(`(?m)^ssh -p \d+ (sgt_[a-z0-9]+)@`).FindStringSubmatch(e.cli("carol", "ssh-access", "c1"))
	if m == nil {
		t.Fatal("carol's ssh-access printed no token")
	}
	held := exec.Command(sshBin, append(append(append([]string{}, base...), noKey...), "-N", "--", m[1]+"@127.0.0.1")...)
	var heldOut syncBuffer
	held.Stdout, held.Stderr = &heldOut, &heldOut
	if err := held.Start(); err != nil {
		t.Fatal(err)
	}
	ended := make(chan struct{})
	go func() { _ = held.Wait(); close(ended) }()
	t.Cleanup(func() { _ = held.Process.Kill() })
	e.waitLog(`user "carol" via a token logged in`)
	select {
	case <-ended:
		t.Fatalf("carol's ssh -N ended before the revocation:\n%s", heldOut.String())
	case <-logsEnded:
		t.Fatalf("carol's logs ended before the revocation:\n%s", logsOut.String())
	default:
	}
	e.adminDo(http.MethodDelete, "/v1/admin/keys/"+e.keyIDs["carol"], http.StatusNoContent)
	select {
	case <-ended:
	case <-time.After(10 * time.Second):
		t.Fatal("carol's SSH session stayed open after her key was revoked")
	}
	e.waitLog(`user "carol" via a token: closed: the user holds no active API key with sandbox:ssh`)
	select {
	case <-logsEnded:
	case <-time.After(10 * time.Second):
		t.Fatal("carol's logs stayed open after her key was revoked")
	}
	// Ended by the revocation while it was open, not refused at its start:
	// the gateway logs the followed request it ended.
	e.waitLog(`GET /v1/sandboxes/{ref}/processes/{pid}/output`)
	e.waitLog(`by key ` + e.keyIDs["carol"] + `: ended: its API key was revoked`)
}

// adminDo sends one admin API request with the admin key and checks its status.
func (e *env) adminDo(method, path string, want int) {
	key, err := os.ReadFile(e.userKeys["admin"])
	if err != nil {
		e.t.Fatal(err)
	}
	req, _ := http.NewRequest(method, e.apiURL+path, nil)
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(key)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != want {
		body, _ := io.ReadAll(resp.Body)
		e.t.Fatalf("%s %s: %s %s", method, path, resp.Status, body)
	}
}

// --- the fleet ------------------------------------------------------------------

func start(t *testing.T) *env {
	t.Helper()
	// Short: a unix socket path is limited to ~100 bytes.
	dir, err := os.MkdirTemp("", "sbxe2e")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	e := &env{t: t, bin: filepath.Join(dir, "bin"), dir: dir, state: filepath.Join(dir, "gw", "state.json"),
		gwLog: &syncBuffer{}, nodes: map[string]chan struct{}{}, nodeCmds: map[string]*exec.Cmd{}, userKeys: map[string]string{},
		keyIDs: map[string]string{}}
	e.build()

	for _, n := range []string{"n1", "n2"} {
		sock := filepath.Join(dir, n+".sock")
		e.nodeCmds[n], e.nodes[n] = e.daemon(nil, "sandboxd", "--backend", "fake", "--node-id", n,
			"--listen", "unix://"+sock, "--state-dir", filepath.Join(dir, n))
		waitFor(t, "sandboxd "+n, func() bool { _, err := os.Stat(sock); return err == nil })
		e.gateway("nodes", "add", n, "unix://"+sock)
	}
	user := []string{"--scope", "sandbox:read", "--scope", "sandbox:create", "--scope", "sandbox:delete", "--scope", "sandbox:ssh"}
	// carol is revoked mid-test, so nobody else's steps depend on her.
	for _, u := range []string{"alice", "bob", "carol"} {
		e.newKey(u, append([]string{"--user", u}, user...)...)
	}
	e.newKey("admin", "--user", "root", "--scope", "admin")

	apiPort, sshPort := freePort(t), freePort(t)
	e.sshPort = sshPort
	e.apiURL = fmt.Sprintf("http://127.0.0.1:%d", apiPort)
	e.daemon(e.gwLog, "sandbox-gateway", "--state", e.state, "serve",
		"--listen", fmt.Sprintf("127.0.0.1:%d", apiPort),
		"--ssh-listen", fmt.Sprintf("127.0.0.1:%d", sshPort),
		// Long enough that a node stopped mid-test is still believed
		// healthy: the window every real fleet has between polls.
		"--poll-interval", "30s")
	e.waitLog("2 nodes, 2 answering")

	e.cliEnv = append(os.Environ(),
		"HOME="+filepath.Join(dir, "home"),
		"XDG_CONFIG_HOME="+filepath.Join(dir, "cfg"),
		"SSH_AUTH_SOCK=", // only the key we generate
	)
	for _, u := range []string{"alice", "bob", "carol", "admin"} {
		e.cliOK("", "context", "add", u, e.apiURL, "--token-file", e.userKeys[u])
	}
	return e
}

func (e *env) build() {
	root, err := filepath.Abs("../..")
	if err != nil {
		e.t.Fatal(err)
	}
	for _, c := range []string{"sandboxd", "sandbox-gateway", "sandbox-cli"} {
		cmd := exec.Command("go", "build", "-o", filepath.Join(e.bin, c), "./cmd/"+c)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			e.t.Fatalf("go build %s: %v\n%s", c, err, out)
		}
	}
}

// daemon starts a long-running binary, stopped at cleanup. Its stderr goes to
// log when given, else to the test log on failure.
func (e *env) daemon(log *syncBuffer, name string, args ...string) (*exec.Cmd, chan struct{}) {
	cmd := exec.Command(filepath.Join(e.bin, name), args...)
	if log == nil {
		log = &syncBuffer{}
	}
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Start(); err != nil {
		e.t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { cmd.Wait(); close(done) }()
	e.t.Cleanup(func() {
		cmd.Process.Signal(os.Interrupt)
		select {
		case <-done:
		case <-time.After(10 * time.Second):
			cmd.Process.Kill()
			<-done
		}
		if e.t.Failed() {
			e.t.Logf("%s %s:\n%s", name, strings.Join(args, " "), log.String())
		}
	})
	return cmd, done
}

func (e *env) stopNode(name string) {
	e.nodeCmds[name].Process.Signal(os.Interrupt)
	select {
	case <-e.nodes[name]:
	case <-time.After(20 * time.Second):
		e.t.Fatalf("%s did not exit", name)
	}
}

func (e *env) gateway(args ...string) string {
	cmd := exec.Command(filepath.Join(e.bin, "sandbox-gateway"), append([]string{"--state", e.state}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		e.t.Fatalf("sandbox-gateway %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func (e *env) newKey(who string, args ...string) {
	out := e.gateway(append([]string{"keys", "create"}, args...)...)
	m := regexp.MustCompile(`(?m)^secret: (\S+)$`).FindStringSubmatch(out)
	if m == nil {
		e.t.Fatalf("keys create printed no secret:\n%s", out)
	}
	path := filepath.Join(e.dir, who+".key")
	if err := os.WriteFile(path, []byte(m[1]+"\n"), 0o600); err != nil {
		e.t.Fatal(err)
	}
	e.userKeys[who] = path
	if m := regexp.MustCompile(`(?m)^id: +(\S+)$`).FindStringSubmatch(out); m != nil {
		e.keyIDs[who] = m[1]
	}
}

// run runs sandbox-cli as user (its context), returning stdout and stderr.
func (e *env) run(user string, args ...string) (string, error) {
	if user != "" {
		// --context goes after the subcommand's own words: flags of a
		// command with SetInterspersed(false) (ssh) must precede SANDBOX.
		args = withContext(args, user)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	cmd := exec.CommandContext(ctx, filepath.Join(e.bin, "sandbox-cli"), args...)
	cmd.Env = e.cliEnv
	var buf bytes.Buffer
	cmd.Stdout, cmd.Stderr = &buf, &buf
	err := cmd.Run()
	return buf.String(), err
}

// withContext puts --context NAME after the command's words (the leading
// arguments that are not flags and not a sandbox), and before everything
// else.
func withContext(args []string, name string) []string {
	words := 1
	switch args[0] {
	case "ssh-key", "context", "volume":
		words = 2
	}
	if words > len(args) {
		words = len(args)
	}
	out := append([]string{}, args[:words]...)
	out = append(out, "--context", name)
	return append(out, args[words:]...)
}

func (e *env) cli(user string, args ...string) string {
	out, err := e.run(user, args...)
	if err != nil {
		e.t.Fatalf("sandbox-cli %v (as %s): %v\n%s", args, user, err, out)
	}
	return out
}

func (e *env) cliOK(user string, args ...string) { e.cli(user, args...) }

// sshInfo is GET /v1/ssh as user, read directly: the address the CLI and
// ssh-access are compared against.
func (e *env) sshInfo(user string) struct {
	Host     string   `json:"host"`
	Port     int      `json:"port"`
	HostKeys []string `json:"host_keys"`
} {
	key, err := os.ReadFile(e.userKeys[user])
	if err != nil {
		e.t.Fatal(err)
	}
	req, _ := http.NewRequest(http.MethodGet, e.apiURL+"/v1/ssh", nil)
	req.Header.Set("Authorization", "Bearer "+strings.TrimSpace(string(key)))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		e.t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	var info struct {
		Host     string   `json:"host"`
		Port     int      `json:"port"`
		HostKeys []string `json:"host_keys"`
	}
	if resp.StatusCode != http.StatusOK || json.Unmarshal(body, &info) != nil {
		e.t.Fatalf("GET /v1/ssh: %s %s", resp.Status, body)
	}
	return info
}

func (e *env) waitLog(s string) {
	waitFor(e.t, "the gateway to log "+strconv.Quote(s), func() bool { return strings.Contains(e.gwLog.String(), s) })
}

func waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

func freePort(t *testing.T) int {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	return ln.Addr().(*net.TCPAddr).Port
}

func exitCode(err error) int {
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	if err != nil {
		return -1
	}
	return 0
}

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

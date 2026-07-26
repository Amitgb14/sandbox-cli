//go:build docker_integration

// These tests require a running Docker daemon and the sandbox-base image (built
// lazily on first run). Enable with: go test -tags docker_integration ./...
package cli

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/image"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
	"github.com/Amitgb14/sandbox-cli/internal/worktree"
	"time"
)

func newTestSession(t *testing.T, cfg config.Config) *sandbox.Session {
	t.Helper()
	if !dockerAvailable() {
		t.Skip("docker daemon not available")
	}
	sess := sandbox.New(cfg)
	if d, ok := sess.Runtime.(*runtime.DockerCLI); ok {
		image.Register(d)
	}
	return sess
}

// runInSandbox runs a command in the sandbox and captures its stdout by shelling
// out to the built binary is avoided; instead we invoke docker via the runtime
// but redirect stdout through a temp file written inside /workspace.
func TestIsolation_HomeAndWorkspace(t *testing.T) {
	proj := t.TempDir()
	cfg := config.Default()
	sess := newTestSession(t, cfg)

	// Write a marker file inside the container's /workspace; assert it appears on host.
	code, err := sess.Run(context.Background(), sandbox.Options{
		Project: proj,
		TTY:     ptr(false),
		Command: []string{"sh", "-c", "echo $HOME > /workspace/home.txt && touch /workspace/created.txt"},
	}, false)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}

	homeTxt, err := os.ReadFile(filepath.Join(proj, "home.txt"))
	if err != nil {
		t.Fatalf("reading home.txt: %v", err)
	}
	if got := strings.TrimSpace(string(homeTxt)); got != "/sandbox/home" {
		t.Errorf("HOME inside container = %q, want /sandbox/home", got)
	}
	if _, err := os.Stat(filepath.Join(proj, "created.txt")); err != nil {
		t.Errorf("expected created.txt on host: %v", err)
	}
}

// TestRmRfHomeSafety proves that `rm -rf ~` inside the sandbox cannot touch the
// host home: we place a canary in a fake host HOME that is never mounted.
func TestRmRfHomeSafety(t *testing.T) {
	proj := t.TempDir()
	fakeHome := t.TempDir()
	canary := filepath.Join(fakeHome, "canary.txt")
	if err := os.WriteFile(canary, []byte("keep me"), 0o644); err != nil {
		t.Fatal(err)
	}

	sess := newTestSession(t, config.Default())
	_, err := sess.Run(context.Background(), sandbox.Options{
		Project: proj,
		TTY:     ptr(false),
		Command: []string{"sh", "-c", "rm -rf ~ || true; rm -rf / 2>/dev/null || true; echo done"},
	}, false)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}

	if _, err := os.Stat(canary); err != nil {
		t.Fatalf("host canary destroyed — isolation FAILED: %v", err)
	}
	// The fake host home dir and its contents remain intact.
	if _, err := os.Stat(fakeHome); err != nil {
		t.Fatalf("fake host home destroyed — isolation FAILED: %v", err)
	}
}

// TestEnvPassthrough proves allowlisted host env reaches the container while
// non-allowlisted vars do not.
func TestEnvPassthrough(t *testing.T) {
	proj := t.TempDir()
	t.Setenv("SANDBOX_TEST_ALLOWED", "yes")
	t.Setenv("SANDBOX_TEST_SECRET", "leak")

	sess := newTestSession(t, config.Default())
	_, err := sess.Run(context.Background(), sandbox.Options{
		Project:  proj,
		TTY:      ptr(false),
		EnvAllow: []string{"SANDBOX_TEST_ALLOWED"},
		Command:  []string{"sh", "-c", "echo allowed=$SANDBOX_TEST_ALLOWED secret=$SANDBOX_TEST_SECRET > /workspace/env.txt"},
	}, false)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	out, err := os.ReadFile(filepath.Join(proj, "env.txt"))
	if err != nil {
		t.Fatal(err)
	}
	s := string(out)
	if !strings.Contains(s, "allowed=yes") {
		t.Errorf("allowlisted var not forwarded: %q", s)
	}
	if strings.Contains(s, "secret=leak") {
		t.Errorf("non-allowlisted var leaked into container: %q", s)
	}
}

// TestEgressAllowlist proves two properties of allowlist mode: the firewall
// entrypoint drops back to the non-root sandbox user before running the guest
// (so the agent isn't root), and a destination outside the allowlist is blocked.
// It does not assert that an allowed domain succeeds, to avoid depending on live
// internet in CI; the block + privilege-drop are the security-relevant claims.
func TestEgressAllowlist(t *testing.T) {
	proj := t.TempDir()
	sess := newTestSession(t, config.Default())

	// 1.1.1.1 is not in the allowlist, so the default-deny rule must reject it;
	// curl then exits non-zero. whoami must report the dropped-to sandbox user.
	_, err := sess.Run(context.Background(), sandbox.Options{
		Project: proj,
		TTY:     ptr(false),
		Allow:   []string{"example.com"},
		Command: []string{"sh", "-c",
			"whoami > /workspace/who.txt; " +
				"curl -s -m 5 -o /dev/null https://1.1.1.1 && echo reachable > /workspace/blocked.txt || echo blocked > /workspace/blocked.txt"},
	}, false)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}

	who, err := os.ReadFile(filepath.Join(proj, "who.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(who)); got != "sandbox" {
		t.Errorf("guest ran as %q, want sandbox (firewall entrypoint must drop privileges)", got)
	}

	blocked, err := os.ReadFile(filepath.Join(proj, "blocked.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(blocked)); got != "blocked" {
		t.Errorf("non-allowlisted egress = %q, want blocked", got)
	}
}

// TestEgressAllowlistFiltersIngressToo proves the INPUT half of the firewall.
//
// An egress allowlist that ignores inbound traffic is not a boundary: the OUTPUT
// chain accepts ESTABLISHED,RELATED, so a connection someone else *dials in*
// gets a working reply path and carries data straight back out past the
// allowlist. That was demonstrated end to end — a second container on the bridge
// opened a socket to an allowlisted sandbox and got 30 bytes back, while that
// same sandbox could not reach 1.1.1.1 on its own initiative.
//
// Asserting on the chain rather than on a live connection keeps this hermetic:
// the rules are the property under test, and a two-container rendezvous would be
// slow and flaky in CI.
func TestEgressAllowlistFiltersIngressToo(t *testing.T) {
	proj := t.TempDir()
	sess := newTestSession(t, config.Default())

	// Read the chain from outside the container. The guest cannot: it is dropped
	// to an unprivileged user, and asking for --user root is refused precisely
	// because a root guest could flush these rules
	// (TestBuildSpec_RootWithAllowlistRefuses). So the container is started
	// detached and inspected with `docker exec -u root`, which is the deployed
	// rule set rather than a reconstruction of it.
	name, err := sess.Start(context.Background(), sandbox.Options{
		Project: proj,
		Allow:   []string{"example.com"},
		Publish: []string{"3000", "9000/udp"},
		Command: []string{"sleep", "30"},
	}, false)
	if err != nil {
		t.Fatalf("start error: %v", err)
	}
	defer exec.Command("docker", "rm", "-f", name).Run()

	// Wait for the rules, not merely for a successful exec: the container counts as
	// running the moment the entrypoint starts, which is before it has finished
	// programming anything, so an early read returns a bare "-P INPUT ACCEPT".
	var rules string
	for i := 0; i < 60; i++ {
		out, execErr := exec.Command("docker", "exec", "-u", "root", name, "iptables", "-S", "INPUT").Output()
		if execErr == nil && strings.Contains(string(out), "REJECT") {
			rules = string(out)
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if rules == "" {
		t.Fatal("the INPUT chain was never programmed")
	}

	// Default-deny, with the two exceptions that keep the sandbox usable:
	// loopback, and replies to connections the sandbox itself opened (which is
	// also what lets DNS answers and allowlisted HTTPS work at all).
	for _, want := range []string{
		"-A INPUT -i lo -j ACCEPT",
		"ESTABLISHED -j ACCEPT",
		"-A INPUT -j REJECT",
	} {
		if !strings.Contains(rules, want) {
			t.Errorf("INPUT chain missing %q:\n%s", want, rules)
		}
	}
	// --publish is the one deliberate request for ingress, so those ports stay
	// open — otherwise a dev server silently stops answering the moment someone
	// adds --allow.
	for _, want := range []string{"--dport 3000 -j ACCEPT", "--dport 9000 -j ACCEPT"} {
		if !strings.Contains(rules, want) {
			t.Errorf("published port carve-out missing %q:\n%s", want, rules)
		}
	}
	// The REJECT must come last, or the carve-outs below it never match.
	if i, j := strings.Index(rules, "-A INPUT -j REJECT"), strings.LastIndex(rules, "--dport"); i >= 0 && j > i {
		t.Errorf("REJECT precedes a port carve-out, so the carve-out is dead:\n%s", rules)
	}
}

// TestEgressIsEnforcedByNameNotAddress is the end-to-end proof for the whole
// egress-proxy change.
//
// The firewall it replaces permitted resolved addresses, so gist.github.com —
// which shares github.com's address and is a *write* endpoint, i.e. an
// exfiltration channel — answered under the baseline despite never being listed.
// The same for docs.python.org via pypi.org's Fastly IPs. Both must now be
// refused while the names actually on the list still work.
//
// Deliberately live rather than a rule-shape assertion: the previous defect was
// invisible in the rules (they looked correct, and were correct about addresses),
// so only a real connection distinguishes the fixed behaviour from the broken one.
func TestEgressIsEnforcedByNameNotAddress(t *testing.T) {
	proj := t.TempDir()
	sess := newTestSession(t, config.Default())

	script := `for h in github.com gist.github.com pypi.org docs.python.org; do
  code=$(curl -s -m 15 -o /dev/null -w "%{http_code}" "https://$h" 2>/dev/null)
  echo "$h=$code"
done > /workspace/egress.txt`

	if _, err := sess.Run(context.Background(), sandbox.Options{
		Project: proj,
		TTY:     ptr(false),
		Allow:   []string{"example.com"}, // baseline carries github.com and pypi.org
		Command: []string{"sh", "-c", script},
	}, false); err != nil {
		t.Fatalf("run error: %v", err)
	}
	out, err := os.ReadFile(filepath.Join(proj, "egress.txt"))
	if err != nil {
		t.Fatal(err)
	}
	got := string(out)

	// Parsed into exact lines rather than matched as substrings. Contains() is
	// wrong here for the same reason it is wrong in an allowlist: "github.com=000"
	// is a substring of "gist.github.com=000", so a substring check reported the
	// allowlisted host as blocked when it was the *unlisted* one that was blocked.
	// The bug this feature fixes, reproduced in the test for it.
	code := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(got), "\n") {
		if h, c, ok := strings.Cut(strings.TrimSpace(line), "="); ok {
			code[h] = c
		}
	}

	// On the list: reachable. If these fail the change has broken ordinary use,
	// which matters more than the hole it closes.
	for _, h := range []string{"github.com", "pypi.org"} {
		if code[h] == "" || code[h] == "000" {
			t.Errorf("%s is on the allowlist but was blocked (code %q):\n%s", h, code[h], got)
		}
	}
	// Not on the list, but sharing an allowlisted address: must be refused now.
	for _, h := range []string{"gist.github.com", "docs.python.org"} {
		if code[h] != "000" {
			t.Errorf("%s is not on the allowlist but returned %q — egress is still "+
				"matching addresses rather than names:\n%s", h, code[h], got)
		}
	}
}

// TestEgressProxyCannotBeBypassed pins that the enforcement is the redirect, not
// the environment. An agent that unsets HTTPS_PROXY, or dials an address with no
// hostname at all, must still be refused — otherwise the allowlist is advisory.
func TestEgressProxyCannotBeBypassed(t *testing.T) {
	proj := t.TempDir()
	sess := newTestSession(t, config.Default())

	script := `env -u HTTPS_PROXY -u https_proxy -u HTTP_PROXY -u http_proxy \
  curl -s -m 12 -o /dev/null -w "unset=%{http_code}\n" https://gist.github.com > /workspace/bypass.txt 2>&1
curl -s -m 8 -o /dev/null -w "altport=%{http_code}\n" https://gist.github.com:8443 >> /workspace/bypass.txt 2>&1`

	if _, err := sess.Run(context.Background(), sandbox.Options{
		Project: proj,
		TTY:     ptr(false),
		Allow:   []string{"example.com"},
		Command: []string{"sh", "-c", script},
	}, false); err != nil {
		t.Fatalf("run error: %v", err)
	}
	out, _ := os.ReadFile(filepath.Join(proj, "bypass.txt"))
	got := string(out)
	if !strings.Contains(got, "unset=000") {
		t.Errorf("unsetting the proxy env reached a blocked host; the env is not the boundary:\n%s", got)
	}
	if !strings.Contains(got, "altport=000") {
		t.Errorf("a non-80/443 port reached a blocked host:\n%s", got)
	}
}

// TestGitIdentity proves --git forwards the host git identity into the container
// and marks the workspace as trusted. It pins a deterministic host identity via
// an isolated global git config so the assertion doesn't depend on the CI user's
// real git settings.
func TestGitIdentity(t *testing.T) {
	proj := t.TempDir()

	gc := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(gc, []byte("[user]\n\tname = Test User\n\temail = test@example.com\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", gc)
	t.Setenv("GIT_CONFIG_SYSTEM", "/dev/null")
	// Register cleanup for the vars injectGitIdentity sets on this process.
	for _, n := range []string{"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL"} {
		t.Setenv(n, "")
	}

	sess := newTestSession(t, config.Default())
	_, err := sess.Run(context.Background(), sandbox.Options{
		Project:     proj,
		TTY:         ptr(false),
		GitIdentity: true,
		Command: []string{"sh", "-c",
			"printf %s \"$GIT_AUTHOR_EMAIL\" > /workspace/email.txt; " +
				"git config --get safe.directory > /workspace/safe.txt"},
	}, false)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}

	email, err := os.ReadFile(filepath.Join(proj, "email.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(email)); got != "test@example.com" {
		t.Errorf("GIT_AUTHOR_EMAIL in container = %q, want test@example.com", got)
	}

	safe, err := os.ReadFile(filepath.Join(proj, "safe.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(safe)); got != "*" {
		t.Errorf("git safe.directory in container = %q, want *", got)
	}
}

// TestHostGateway proves --host-gateway injects the host.docker.internal mapping
// so an agent can reach host services. It checks the mapping lands in the
// container's /etc/hosts (deterministic; does not require the gateway to be
// reachable from CI).
func TestHostGateway(t *testing.T) {
	proj := t.TempDir()
	sess := newTestSession(t, config.Default())
	_, err := sess.Run(context.Background(), sandbox.Options{
		Project:     proj,
		TTY:         ptr(false),
		HostGateway: true,
		Command:     []string{"sh", "-c", "grep host.docker.internal /etc/hosts > /workspace/hosts.txt || true"},
	}, false)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	hosts, err := os.ReadFile(filepath.Join(proj, "hosts.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(hosts), "host.docker.internal") {
		t.Errorf("host.docker.internal not mapped in /etc/hosts, got %q", string(hosts))
	}
}

// TestSecretDelivery proves the credential broker resolves each source kind
// (file / env / cmd) at run time and delivers the value into the container.
func TestSecretDelivery(t *testing.T) {
	proj := t.TempDir()

	secretFile := filepath.Join(t.TempDir(), "tok")
	if err := os.WriteFile(secretFile, []byte("file-value\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("SB_SECRET_SRC", "env-value")
	// Register cleanup for the vars injectSecrets sets on this process.
	for _, n := range []string{"TOK_FILE", "TOK_ENV", "TOK_CMD"} {
		t.Setenv(n, "")
	}

	sess := newTestSession(t, config.Default())
	_, err := sess.Run(context.Background(), sandbox.Options{
		Project: proj,
		TTY:     ptr(false),
		Secrets: []string{
			"TOK_FILE=file:" + secretFile,
			"TOK_ENV=env:SB_SECRET_SRC",
			"TOK_CMD=cmd:printf cmd-value",
		},
		Command: []string{"sh", "-c", `printf '%s|%s|%s' "$TOK_FILE" "$TOK_ENV" "$TOK_CMD" > /workspace/out.txt`},
	}, false)
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	out, err := os.ReadFile(filepath.Join(proj, "out.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := string(out); got != "file-value|env-value|cmd-value" {
		t.Errorf("secrets delivered = %q, want file-value|env-value|cmd-value", got)
	}
}

// TestCachePersistsAndWritable proves two things about --cache: the non-root
// sandbox user can write to a default cache path (so the named volume
// initialized with the right ownership), and data written there survives the
// --rm container into a later run. It writes only a probe dotfile into the npm
// cache volume and removes it in cleanup, so it does not destroy any real cache
// (it may leave the other default cache volumes created but empty).
func TestCachePersistsAndWritable(t *testing.T) {
	const probe = "/sandbox/home/.npm/.sbtest_probe"

	runCache := func(cmd string) string {
		proj := t.TempDir()
		sess := newTestSession(t, config.Default())
		if _, err := sess.Run(context.Background(), sandbox.Options{
			Project: proj,
			TTY:     ptr(false),
			Cache:   true,
			// Braces so the redirect captures the whole command (not just its
			// last statement), e.g. both `id -un` and `echo WROTE`.
			Command: []string{"sh", "-c", "{ " + cmd + " ; } > /workspace/out.txt 2>&1 || true"},
		}, false); err != nil {
			t.Fatalf("run error: %v", err)
		}
		out, err := os.ReadFile(filepath.Join(proj, "out.txt"))
		if err != nil {
			t.Fatal(err)
		}
		return strings.TrimSpace(string(out))
	}

	t.Cleanup(func() { runCache("rm -f " + probe) })

	// Run 1: the non-root user writes a probe into the npm cache volume.
	if got := runCache("id -un; echo cache-ok > " + probe + " && echo WROTE"); !strings.Contains(got, "sandbox") || !strings.Contains(got, "WROTE") {
		t.Fatalf("run 1: expected sandbox user + WROTE, got %q", got)
	}
	// Run 2: a fresh --rm container sees the persisted probe.
	if got := runCache("cat " + probe); got != "cache-ok" {
		t.Errorf("run 2: cache did not persist, read %q, want cache-ok", got)
	}
}

// TestWorktreeEndToEnd proves the full --worktree path: a worktree is created for
// a branch and the container mounts that branch's checkout at /workspace (so the
// agent sees the branch's content, not the main working tree's).
func TestWorktreeEndToEnd(t *testing.T) {
	if !dockerAvailable() {
		t.Skip("docker daemon not available")
	}
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("git not available")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir()) // isolate the managed worktree location

	repo := t.TempDir()
	gitRun := func(args ...string) {
		t.Helper()
		cmd := exec.Command(git, args...)
		cmd.Dir = repo
		if out, e := cmd.CombinedOutput(); e != nil {
			t.Fatalf("git %v: %v\n%s", args, e, out)
		}
	}
	// main has marker "on-main"; feature/x has marker "on-feature".
	gitRun("init", "-q")
	gitRun("config", "user.email", "t@example.com")
	gitRun("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(repo, "marker.txt"), []byte("on-main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun("add", ".")
	gitRun("commit", "-qm", "main")
	gitRun("checkout", "-q", "-b", "feature/x")
	if err := os.WriteFile(filepath.Join(repo, "marker.txt"), []byte("on-feature\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitRun("commit", "-qam", "feature")
	gitRun("checkout", "-q", "-") // free feature/x so it can be checked out in a worktree

	info, err := worktree.Resolve(repo, "feature/x")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		// Force: the container leaves an untracked file in the worktree.
		exec.Command(git, "-C", repo, "worktree", "remove", "--force", info.Path).Run()
	})
	if !info.Created {
		t.Fatalf("expected the worktree to be created")
	}

	// Run the sandbox in the worktree and have the container report what it sees
	// mounted at /workspace.
	sess := newTestSession(t, config.Default())
	if _, err := sess.Run(context.Background(), sandbox.Options{
		Project: info.Path,
		TTY:     ptr(false),
		Command: []string{"sh", "-c", "cat /workspace/marker.txt > /workspace/seen.txt"},
	}, false); err != nil {
		t.Fatalf("run error: %v", err)
	}
	seen, err := os.ReadFile(filepath.Join(info.Path, "seen.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(seen)); got != "on-feature" {
		t.Errorf("container mounted %q at /workspace; want the feature/x branch content on-feature", got)
	}
}

// dockerAvailable is a cheap precondition guard used by TestMain-style skips.
func dockerAvailable() bool {
	cmd := exec.Command("docker", "info")
	cmd.Stdout = &bytes.Buffer{}
	cmd.Stderr = &bytes.Buffer{}
	return cmd.Run() == nil
}

func ptr(b bool) *bool { return &b }

// Package conformance is the definition of "the same API in every mode".
//
// Run takes a client and nothing else. It does not know whether the endpoint is
// the in-process fake, a Mac running the local backend, a self-hosted Linux
// machine or the cloud — and it must not: a test that needs to know is a test of
// one backend, not of the API. Where endpoints legitimately differ, a test asks
// the endpoint's capabilities and skips with the reason; it never asks which
// mode it is talking to.
//
// Commands are limited to echo, cat, printenv, pwd, sleep, true and false — what
// every Linux image has, with the same behaviour — so the suite runs unchanged
// against a real VM. A test that needs more belongs in
// docs/testing/end-to-end.md.
//
// Rules for adding a test are in .claude/skills/conformance-test/SKILL.md.
package conformance

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
)

// Run runs the whole suite against the endpoint behind c.
func Run(t *testing.T, c *api.Client) {
	t.Helper()
	RunExcept(t, c, nil)
}

// RunExcept is Run for a test harness that stands in for part of an endpoint
// and cannot do what one test asks: except names that test and why, and the
// reason is the skip message, so it is read every time the suite runs. Not for
// a real endpoint — what an endpoint cannot do, its capabilities say — and a
// name that is not a test fails, so an exception cannot outlive its test.
func RunExcept(t *testing.T, c *api.Client, except map[string]string) {
	t.Helper()
	ctx := context.Background()
	caps, err := c.Capabilities(ctx)
	if err != nil {
		t.Fatalf("capabilities: %v", err)
	}
	e := &env{c: c, caps: caps}
	for _, tc := range []struct {
		name string
		fn   func(*testing.T, *env)
	}{
		{"CapabilitiesDescribeTheEndpoint", testCapabilities},
		{"SandboxLifecycle", testLifecycle},
		{"TerminateIsIdempotent", testTerminateIdempotent},
		{"NamesAreUniqueAmongLiveSandboxes", testNameConflict},
		{"InvalidNameIsRejected", testInvalidName},
		{"UnknownSandboxIsNotFound", testUnknownSandbox},
		{"EnvironmentValuesAreNeverReturned", testEnvValuesHidden},
		{"ReservedEnvironmentNamesAreRefused", testReservedEnv},
		{"RunCapturesOutputAndExitCode", testRunOutput},
		{"RunPassesStdin", testRunStdin},
		{"RunTimesOut", testRunTimeout},
		{"RunRejectsAnUnknownCommand", testUnknownCommand},
		{"RunUsesTheWorkingDirectory", testRunCwd},
		{"BackgroundProcessStreamsStdinToOutput", testBackgroundProcess},
		{"SignalEndsAProcess", testSignal},
		{"LateFollowerSeesOutputFromTheStart", testLateFollower},
		{"FilesRoundTrip", testFilesRoundTrip},
		{"FilesAndProcessesShareOneFilesystem", testFilesShared},
		{"RemovingANonEmptyDirectoryConflicts", testRemoveNonEmpty},
		{"PathTraversalIsRejected", testPathTraversal},
		{"TerminatedSandboxRefusesWork", testTerminatedRefuses},
		{"NetworkDefaultApplies", testNetworkDefault},
		{"NetworkAboveCeilingIsRefused", testNetworkAboveCeiling},
		{"NetworkEmptyAllowlistIsRefused", testNetworkEmptyAllowlist},
		{"NetworkDenyIsKeptAndNormalized", testNetworkDeny},
		{"NetworkAllowOutsideMayAllowIsRefused", testNetworkMayAllow},
		{"NetworkUpdateFollowsTheSameRules", testNetworkUpdate},
		{"ALiveSandboxCanBeRenamedRelabelledAndRetimed", testUpdateRecords},
		{"ImagesAreListedInstalledAndPinned", testImages},
		{"IdleSandboxIsTerminated", testIdleTimeout},
		{"IdleTimeoutAboveTheLimitIsInvalid", testIdleTimeoutLimit},
		{"ABusySandboxEndsAtItsLifetime", testLifetime},
		{"LifetimeAboveTheLimitIsInvalid", testLifetimeLimit},
		{"AttachStreamsInputAndOutput", testAttach},
		{"SuspendKeepsTheSandbox", testSuspend},
		{"ASnapshotForksTheSandbox", testSnapshot},
		{"MetricsSampleARunningSandbox", testMetrics},
		{"AScheduleKeepsTheNewestSnapshots", testSnapshotSchedule},
		{"ATunnelReachesAGuestPort", testTunnel},
		{"LabelsAreKeptAndFilterTheListing", testLabels},
		{"TheAuditLogRecordsWhatHappened", testAudit},
		{"TheAuditLogNeverKeepsAProcesssArguments", testAuditArgs},
		{"ProcessesDoNotRunAsRoot", testNotRoot},
		{"SandboxesCannotReachEachOther", testPeerIsolation},
		{"AVolumeOutlivesItsSandbox", testVolumes},
		{"VolumeMountsAreChecked", testVolumeRefusals},
		{"AVolumeHasOneWriterOrManyReaders", testVolumeSharing},
	} {
		why, skip := except[tc.name]
		delete(except, tc.name)
		t.Run(tc.name, func(t *testing.T) {
			if skip {
				t.Skip("excepted by the harness: " + why)
			}
			tc.fn(t, e)
		})
	}
	for name := range except {
		t.Errorf("RunExcept names %q, which is not a test in the suite", name)
	}
}

type env struct {
	c    *api.Client
	caps api.Capabilities
}

func ctxT(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// uniqueName is a sandbox name no other run of the suite will use, so two runs
// can share an endpoint.
func uniqueName(prefix string) string {
	var b [4]byte
	_, _ = rand.Read(b[:])
	return prefix + "-" + hex.EncodeToString(b[:])
}

// newSandbox creates a sandbox owned by the test, terminated when it ends —
// failed or not.
func (e *env) newSandbox(t *testing.T, req api.CreateSandboxRequest) api.Sandbox {
	t.Helper()
	sb, err := e.c.CreateSandbox(ctxT(t), req)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	t.Cleanup(func() { _ = e.c.TerminateSandbox(context.Background(), sb.ID) })
	return sb
}

func wantCode(t *testing.T, err error, code string) {
	t.Helper()
	if err == nil {
		t.Fatalf("succeeded; want error %q", code)
	}
	if !api.IsCode(err, code) {
		t.Fatalf("error = %v; want code %q", err, code)
	}
}

func (e *env) run(t *testing.T, ref string, req api.RunRequest) api.RunResult {
	t.Helper()
	res, err := e.c.Run(ctxT(t), ref, req)
	if err != nil {
		t.Fatalf("run %v: %v", req.Argv, err)
	}
	return res
}

// follow collects a process's whole output and its exit code.
func (e *env) follow(t *testing.T, ref string, pid int) (string, int) {
	t.Helper()
	var out bytes.Buffer
	code := -999
	err := e.c.FollowOutput(ctxT(t), ref, pid, func(ev api.OutputEvent) error {
		if ev.ExitCode != nil {
			code = *ev.ExitCode
		}
		out.Write(ev.Data)
		return nil
	})
	if err != nil {
		t.Fatalf("follow: %v", err)
	}
	if code == -999 {
		t.Fatal("output stream ended without an exit code")
	}
	return out.String(), code
}

// --- tests -------------------------------------------------------------------

func testCapabilities(t *testing.T, e *env) {
	c := e.caps
	if c.APIVersion != api.Version {
		t.Errorf("api_version = %q, want %q", c.APIVersion, api.Version)
	}
	if c.Backend == "" {
		t.Error("backend is empty")
	}
	if c.Limits.MaxCPUs <= 0 || c.Limits.MaxMemoryMB <= 0 || c.Limits.MaxDiskMB <= 0 {
		t.Errorf("limits must be positive: %+v", c.Limits)
	}
	if api.NetworkRank(c.Network.Ceiling) < 0 {
		t.Errorf("network ceiling %q is not a mode", c.Network.Ceiling)
	}
	if api.NetworkRank(c.Network.Default.Mode) > api.NetworkRank(c.Network.Ceiling) {
		t.Errorf("default network mode %q is above the ceiling %q", c.Network.Default.Mode, c.Network.Ceiling)
	}
}

func testLifecycle(t *testing.T, e *env) {
	name := uniqueName("life")
	sb := e.newSandbox(t, api.CreateSandboxRequest{Name: name})
	if sb.State != api.StateRunning {
		t.Fatalf("created sandbox is %q, want running", sb.State)
	}
	if !strings.HasPrefix(sb.ID, "sbx_") {
		t.Errorf("id %q does not look like a sandbox id", sb.ID)
	}
	ctx := ctxT(t)
	for _, ref := range []string{sb.ID, name} {
		got, err := e.c.Sandbox(ctx, ref)
		if err != nil || got.ID != sb.ID {
			t.Fatalf("get %q = %+v, %v; want %s", ref, got, err, sb.ID)
		}
	}
	list, err := e.c.Sandboxes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, s := range list {
		found = found || s.ID == sb.ID
	}
	if !found {
		t.Error("created sandbox is not listed")
	}
	if err := e.c.TerminateSandbox(ctx, sb.ID); err != nil {
		t.Fatal(err)
	}
	got, err := e.c.Sandbox(ctx, sb.ID)
	if err != nil || got.State != api.StateTerminated {
		t.Fatalf("after terminate: %+v, %v; want terminated", got, err)
	}
}

func testTerminateIdempotent(t *testing.T, e *env) {
	sb := e.newSandbox(t, api.CreateSandboxRequest{})
	ctx := ctxT(t)
	for i := 0; i < 2; i++ {
		if err := e.c.TerminateSandbox(ctx, sb.ID); err != nil {
			t.Fatalf("terminate #%d: %v", i+1, err)
		}
	}
}

func testNameConflict(t *testing.T, e *env) {
	name := uniqueName("dup")
	first := e.newSandbox(t, api.CreateSandboxRequest{Name: name})
	_, err := e.c.CreateSandbox(ctxT(t), api.CreateSandboxRequest{Name: name})
	wantCode(t, err, api.CodeConflict)
	// Once the first is gone, the name is free again.
	if err := e.c.TerminateSandbox(ctxT(t), first.ID); err != nil {
		t.Fatal(err)
	}
	second := e.newSandbox(t, api.CreateSandboxRequest{Name: name})
	if second.ID == first.ID {
		t.Fatal("reusing a name returned the terminated sandbox")
	}
	got, err := e.c.Sandbox(ctxT(t), name)
	if err != nil || got.ID != second.ID {
		t.Fatalf("name resolves to %+v, %v; want the live sandbox %s", got, err, second.ID)
	}
}

func testInvalidName(t *testing.T, e *env) {
	for _, name := range []string{"Upper", "has space", "sbx_lookslikeanid", "-lead", strings.Repeat("a", 64)} {
		_, err := e.c.CreateSandbox(ctxT(t), api.CreateSandboxRequest{Name: name})
		if !api.IsCode(err, api.CodeInvalidRequest) {
			t.Errorf("name %q: error = %v; want invalid_request", name, err)
		}
	}
}

func testUnknownSandbox(t *testing.T, e *env) {
	_, err := e.c.Sandbox(ctxT(t), uniqueName("nobody"))
	wantCode(t, err, api.CodeNotFound)
	_, err = e.c.Run(ctxT(t), "sbx_0000000000000000", api.RunRequest{Argv: []string{"true"}})
	wantCode(t, err, api.CodeNotFound)
}

func testEnvValuesHidden(t *testing.T, e *env) {
	const secret = "s3cr3t-value-that-must-not-echo"
	sb := e.newSandbox(t, api.CreateSandboxRequest{Env: map[string]string{"API_TOKEN": secret}})
	raw, _ := json.Marshal(sb)
	if bytes.Contains(raw, []byte(secret)) {
		t.Fatalf("create response contains the value: %s", raw)
	}
	if len(sb.EnvNames) != 1 || sb.EnvNames[0] != "API_TOKEN" {
		t.Errorf("env_names = %v, want [API_TOKEN]", sb.EnvNames)
	}
	got, _ := e.c.Sandbox(ctxT(t), sb.ID)
	raw, _ = json.Marshal(got)
	if bytes.Contains(raw, []byte(secret)) {
		t.Fatalf("get response contains the value: %s", raw)
	}
	// It is hidden from the API, not from the sandbox.
	res := e.run(t, sb.ID, api.RunRequest{Argv: []string{"printenv", "API_TOKEN"}})
	if strings.TrimSpace(string(res.Stdout)) != secret {
		t.Errorf("the sandbox does not see the variable: %q", res.Stdout)
	}
}

func testReservedEnv(t *testing.T, e *env) {
	_, err := e.c.CreateSandbox(ctxT(t), api.CreateSandboxRequest{Env: map[string]string{"LD_PRELOAD": "/x.so"}})
	wantCode(t, err, api.CodeRefused)
	sb := e.newSandbox(t, api.CreateSandboxRequest{})
	_, err = e.c.Run(ctxT(t), sb.ID, api.RunRequest{Argv: []string{"true"}, Env: map[string]string{"BASH_ENV": "/x"}})
	wantCode(t, err, api.CodeRefused)
}

func testRunOutput(t *testing.T, e *env) {
	sb := e.newSandbox(t, api.CreateSandboxRequest{})
	res := e.run(t, sb.ID, api.RunRequest{Argv: []string{"echo", "hello", "world"}})
	if res.ExitCode != 0 || string(res.Stdout) != "hello world\n" || res.TimedOut || res.Truncated {
		t.Errorf("echo: %+v", res)
	}
	res = e.run(t, sb.ID, api.RunRequest{Argv: []string{"false"}})
	if res.ExitCode != 1 {
		t.Errorf("false exited %d, want 1", res.ExitCode)
	}
}

func testRunStdin(t *testing.T, e *env) {
	sb := e.newSandbox(t, api.CreateSandboxRequest{})
	in := []byte("line one\nline two\n")
	res := e.run(t, sb.ID, api.RunRequest{Argv: []string{"cat"}, Stdin: in})
	if !bytes.Equal(res.Stdout, in) || res.ExitCode != 0 {
		t.Errorf("cat stdin: stdout=%q exit=%d", res.Stdout, res.ExitCode)
	}
}

func testRunTimeout(t *testing.T, e *env) {
	sb := e.newSandbox(t, api.CreateSandboxRequest{})
	start := time.Now()
	res := e.run(t, sb.ID, api.RunRequest{Argv: []string{"sleep", "30"}, TimeoutSecs: 1})
	if !res.TimedOut || res.ExitCode != -1 {
		t.Errorf("result %+v; want timed_out and exit -1", res)
	}
	// Generous: this bounds "was killed", not how fast.
	if d := time.Since(start); d > 15*time.Second {
		t.Errorf("a 1s timeout took %v", d)
	}
}

func testUnknownCommand(t *testing.T, e *env) {
	sb := e.newSandbox(t, api.CreateSandboxRequest{})
	_, err := e.c.Run(ctxT(t), sb.ID, api.RunRequest{Argv: []string{"definitely-not-a-command-x9"}})
	wantCode(t, err, api.CodeInvalidRequest)
	_, err = e.c.Run(ctxT(t), sb.ID, api.RunRequest{})
	wantCode(t, err, api.CodeInvalidRequest)
}

func testRunCwd(t *testing.T, e *env) {
	sb := e.newSandbox(t, api.CreateSandboxRequest{})
	res := e.run(t, sb.ID, api.RunRequest{Argv: []string{"pwd"}, Cwd: "/tmp"})
	if strings.TrimSpace(string(res.Stdout)) != "/tmp" {
		t.Errorf("pwd in /tmp printed %q", res.Stdout)
	}
	_, err := e.c.Run(ctxT(t), sb.ID, api.RunRequest{Argv: []string{"pwd"}, Cwd: "tmp"})
	wantCode(t, err, api.CodeInvalidRequest)
	// A directory that does not exist is named as the problem. It used to come
	// back as "no such command" — the same code, blaming a command that exists.
	_, err = e.c.Run(ctxT(t), sb.ID, api.RunRequest{Argv: []string{"pwd"}, Cwd: "/no-such-dir"})
	wantCode(t, err, api.CodeInvalidRequest)
	var ae *api.Error
	if !errors.As(err, &ae) || !strings.Contains(ae.Message, "cwd") {
		t.Errorf("a missing cwd was reported as %v; want it named as the cwd", err)
	}
	// A process starts in the sandbox user's home, whatever the image: it is
	// where a sandbox's work happens, and there is no other workspace.
	res = e.run(t, sb.ID, api.RunRequest{Argv: []string{"pwd"}})
	if strings.TrimSpace(string(res.Stdout)) != "/sandbox/home" {
		t.Errorf("pwd with no cwd printed %q, want /sandbox/home", res.Stdout)
	}
}

func testBackgroundProcess(t *testing.T, e *env) {
	sb := e.newSandbox(t, api.CreateSandboxRequest{})
	ctx := ctxT(t)
	p, err := e.c.StartProcess(ctx, sb.ID, api.RunRequest{Argv: []string{"cat"}})
	if err != nil {
		t.Fatal(err)
	}
	if p.State != api.ProcessRunning || p.PID <= 0 {
		t.Fatalf("started process: %+v", p)
	}
	if err := e.c.WriteStdin(ctx, sb.ID, p.PID, []byte("ping\n"), false); err != nil {
		t.Fatal(err)
	}
	if err := e.c.WriteStdin(ctx, sb.ID, p.PID, []byte("pong\n"), true); err != nil {
		t.Fatal(err)
	}
	out, code := e.follow(t, sb.ID, p.PID)
	if out != "ping\npong\n" || code != 0 {
		t.Errorf("output %q exit %d", out, code)
	}
	got, err := e.c.Process(ctx, sb.ID, p.PID)
	if err != nil || got.State != api.ProcessExited || got.ExitCode == nil || *got.ExitCode != 0 {
		t.Errorf("after exit: %+v, %v", got, err)
	}
	list, err := e.c.Processes(ctx, sb.ID)
	if err != nil || len(list) != 1 || list[0].PID != p.PID {
		t.Errorf("process list: %+v, %v", list, err)
	}
	err = e.c.WriteStdin(ctx, sb.ID, p.PID, []byte("late"), false)
	wantCode(t, err, api.CodeConflict)
}

func testSignal(t *testing.T, e *env) {
	sb := e.newSandbox(t, api.CreateSandboxRequest{})
	ctx := ctxT(t)
	p, err := e.c.StartProcess(ctx, sb.ID, api.RunRequest{Argv: []string{"sleep", "30"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := e.c.Signal(ctx, sb.ID, p.PID, "TERM"); err != nil {
		t.Fatal(err)
	}
	if _, code := e.follow(t, sb.ID, p.PID); code != 128+15 {
		t.Errorf("exit after TERM = %d, want %d", code, 128+15)
	}
	err = e.c.Signal(ctx, sb.ID, p.PID, "STOP")
	wantCode(t, err, api.CodeInvalidRequest)
}

func testLateFollower(t *testing.T, e *env) {
	sb := e.newSandbox(t, api.CreateSandboxRequest{})
	ctx := ctxT(t)
	p, err := e.c.StartProcess(ctx, sb.ID, api.RunRequest{Argv: []string{"echo", "early"}})
	if err != nil {
		t.Fatal(err)
	}
	// Wait for it to finish before anyone follows.
	deadline := time.Now().Add(20 * time.Second)
	for {
		got, err := e.c.Process(ctx, sb.ID, p.PID)
		if err != nil {
			t.Fatal(err)
		}
		if got.State == api.ProcessExited {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("echo never exited")
		}
		time.Sleep(20 * time.Millisecond)
	}
	for i := 0; i < 2; i++ { // and a second follower sees the same
		if out, code := e.follow(t, sb.ID, p.PID); out != "early\n" || code != 0 {
			t.Errorf("follower %d: output %q exit %d", i+1, out, code)
		}
	}
}

func testFilesRoundTrip(t *testing.T, e *env) {
	sb := e.newSandbox(t, api.CreateSandboxRequest{})
	ctx := ctxT(t)
	data := []byte("package main\n\x00binary-safe\xff\n")
	if err := e.c.WriteFile(ctx, sb.ID, "/sandbox/home/nested/dir/f.txt", data); err != nil {
		t.Fatal(err)
	}
	got, err := e.c.ReadFile(ctx, sb.ID, "/sandbox/home/nested/dir/f.txt")
	if err != nil || !bytes.Equal(got, data) {
		t.Fatalf("read back %q, %v", got, err)
	}
	entries, err := e.c.ListDir(ctx, sb.ID, "/sandbox/home/nested/dir")
	if err != nil || len(entries) != 1 || entries[0].Name != "f.txt" || entries[0].Type != "file" || entries[0].Size != int64(len(data)) {
		t.Fatalf("listing: %+v, %v", entries, err)
	}
	entries, err = e.c.ListDir(ctx, sb.ID, "/sandbox/home/nested")
	if err != nil || len(entries) != 1 || entries[0].Type != "dir" {
		t.Fatalf("parent listing: %+v, %v", entries, err)
	}
	if err := e.c.RemoveFile(ctx, sb.ID, "/sandbox/home/nested/dir/f.txt"); err != nil {
		t.Fatal(err)
	}
	_, err = e.c.ReadFile(ctx, sb.ID, "/sandbox/home/nested/dir/f.txt")
	wantCode(t, err, api.CodeNotFound)
}

func testFilesShared(t *testing.T, e *env) {
	sb := e.newSandbox(t, api.CreateSandboxRequest{})
	if err := e.c.WriteFile(ctxT(t), sb.ID, "/sandbox/home/shared.txt", []byte("via the API\n")); err != nil {
		t.Fatal(err)
	}
	res := e.run(t, sb.ID, api.RunRequest{Argv: []string{"cat", "/sandbox/home/shared.txt"}})
	if string(res.Stdout) != "via the API\n" {
		t.Errorf("a process read %q", res.Stdout)
	}
	res = e.run(t, sb.ID, api.RunRequest{Argv: []string{"cat", "shared.txt"}, Cwd: "/sandbox/home"})
	if string(res.Stdout) != "via the API\n" {
		t.Errorf("a relative path from cwd read %q", res.Stdout)
	}
}

func testRemoveNonEmpty(t *testing.T, e *env) {
	sb := e.newSandbox(t, api.CreateSandboxRequest{})
	ctx := ctxT(t)
	if err := e.c.WriteFile(ctx, sb.ID, "/sandbox/home/d/x", []byte("x")); err != nil {
		t.Fatal(err)
	}
	wantCode(t, e.c.RemoveFile(ctx, sb.ID, "/sandbox/home/d"), api.CodeConflict)
}

func testPathTraversal(t *testing.T, e *env) {
	sb := e.newSandbox(t, api.CreateSandboxRequest{})
	ctx := ctxT(t)
	for _, p := range []string{"relative/path", "/workspace/../etc/passwd", "/a/../../b", "", "/x\x00y"} {
		_, err := e.c.ReadFile(ctx, sb.ID, p)
		if !api.IsCode(err, api.CodeInvalidRequest) {
			t.Errorf("read %q: error = %v; want invalid_request", p, err)
		}
		if err := e.c.WriteFile(ctx, sb.ID, p, []byte("x")); !api.IsCode(err, api.CodeInvalidRequest) {
			t.Errorf("write %q: error = %v; want invalid_request", p, err)
		}
	}
}

func testTerminatedRefuses(t *testing.T, e *env) {
	sb := e.newSandbox(t, api.CreateSandboxRequest{})
	ctx := ctxT(t)
	if err := e.c.TerminateSandbox(ctx, sb.ID); err != nil {
		t.Fatal(err)
	}
	_, err := e.c.Run(ctx, sb.ID, api.RunRequest{Argv: []string{"true"}})
	wantCode(t, err, api.CodeConflict)
	wantCode(t, e.c.WriteFile(ctx, sb.ID, "/sandbox/home/x", []byte("x")), api.CodeConflict)
}

func testNetworkDefault(t *testing.T, e *env) {
	sb := e.newSandbox(t, api.CreateSandboxRequest{})
	if sb.Network.Mode != e.caps.Network.Default.Mode {
		t.Errorf("network mode %q, want the server default %q", sb.Network.Mode, e.caps.Network.Default.Mode)
	}
}

func testNetworkAboveCeiling(t *testing.T, e *env) {
	if e.caps.Network.Ceiling == api.NetworkOpen {
		t.Skip("ceiling is open: no mode is above it")
	}
	_, err := e.c.CreateSandbox(ctxT(t), api.CreateSandboxRequest{Network: &api.NetworkPolicy{Mode: api.NetworkOpen}})
	wantCode(t, err, api.CodeRefused)
	_, err = e.c.CreateSandbox(ctxT(t), api.CreateSandboxRequest{Network: &api.NetworkPolicy{Mode: "wide-open"}})
	wantCode(t, err, api.CodeInvalidRequest)
	// Tightening is always allowed.
	sb := e.newSandbox(t, api.CreateSandboxRequest{Network: &api.NetworkPolicy{Mode: api.NetworkNone}})
	if sb.Network.Mode != api.NetworkNone {
		t.Errorf("asked for none, got %q", sb.Network.Mode)
	}
}

func testNetworkEmptyAllowlist(t *testing.T, e *env) {
	if api.NetworkRank(e.caps.Network.Ceiling) < api.NetworkRank(api.NetworkAllowlist) || !e.caps.Has(api.CapEgressAllowlist) {
		t.Skip("no enforceable allowlist here")
	}
	_, err := e.c.CreateSandbox(ctxT(t), api.CreateSandboxRequest{
		Network: &api.NetworkPolicy{Mode: api.NetworkAllowlist, Allow: []string{}},
	})
	wantCode(t, err, api.CodeRefused)
}

func testNetworkDeny(t *testing.T, e *env) {
	if api.NetworkRank(e.caps.Network.Ceiling) < api.NetworkRank(api.NetworkAllowlist) || !e.caps.Has(api.CapEgressAllowlist) {
		t.Skip("no enforceable allowlist here")
	}
	sb := e.newSandbox(t, api.CreateSandboxRequest{
		Network: &api.NetworkPolicy{Mode: api.NetworkAllowlist, Deny: []string{"Gist.GitHub.com.", "gist.github.com"}},
	})
	if len(sb.Network.Deny) != 1 || sb.Network.Deny[0] != "gist.github.com" {
		t.Errorf("deny = %v; want [gist.github.com], normalized and deduplicated", sb.Network.Deny)
	}
	if len(sb.Network.Allow) == 0 {
		t.Error("an omitted allow list did not inherit the server default")
	}
	_, err := e.c.CreateSandbox(ctxT(t), api.CreateSandboxRequest{
		Network: &api.NetworkPolicy{Mode: api.NetworkAllowlist, Deny: []string{"https://example.com/path"}},
	})
	wantCode(t, err, api.CodeInvalidRequest)
}

func testNetworkMayAllow(t *testing.T, e *env) {
	if api.NetworkRank(e.caps.Network.Ceiling) < api.NetworkRank(api.NetworkAllowlist) {
		t.Skip("ceiling is below allowlist")
	}
	for _, m := range e.caps.Network.MayAllow {
		if m == "*" {
			t.Skip("may_allow is [*]: every name is permitted")
		}
	}
	_, err := e.c.CreateSandbox(ctxT(t), api.CreateSandboxRequest{
		Network: &api.NetworkPolicy{Mode: api.NetworkAllowlist, Allow: []string{"not-permitted.conformance.invalid"}},
	})
	wantCode(t, err, api.CodeRefused)
}

func testNetworkUpdate(t *testing.T, e *env) {
	sb := e.newSandbox(t, api.CreateSandboxRequest{})
	ctx := ctxT(t)
	none := api.UpdateSandboxRequest{Network: &api.NetworkPolicy{Mode: api.NetworkNone}}
	if !e.caps.Has(api.CapNetworkPolicyUpdate) {
		_, err := e.c.UpdateSandbox(ctx, sb.ID, none)
		wantCode(t, err, api.CodeUnsupported)
		return
	}
	got, err := e.c.UpdateSandbox(ctx, sb.ID, none)
	if err != nil || got.Network.Mode != api.NetworkNone {
		t.Fatalf("update to none: %+v, %v", got, err)
	}
	if e.caps.Network.Ceiling != api.NetworkOpen {
		_, err = e.c.UpdateSandbox(ctx, sb.ID, api.UpdateSandboxRequest{Network: &api.NetworkPolicy{Mode: api.NetworkOpen}})
		wantCode(t, err, api.CodeRefused)
		after, _ := e.c.Sandbox(ctx, sb.ID)
		if after.Network.Mode != api.NetworkNone {
			t.Errorf("a refused update changed the policy to %q", after.Network.Mode)
		}
	}
}

// The images an endpoint manages (capability images): the default image is
// listed once a sandbox starts from it, as in use and pinned; installing it
// again runs to installed; and what is not an image, or not installed, is
// refused. Nothing else is installed or removed, so the test is safe against
// a real server's registry and disk. Without the capability every call is
// unsupported.
func testImages(t *testing.T, e *env) {
	ctx := ctxT(t)
	if !e.caps.Has(api.CapImages) {
		_, err := e.c.Images(ctx)
		wantCode(t, err, api.CodeUnsupported)
		_, err = e.c.InstallImage(ctx, "example.com/a:1")
		wantCode(t, err, api.CodeUnsupported)
		return
	}
	e.newSandbox(t, api.CreateSandboxRequest{})
	find := func() (api.Image, bool) {
		list, err := e.c.Images(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, img := range list {
			if img.Default {
				return img, true
			}
		}
		return api.Image{}, false
	}
	def, ok := find()
	if !ok || def.InUse < 1 {
		t.Fatalf("the default image is not listed as in use by the sandbox started from it: %+v, %v", def, ok)
	}
	_, err := e.c.RemoveImage(ctx, def.Image)
	wantCode(t, err, api.CodeConflict)

	got, err := e.c.InstallImage(ctx, def.Image)
	if err != nil || got.Image != def.Image {
		t.Fatalf("installing the default image again: %+v, %v", got, err)
	}
	deadline := time.Now().Add(3 * time.Minute)
	for {
		img, _ := find()
		if img.State == api.ImageInstalled {
			break
		}
		if img.State == api.ImageFailed || time.Now().After(deadline) {
			t.Fatalf("the default image did not install again: %+v", img)
		}
		time.Sleep(200 * time.Millisecond)
	}

	_, err = e.c.InstallImage(ctx, "Not An Image!")
	wantCode(t, err, api.CodeInvalidRequest)
	_, err = e.c.RemoveImage(ctx, "registry.invalid/never/installed:"+uniqueName("tag"))
	wantCode(t, err, api.CodeNotFound)
}

// clientLabels is a sandbox's labels without those a gateway keeps for
// itself (gateway.*), which an update neither shows nor touches.
func clientLabels(l map[string]string) map[string]string {
	out := map[string]string{}
	for k, v := range l {
		if !strings.HasPrefix(k, "gateway.") {
			out[k] = v
		}
	}
	return out
}

// A live sandbox's name, labels and idle timeout change in place, under the
// rules they were set by at create: a name stays unique among live
// sandboxes and resolves at once, labels are replaced whole and filter the
// listing, and the idle timeout stays within the limit. A request with one
// bad field changes nothing.
func testUpdateRecords(t *testing.T, e *env) {
	ctx := ctxT(t)
	sb := e.newSandbox(t, api.CreateSandboxRequest{Name: uniqueName("before"), Labels: map[string]string{"suite.keep": "no", "team": "a"}})
	str := func(s string) *string { return &s }
	labels := func(m map[string]string) *map[string]string { return &m }
	num := func(n int) *int { return &n }

	name := uniqueName("after")
	mark := uniqueName("mark")
	got, err := e.c.UpdateSandbox(ctx, sb.ID, api.UpdateSandboxRequest{Name: &name, Labels: labels(map[string]string{"suite.mark": mark})})
	if err != nil {
		t.Fatalf("rename and relabel: %v", err)
	}
	if got.Name != name || len(clientLabels(got.Labels)) != 1 || got.Labels["suite.mark"] != mark {
		t.Fatalf("after the update: name %q labels %v", got.Name, got.Labels)
	}
	if byName, err := e.c.Sandbox(ctx, name); err != nil || byName.ID != sb.ID {
		t.Errorf("the new name resolves to %+v, %v", byName, err)
	}
	if list, err := e.c.Sandboxes(ctx, "suite.mark="+mark); err != nil || len(list) != 1 || list[0].ID != sb.ID {
		t.Errorf("listing by the new label: %d, %v", len(list), err)
	}

	// The name is unique among live sandboxes, as at create.
	other := e.newSandbox(t, api.CreateSandboxRequest{Name: uniqueName("other")})
	_, err = e.c.UpdateSandbox(ctx, other.ID, api.UpdateSandboxRequest{Name: &name})
	wantCode(t, err, api.CodeConflict)

	// One bad field and nothing changes, the good ones included.
	limit := e.caps.Limits.MaxIdleTimeoutSecs
	for what, req := range map[string]api.UpdateSandboxRequest{
		"an invalid name":  {Name: str("Upper Case"), Labels: labels(map[string]string{"x": "y"})},
		"an invalid label": {Name: str(uniqueName("ok")), Labels: labels(map[string]string{"k": "esc\x1b[2J"})},
		"a negative idle":  {Name: str(uniqueName("ok")), IdleTimeoutSecs: num(-1)},
		"nothing at all":   {},
	} {
		_, err := e.c.UpdateSandbox(ctx, sb.ID, req)
		wantCode(t, err, api.CodeInvalidRequest)
		after, _ := e.c.Sandbox(ctx, sb.ID)
		if after.Name != name || after.Labels["suite.mark"] != mark {
			t.Errorf("%s changed the sandbox: name %q labels %v", what, after.Name, after.Labels)
		}
	}

	// The idle timeout, within the limit; "never" only without one.
	got, err = e.c.UpdateSandbox(ctx, sb.ID, api.UpdateSandboxRequest{IdleTimeoutSecs: num(120)})
	if err != nil || got.IdleTimeoutSecs != 120 {
		t.Fatalf("idle timeout: %d, %v", got.IdleTimeoutSecs, err)
	}
	if limit > 0 {
		_, err = e.c.UpdateSandbox(ctx, sb.ID, api.UpdateSandboxRequest{IdleTimeoutSecs: num(limit + 1)})
		wantCode(t, err, api.CodeInvalidRequest)
		_, err = e.c.UpdateSandbox(ctx, sb.ID, api.UpdateSandboxRequest{IdleTimeoutSecs: num(0)})
		wantCode(t, err, api.CodeInvalidRequest)
	}

	// {} removes every label a client set; "" removes the name.
	got, err = e.c.UpdateSandbox(ctx, sb.ID, api.UpdateSandboxRequest{Name: str(""), Labels: labels(map[string]string{})})
	if err != nil || got.Name != "" || len(clientLabels(got.Labels)) != 0 {
		t.Fatalf("clearing: name %q labels %v, %v", got.Name, got.Labels, err)
	}

	// A terminated sandbox is past changing.
	if err := e.c.TerminateSandbox(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	_, err = e.c.UpdateSandbox(ctx, other.ID, api.UpdateSandboxRequest{Labels: labels(map[string]string{"late": "x"})})
	wantCode(t, err, api.CodeConflict)
}

func testIdleTimeout(t *testing.T, e *env) {
	sb := e.newSandbox(t, api.CreateSandboxRequest{IdleTimeoutSecs: 2})
	if sb.IdleTimeoutSecs != 2 {
		t.Fatalf("idle_timeout_secs = %d, want 2", sb.IdleTimeoutSecs)
	}
	// Watched through the listing: a GET names the sandbox, and naming it is
	// activity, which would keep it alive.
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		list, err := e.c.Sandboxes(ctxT(t))
		if err != nil {
			t.Fatal(err)
		}
		for _, s := range list {
			if s.ID == sb.ID && s.State == api.StateTerminated {
				return
			}
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatal("an idle sandbox was not terminated")
}

// A lifetime ends a sandbox whatever it is doing: here a process keeps it
// busy, which an idle timeout would respect and a lifetime does not.
func testLifetime(t *testing.T, e *env) {
	sb := e.newSandbox(t, api.CreateSandboxRequest{LifetimeSecs: 3})
	if sb.LifetimeSecs != 3 || sb.ExpiresAt == nil {
		t.Fatalf("lifetime_secs %d, expires_at %v; want 3 and a time", sb.LifetimeSecs, sb.ExpiresAt)
	}
	if d := sb.ExpiresAt.Sub(sb.CreatedAt); d != 3*time.Second {
		t.Errorf("expires_at is %v after created_at, want 3s", d)
	}
	if _, err := e.c.StartProcess(ctxT(t), sb.ID, api.RunRequest{Argv: []string{"sleep", "600"}}); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		got, err := e.c.Sandbox(ctxT(t), sb.ID)
		if err != nil {
			t.Fatal(err)
		}
		if got.State == api.StateTerminated {
			return
		}
		time.Sleep(250 * time.Millisecond)
	}
	t.Fatal("a sandbox outlived its lifetime")
}

func testLifetimeLimit(t *testing.T, e *env) {
	max := e.caps.Limits.MaxLifetimeSecs
	if max <= 0 {
		t.Skip("no lifetime limit")
	}
	_, err := e.c.CreateSandbox(ctxT(t), api.CreateSandboxRequest{LifetimeSecs: max + 1})
	wantCode(t, err, api.CodeInvalidRequest)
	sb := e.newSandbox(t, api.CreateSandboxRequest{})
	if sb.LifetimeSecs != max || sb.ExpiresAt == nil {
		t.Errorf("a sandbox asking for none: lifetime_secs %d, expires_at %v; want the limit, %d", sb.LifetimeSecs, sb.ExpiresAt, max)
	}
}

func testIdleTimeoutLimit(t *testing.T, e *env) {
	max := e.caps.Limits.MaxIdleTimeoutSecs
	if max <= 0 {
		t.Skip("no idle timeout limit")
	}
	_, err := e.c.CreateSandbox(ctxT(t), api.CreateSandboxRequest{IdleTimeoutSecs: max + 1})
	wantCode(t, err, api.CodeInvalidRequest)
}

// Attach holds a two-way stream on a running process: input reaches it, output
// comes back, the exit code arrives last — and a second attach after it exited
// still sees everything. Detaching does not stop a process.
func testAttach(t *testing.T, e *env) {
	sb := e.newSandbox(t, api.CreateSandboxRequest{})
	ctx := ctxT(t)
	p, err := e.c.StartProcess(ctx, sb.ID, api.RunRequest{Argv: []string{"cat"}})
	if err != nil {
		t.Fatal(err)
	}
	// Attach, write, and detach without ending input: the process keeps running.
	st, err := e.c.Attach(ctx, sb.ID, p.PID)
	if err != nil {
		t.Fatalf("attach: %v", err)
	}
	st.Write([]byte("first\n"))
	st.Close()
	time.Sleep(100 * time.Millisecond)
	if got, _ := e.c.Process(ctx, sb.ID, p.PID); got.State != api.ProcessRunning {
		t.Fatalf("detaching stopped the process: %+v", got)
	}
	// Attach again, finish the input, and read to the end.
	st, err = e.c.Attach(ctx, sb.ID, p.PID)
	if err != nil {
		t.Fatal(err)
	}
	st.Write([]byte("second\n"))
	st.CloseStdin()
	var out bytes.Buffer
	code, err := st.Copy(&out, &out)
	st.Close()
	if err != nil || code != 0 || out.String() != "first\nsecond\n" {
		t.Fatalf("attach: output %q, exit %d, err %v", out.String(), code, err)
	}
	// After exit, an attach replays and ends with the code.
	st, err = e.c.Attach(ctx, sb.ID, p.PID)
	if err != nil {
		t.Fatal(err)
	}
	out.Reset()
	code, err = st.Copy(&out, &out)
	st.Close()
	if err != nil || code != 0 || out.String() != "first\nsecond\n" {
		t.Fatalf("late attach: output %q, exit %d, err %v", out.String(), code, err)
	}
	// A plain request to the attach endpoint, without the upgrade, is refused.
	if _, err := e.c.Run(ctx, sb.ID, api.RunRequest{Argv: []string{"true"}, Tty: true}); !api.IsCode(err, api.CodeInvalidRequest) {
		t.Errorf("run with a terminal: %v; want invalid_request", err)
	}
}

// Suspend stops a sandbox and resume brings it back as it was. A suspended
// sandbox does no work, and a sandbox with a running process is not suspended
// out from under it.
func testSuspend(t *testing.T, e *env) {
	sb := e.newSandbox(t, api.CreateSandboxRequest{})
	ctx := ctxT(t)
	if !e.caps.Has(api.CapSuspend) {
		_, err := e.c.Suspend(ctx, sb.ID)
		wantCode(t, err, api.CodeUnsupported)
		return
	}
	if err := e.c.WriteFile(ctx, sb.ID, "/sandbox/home/kept.txt", []byte("before")); err != nil {
		t.Fatal(err)
	}
	p, err := e.c.StartProcess(ctx, sb.ID, api.RunRequest{Argv: []string{"sleep", "30"}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = e.c.Suspend(ctx, sb.ID)
	wantCode(t, err, api.CodeConflict)
	_ = e.c.Signal(ctx, sb.ID, p.PID, "KILL")
	e.follow(t, sb.ID, p.PID)

	got, err := e.c.Suspend(ctx, sb.ID)
	if err != nil || got.State != api.StateSuspended {
		t.Fatalf("suspend: %+v, %v", got, err)
	}
	_, err = e.c.Run(ctx, sb.ID, api.RunRequest{Argv: []string{"true"}})
	wantCode(t, err, api.CodeConflict)
	_, err = e.c.Suspend(ctx, sb.ID)
	wantCode(t, err, api.CodeConflict)

	got, err = e.c.Resume(ctx, sb.ID)
	if err != nil || got.State != api.StateRunning {
		t.Fatalf("resume: %+v, %v", got, err)
	}
	if data, err := e.c.ReadFile(ctx, sb.ID, "/sandbox/home/kept.txt"); err != nil || string(data) != "before" {
		t.Fatalf("after resume: %q, %v", data, err)
	}
	if res := e.run(t, sb.ID, api.RunRequest{Argv: []string{"echo", "awake"}}); string(res.Stdout) != "awake\n" {
		t.Fatalf("a command after resume printed %q", res.Stdout)
	}
	_, err = e.c.Resume(ctx, sb.ID)
	wantCode(t, err, api.CodeConflict)
}

// A snapshot captures a sandbox; sandboxes started from it begin where it was
// and diverge from there. The original keeps running.
func testSnapshot(t *testing.T, e *env) {
	sb := e.newSandbox(t, api.CreateSandboxRequest{Network: &api.NetworkPolicy{Mode: api.NetworkNone}})
	ctx := ctxT(t)
	memory, disk := e.caps.Has(api.CapMemorySnapshot), e.caps.Has(api.CapDiskSnapshot)
	if !memory && !disk {
		_, err := e.c.CreateSnapshot(ctx, sb.ID)
		wantCode(t, err, api.CodeUnsupported)
		_, err = e.c.CreateSandbox(ctx, api.CreateSandboxRequest{SnapshotID: "snp_0000000000000000"})
		wantCode(t, err, api.CodeUnsupported)
		return
	}
	if err := e.c.WriteFile(ctx, sb.ID, "/sandbox/home/state.txt", []byte("prepared")); err != nil {
		t.Fatal(err)
	}
	snap, err := e.c.CreateSnapshot(ctx, sb.ID)
	if err != nil {
		t.Fatalf("snapshot: %v", err)
	}
	t.Cleanup(func() { _ = e.c.DeleteSnapshot(context.Background(), snap.ID) })
	// The kind says what a fork gets back: whole where the endpoint captures
	// memory, the files only where it captures disks.
	if want := map[bool]string{true: api.SnapshotMemory, false: api.SnapshotDisk}[memory]; snap.Kind != want {
		t.Fatalf("snapshot kind %q; the endpoint's capabilities say %q", snap.Kind, want)
	}
	if res := e.run(t, sb.ID, api.RunRequest{Argv: []string{"echo", "still"}}); string(res.Stdout) != "still\n" {
		t.Fatalf("the original stopped working after a snapshot: %q", res.Stdout)
	}
	list, err := e.c.Snapshots(ctx)
	if err != nil || len(list) == 0 {
		t.Fatalf("snapshot list: %v, %v", list, err)
	}

	forks := make([]api.Sandbox, 2)
	for i := range forks {
		forks[i] = e.newSandbox(t, api.CreateSandboxRequest{SnapshotID: snap.ID, Network: &api.NetworkPolicy{Mode: api.NetworkNone}})
		if data, err := e.c.ReadFile(ctx, forks[i].ID, "/sandbox/home/state.txt"); err != nil || string(data) != "prepared" {
			t.Fatalf("fork %d: %q, %v", i, data, err)
		}
	}
	if err := e.c.WriteFile(ctx, forks[0].ID, "/sandbox/home/state.txt", []byte("changed in fork 0")); err != nil {
		t.Fatal(err)
	}
	for _, ref := range []string{forks[1].ID, sb.ID} {
		if data, _ := e.c.ReadFile(ctx, ref, "/sandbox/home/state.txt"); string(data) != "prepared" {
			t.Fatalf("a write in one fork reached %s: %q", ref, data)
		}
	}
	_, err = e.c.CreateSandbox(ctx, api.CreateSandboxRequest{SnapshotID: snap.ID, Image: "another:1"})
	wantCode(t, err, api.CodeInvalidRequest)
	if !memory {
		// A disk snapshot fixes the files, not the resources: nothing captured
		// was running with the original's, so a fork may ask for others.
		big := e.newSandbox(t, api.CreateSandboxRequest{SnapshotID: snap.ID, MemoryMB: sb.MemoryMB * 2, Network: &api.NetworkPolicy{Mode: api.NetworkNone}})
		if big.MemoryMB != sb.MemoryMB*2 {
			t.Fatalf("a fork of a disk snapshot asked for %d MiB and got %d", sb.MemoryMB*2, big.MemoryMB)
		}
		if data, err := e.c.ReadFile(ctx, big.ID, "/sandbox/home/state.txt"); err != nil || string(data) != "prepared" {
			t.Fatalf("the larger fork: %q, %v", data, err)
		}
	} else {
		// A memory snapshot is a running machine: its resources come with it.
		for _, mb := range []int{1, sb.MemoryMB * 2} {
			_, err = e.c.CreateSandbox(ctx, api.CreateSandboxRequest{SnapshotID: snap.ID, MemoryMB: mb})
			wantCode(t, err, api.CodeInvalidRequest)
		}
	}
	_, err = e.c.CreateSandbox(ctx, api.CreateSandboxRequest{SnapshotID: "snp_0000000000000000"})
	wantCode(t, err, api.CodeNotFound)
}

// A tunnel reaches a TCP port on the guest's own loopback, and only there.
func testTunnel(t *testing.T, e *env) {
	sb := e.newSandbox(t, api.CreateSandboxRequest{})
	ctx := ctxT(t)
	if !e.caps.Has(api.CapTunnel) {
		_, err := e.c.Tunnel(ctx, sb.ID, 8080)
		wantCode(t, err, api.CodeUnsupported)
		return
	}
	echo := `import socket
s = socket.socket(); s.setsockopt(socket.SOL_SOCKET, socket.SO_REUSEADDR, 1)
s.bind(("127.0.0.1", 8765)); s.listen(1)
c, _ = s.accept(); c.sendall(b"hello from the guest:" + c.recv(64)); c.close()`
	if res, err := e.c.Run(ctx, sb.ID, api.RunRequest{Argv: []string{"python3", "-c", "print(1)"}}); err != nil || res.ExitCode != 0 {
		t.Skip("the sandbox image has no python3 to listen with")
	}
	p, err := e.c.StartProcess(ctx, sb.ID, api.RunRequest{Argv: []string{"python3", "-c", echo}})
	if err != nil {
		t.Fatal(err)
	}
	var conn io.ReadWriteCloser
	for i := 0; i < 100; i++ { // the listener takes a moment to come up
		if conn, err = e.c.Tunnel(ctx, sb.ID, 8765); err == nil {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("tunnel: %v", err)
	}
	conn.Write([]byte("ping"))
	got, _ := io.ReadAll(conn)
	conn.Close()
	if string(got) != "hello from the guest:ping" {
		t.Fatalf("through the tunnel: %q", got)
	}
	e.follow(t, sb.ID, p.PID)
	if _, err := e.c.Tunnel(ctx, sb.ID, 0); !api.IsCode(err, api.CodeInvalidRequest) {
		t.Errorf("port 0: %v; want invalid_request", err)
	}
}

// Labels decide nothing about the sandbox; they come back with it, filter the
// listing, and are bounded and printable.
func testLabels(t *testing.T, e *env) {
	mark := uniqueName("mark")
	sb := e.newSandbox(t, api.CreateSandboxRequest{Labels: map[string]string{"suite.mark": mark, "agent": "none"}})
	if sb.Labels["suite.mark"] != mark {
		t.Fatalf("labels %v", sb.Labels)
	}
	_ = e.newSandbox(t, api.CreateSandboxRequest{Labels: map[string]string{"suite.mark": mark + "-other"}})
	list, err := e.c.Sandboxes(ctxT(t), "suite.mark="+mark)
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0].ID != sb.ID {
		t.Fatalf("filtered listing: %d sandboxes", len(list))
	}
	for _, bad := range []map[string]string{
		{"Upper": "x"}, {"-lead": "x"}, {"k": "esc\x1b[2J"}, {"k": strings.Repeat("v", 257)},
	} {
		_, err := e.c.CreateSandbox(ctxT(t), api.CreateSandboxRequest{Labels: bad})
		wantCode(t, err, api.CodeInvalidRequest)
	}
}

// The audit log says what a sandbox was asked to do and how it ended — with
// environment names, never values — and still answers by id once the sandbox
// is gone.
// A process runs as the image's unprivileged user. Root in the guest is not
// root on the host, but it is the guest's own kernel interface: mount, raw
// sockets, the drive's device nodes, and every defence a guest-side rule is
// meant to add.
func testNotRoot(t *testing.T, e *env) {
	sb := e.newSandbox(t, api.CreateSandboxRequest{})
	res, err := e.c.Run(ctxT(t), sb.ID, api.RunRequest{Argv: []string{"id", "-u"}})
	if err != nil || res.ExitCode != 0 {
		t.Skip("the sandbox image has no id")
	}
	if uid := strings.TrimSpace(string(res.Stdout)); uid == "0" || uid == "" {
		t.Errorf("a process runs as uid %q", uid)
	}
}

// One sandbox cannot connect to another: each is its own boundary, and two
// agents on one endpoint (two runs, two users) must not be a network to each
// other. The listener is checked from inside its own sandbox first, so a
// refusal from the peer means isolation and not a listener that never
// started.
func testPeerIsolation(t *testing.T, e *env) {
	a := e.newSandbox(t, api.CreateSandboxRequest{})
	b := e.newSandbox(t, api.CreateSandboxRequest{})
	py := func(sb, code string) (api.RunResult, error) {
		return e.c.Run(ctxT(t), sb, api.RunRequest{Argv: []string{"python3", "-c", code}, TimeoutSecs: 20})
	}
	if res, err := py(a.ID, "print(1)"); err != nil || res.ExitCode != 0 {
		t.Skip("the sandbox image has no python3")
	}
	res, err := py(a.ID, "import socket\ns=socket.socket(socket.AF_INET,socket.SOCK_DGRAM)\ns.connect(('192.0.2.1',9))\nprint(s.getsockname()[0])")
	addr := strings.TrimSpace(string(res.Stdout))
	if err != nil || res.ExitCode != 0 || addr == "" || strings.HasPrefix(addr, "127.") {
		t.Skip("the sandbox has no network interface (network none), so there is no peer to reach")
	}
	const listen = "import socket\ns=socket.socket()\ns.setsockopt(socket.SOL_SOCKET,socket.SO_REUSEADDR,1)\ns.bind(('0.0.0.0',8765))\ns.listen(8)\nwhile True:\n  c,_=s.accept()\n  c.close()"
	if _, err := e.c.StartProcess(ctxT(t), a.ID, api.RunRequest{Argv: []string{"python3", "-c", listen}}); err != nil {
		t.Fatal(err)
	}
	connect := "import socket,sys,time\nfor i in range(10):\n  try:\n    socket.create_connection(('" + addr + "',8765),timeout=2).close(); sys.exit(0)\n  except OSError:\n    time.sleep(0.3)\nsys.exit(1)"
	if res, err := py(a.ID, connect); err != nil || res.ExitCode != 0 {
		t.Fatalf("precondition: the listener in %s is not reachable from itself at %s", a.ID, addr)
	}
	if res, err := py(b.ID, connect); err == nil && res.ExitCode == 0 {
		t.Errorf("%s reached %s at %s:8765", b.ID, a.ID, addr)
	}
}

// A process is audited by its program, its argument count and a hash of its
// arguments. Their text is never kept: an agent's arguments are its prompt, a
// command line is where a token gets typed, and the log outlives the sandbox.
// The hash is reproducible from the arguments, so a known command can be
// matched against the log.
func testAuditArgs(t *testing.T, e *env) {
	if !e.caps.Has(api.CapAudit) {
		t.Skip("endpoint keeps no audit log (capability audit)")
	}
	sb := e.newSandbox(t, api.CreateSandboxRequest{})
	const pasted = "conformance-pasted-token-91c2"
	e.run(t, sb.ID, api.RunRequest{Argv: []string{"echo", "-n", pasted}})
	evs, err := e.c.Events(ctxT(t), sb.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(evs)
	if strings.Contains(string(raw), pasted) {
		t.Fatalf("an argument is in the audit log: %s", raw)
	}
	h := sha256.Sum256([]byte("-n\x00" + pasted + "\x00"))
	for _, ev := range evs.Events {
		if ev.Type == api.EventProcessStarted && ev.Program == "echo" {
			if ev.ArgCount != 2 || ev.ArgsSHA256 != hex.EncodeToString(h[:]) {
				t.Errorf("process.started %+v, want 2 args and sha256 %x", ev, h)
			}
			return
		}
	}
	t.Errorf("no process.started for echo in %s", raw)
}

func testAudit(t *testing.T, e *env) {
	if !e.caps.Has(api.CapAudit) {
		t.Skip("endpoint keeps no audit log (capability audit)")
	}
	const secret = "conformance-secret-value-7f3a"
	sb, err := e.c.CreateSandbox(ctxT(t), api.CreateSandboxRequest{
		Env: map[string]string{"SUITE_SECRET": secret}, Labels: map[string]string{"suite": "audit"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if res := e.run(t, sb.ID, api.RunRequest{Argv: []string{"false"}}); res.ExitCode != 1 {
		t.Fatalf("false exited %d", res.ExitCode)
	}
	if err := e.c.WriteFile(ctxT(t), sb.ID, "/tmp/audited.txt", []byte("12345")); err != nil {
		t.Fatal(err)
	}
	if err := e.c.TerminateSandbox(ctxT(t), sb.ID); err != nil {
		t.Fatal(err)
	}

	// The exit is recorded when the process is reaped, which can trail the
	// response to the run that waited for it.
	var list api.EventList
	deadline := time.Now().Add(10 * time.Second)
	for {
		list, err = e.c.Events(ctxT(t), sb.ID)
		if err != nil {
			t.Fatal(err)
		}
		if len(list.Events) > 0 && list.Events[len(list.Events)-1].Type == api.EventSandboxTerminated || time.Now().After(deadline) {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	raw, _ := json.Marshal(list)
	if strings.Contains(string(raw), secret) {
		t.Fatal("an environment value is in the audit log")
	}
	seen := map[string]api.Event{}
	for _, ev := range list.Events {
		if ev.Sandbox != sb.ID {
			t.Errorf("an event for %s in %s's log", ev.Sandbox, sb.ID)
		}
		seen[ev.Type] = ev
	}
	created, ok := seen[api.EventSandboxCreated]
	if !ok || created.Labels["suite"] != "audit" || !contains(created.EnvNames, "SUITE_SECRET") {
		t.Errorf("created: %+v", created)
	}
	if st := seen[api.EventProcessStarted]; st.Program != "false" || st.ArgCount != 0 || st.ArgsSHA256 != "" {
		t.Errorf("process.started: %+v", st)
	}
	if ex := seen[api.EventProcessExited]; ex.ExitCode == nil || *ex.ExitCode != 1 {
		t.Errorf("process.exited: %+v", ex)
	}
	if fw := seen[api.EventFileWritten]; fw.Path != "/tmp/audited.txt" || fw.Bytes != 5 {
		t.Errorf("file.written: %+v", fw)
	}
	if term := seen[api.EventSandboxTerminated]; term.Reason != "request" {
		t.Errorf("terminated: %+v", term)
	}
	_, err = e.c.Events(ctxT(t), "sbx_0000000000000000")
	wantCode(t, err, api.CodeNotFound)
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func (e *env) newVolume(t *testing.T) string {
	t.Helper()
	name := uniqueName("vol")
	if _, err := e.c.CreateVolume(ctxT(t), api.CreateVolumeRequest{Name: name, SizeMB: 64}); err != nil {
		t.Fatalf("create volume: %v", err)
	}
	t.Cleanup(func() { _ = e.c.DeleteVolume(context.Background(), name) })
	return name
}

// What one sandbox writes to a volume, the next one mounting it reads; while
// it is mounted writable, nobody else may mount it or delete it; read-only
// means it.
func testVolumes(t *testing.T, e *env) {
	if !e.caps.Has(api.CapVolumes) {
		_, err := e.c.CreateSandbox(ctxT(t), api.CreateSandboxRequest{Volumes: []api.VolumeMount{{Name: "x", Path: "/data"}}})
		wantCode(t, err, api.CodeUnsupported)
		t.Skip("endpoint has no volumes (capability volumes)")
	}
	name := e.newVolume(t)
	_, err := e.c.CreateVolume(ctxT(t), api.CreateVolumeRequest{Name: name})
	wantCode(t, err, api.CodeConflict)

	a := e.newSandbox(t, api.CreateSandboxRequest{Volumes: []api.VolumeMount{{Name: name, Path: "/data"}}})
	if len(a.Volumes) != 1 || a.Volumes[0].Path != "/data" {
		t.Fatalf("sandbox volumes %+v", a.Volumes)
	}
	if err := e.c.WriteFile(ctxT(t), a.ID, "/data/kept.txt", []byte("kept")); err != nil {
		t.Fatal(err)
	}
	_, err = e.c.CreateSandbox(ctxT(t), api.CreateSandboxRequest{Volumes: []api.VolumeMount{{Name: name, Path: "/data"}}})
	wantCode(t, err, api.CodeConflict)
	wantCode(t, e.c.DeleteVolume(ctxT(t), name), api.CodeConflict)
	vols, err := e.c.Volumes(ctxT(t))
	if err != nil {
		t.Fatal(err)
	}
	attached := ""
	for _, v := range vols {
		if v.Name == name {
			attached = v.AttachedTo
		}
	}
	if attached != a.ID {
		t.Errorf("attached_to = %q, want %s", attached, a.ID)
	}
	if e.caps.Has(api.CapMemorySnapshot) {
		_, err := e.c.CreateSnapshot(ctxT(t), a.ID)
		wantCode(t, err, api.CodeConflict)
	}
	if err := e.c.TerminateSandbox(ctxT(t), a.ID); err != nil {
		t.Fatal(err)
	}

	b := e.newSandbox(t, api.CreateSandboxRequest{Volumes: []api.VolumeMount{{Name: name, Path: "/srv/data", ReadOnly: true}}})
	got, err := e.c.ReadFile(ctxT(t), b.ID, "/srv/data/kept.txt")
	if err != nil || string(got) != "kept" {
		t.Fatalf("the next sandbox read %q, %v", got, err)
	}
	err = e.c.WriteFile(ctxT(t), b.ID, "/srv/data/new.txt", []byte("x"))
	wantCode(t, err, api.CodeConflict)
	if err := e.c.TerminateSandbox(ctxT(t), b.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.c.DeleteVolume(ctxT(t), name); err != nil {
		t.Fatalf("delete a detached volume: %v", err)
	}
	wantCode(t, e.c.DeleteVolume(ctxT(t), name), api.CodeNotFound)
}

// Readers share a volume: each one's drive is read-only to its kernel, so none
// can change what another reads. A writer has it alone, since a filesystem live
// in one kernel is half-written to another; and a volume being read cannot be
// written, or deleted, until the last reader is gone.
func testVolumeSharing(t *testing.T, e *env) {
	if !e.caps.Has(api.CapVolumes) {
		t.Skip("endpoint has no volumes (capability volumes)")
	}
	name := e.newVolume(t)
	w := e.newSandbox(t, api.CreateSandboxRequest{Volumes: []api.VolumeMount{{Name: name, Path: "/data"}}})
	if err := e.c.WriteFile(ctxT(t), w.ID, "/data/shared.txt", []byte("shared")); err != nil {
		t.Fatal(err)
	}
	_, err := e.c.CreateSandbox(ctxT(t), api.CreateSandboxRequest{Volumes: []api.VolumeMount{{Name: name, Path: "/data", ReadOnly: true}}})
	wantCode(t, err, api.CodeConflict) // a reader while it is written
	if err := e.c.TerminateSandbox(ctxT(t), w.ID); err != nil {
		t.Fatal(err)
	}

	ro := []api.VolumeMount{{Name: name, Path: "/data", ReadOnly: true}}
	a := e.newSandbox(t, api.CreateSandboxRequest{Volumes: ro})
	b := e.newSandbox(t, api.CreateSandboxRequest{Volumes: ro})
	for _, sb := range []api.Sandbox{a, b} {
		got, err := e.c.ReadFile(ctxT(t), sb.ID, "/data/shared.txt")
		if err != nil || string(got) != "shared" {
			t.Errorf("reader %s read %q, %v", sb.ID, got, err)
		}
	}
	_, err = e.c.CreateSandbox(ctxT(t), api.CreateSandboxRequest{Volumes: []api.VolumeMount{{Name: name, Path: "/data"}}})
	wantCode(t, err, api.CodeConflict) // a writer while it is read
	if err := e.c.TerminateSandbox(ctxT(t), a.ID); err != nil {
		t.Fatal(err)
	}
	wantCode(t, e.c.DeleteVolume(ctxT(t), name), api.CodeConflict) // b still reads it
	if err := e.c.TerminateSandbox(ctxT(t), b.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.c.DeleteVolume(ctxT(t), name); err != nil {
		t.Fatalf("delete once the last reader is gone: %v", err)
	}
}

// Mount paths stay out of the system's directories, are absolute and plain,
// and do not nest; an unknown volume is not found.
func testVolumeRefusals(t *testing.T, e *env) {
	if !e.caps.Has(api.CapVolumes) {
		t.Skip("endpoint has no volumes (capability volumes)")
	}
	name := e.newVolume(t)
	other := e.newVolume(t)
	for _, mounts := range [][]api.VolumeMount{
		{{Name: name, Path: "/usr/local"}},
		{{Name: name, Path: "/tmp/cache"}},
		{{Name: name, Path: "/proc/x"}},
		{{Name: name, Path: "/etc"}},
		{{Name: name, Path: "data"}},
		{{Name: name, Path: "/data/../etc"}},
		{{Name: name, Path: "/"}},
		{{Name: name, Path: "/data"}, {Name: other, Path: "/data/inner"}},
		{{Name: name, Path: "/a"}, {Name: name, Path: "/b"}},
		{{Name: "Bad_Name", Path: "/data"}},
	} {
		_, err := e.c.CreateSandbox(ctxT(t), api.CreateSandboxRequest{Volumes: mounts})
		wantCode(t, err, api.CodeInvalidRequest)
	}
	_, err := e.c.CreateSandbox(ctxT(t), api.CreateSandboxRequest{Volumes: []api.VolumeMount{{Name: uniqueName("none"), Path: "/data"}}})
	wantCode(t, err, api.CodeNotFound)
	_, err = e.c.CreateVolume(ctxT(t), api.CreateVolumeRequest{Name: "Bad_Name"})
	wantCode(t, err, api.CodeInvalidRequest)
}

// A snapshot schedule is bounded by limits the endpoint publishes, refused
// where it cannot be kept, and — where the endpoint allows an interval short
// enough to wait for — takes snapshots and keeps the newest it was told to.
func testSnapshotSchedule(t *testing.T, e *env) {
	ctx := ctxT(t)
	l := e.caps.Limits
	if !e.caps.Has(api.CapMemorySnapshot) && !e.caps.Has(api.CapDiskSnapshot) {
		_, err := e.c.CreateSandbox(ctx, api.CreateSandboxRequest{SnapshotEverySecs: max(l.MinSnapshotEverySecs, 1)})
		wantCode(t, err, api.CodeUnsupported)
		return
	}
	if l.MinSnapshotEverySecs < 1 || l.MaxSnapshotKeep < 1 {
		t.Fatalf("limits %+v: a snapshot schedule needs min_snapshot_every_secs and max_snapshot_keep", l)
	}
	bad := []api.CreateSandboxRequest{
		{SnapshotEverySecs: l.MinSnapshotEverySecs, SnapshotKeep: l.MaxSnapshotKeep + 1},
		{SnapshotKeep: 1},
	}
	if l.MinSnapshotEverySecs > 1 { // one second under a minimum of 1 is 0: no schedule, which is allowed
		bad = append(bad, api.CreateSandboxRequest{SnapshotEverySecs: l.MinSnapshotEverySecs - 1})
	}
	for _, req := range bad {
		_, err := e.c.CreateSandbox(ctx, req)
		wantCode(t, err, api.CodeInvalidRequest)
	}
	if l.MinSnapshotEverySecs > 3 {
		t.Logf("min_snapshot_every_secs is %d: the schedule's timing is not waited for here", l.MinSnapshotEverySecs)
		return
	}
	every := l.MinSnapshotEverySecs
	sb := e.newSandbox(t, api.CreateSandboxRequest{SnapshotEverySecs: every, SnapshotKeep: 1, Network: &api.NetworkPolicy{Mode: api.NetworkNone}})
	if sb.SnapshotEverySecs != every || sb.SnapshotKeep != 1 {
		t.Fatalf("the sandbox says its schedule is %d/%d", sb.SnapshotEverySecs, sb.SnapshotKeep)
	}
	t.Cleanup(func() {
		list, _ := e.c.Snapshots(context.Background())
		for _, s := range list {
			if s.Sandbox == sb.ID {
				_ = e.c.DeleteSnapshot(context.Background(), s.ID)
			}
		}
	})
	// Three intervals and some: at least two scheduled snapshots were taken,
	// and one is kept.
	time.Sleep(time.Duration(3*every)*time.Second + 1500*time.Millisecond)
	list, err := e.c.Snapshots(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var mine []api.Snapshot
	for _, s := range list {
		if s.Sandbox == sb.ID && s.Scheduled {
			mine = append(mine, s)
		}
	}
	if len(mine) != 1 {
		t.Fatalf("%d scheduled snapshots after three intervals with keep 1: %v", len(mine), mine)
	}
	if out, err := e.c.SetSnapshotSchedule(ctx, sb.ID, api.SnapshotSchedule{}); err != nil || out.SnapshotEverySecs != 0 {
		t.Fatalf("stopping the schedule: %+v, %v", out, err)
	}
}

// Where the endpoint measures usage, a running sandbox gets samples within a
// few intervals, each sane: a CPU share of 0 to 100, memory within its limit,
// in time order. Where it does not, it says so.
func testMetrics(t *testing.T, e *env) {
	ctx := ctxT(t)
	sb := e.newSandbox(t, api.CreateSandboxRequest{Network: &api.NetworkPolicy{Mode: api.NetworkNone}})
	if !e.caps.Has(api.CapMetrics) {
		_, err := e.c.Metrics(ctx, sb.ID)
		wantCode(t, err, api.CodeUnsupported)
		return
	}
	var m api.MetricsList
	deadline := time.Now().Add(30 * time.Second)
	for {
		var err error
		if m, err = e.c.Metrics(ctx, sb.ID); err != nil {
			t.Fatal(err)
		}
		if m.IntervalSecs < 1 {
			t.Fatalf("interval_secs %d", m.IntervalSecs)
		}
		if len(m.Samples) >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(time.Duration(m.IntervalSecs) * time.Second / 2)
	}
	if len(m.Samples) < 2 {
		t.Fatalf("%d samples in 30 s at an interval of %d s", len(m.Samples), m.IntervalSecs)
	}
	for i, s := range m.Samples {
		if s.CPUPercent < 0 || s.CPUPercent > 100 || s.MemoryBytes < 0 || (s.MemoryLimitBytes > 0 && s.MemoryBytes > s.MemoryLimitBytes) {
			t.Fatalf("sample %d is not sane: %+v", i, s)
		}
		if i > 0 && !s.Time.After(m.Samples[i-1].Time) {
			t.Fatalf("samples out of order at %d", i)
		}
	}
	if _, err := e.c.Metrics(ctx, "sbx_0000000000000000"); err == nil {
		t.Fatal("metrics of a sandbox that does not exist")
	}
}

package sandbox

import (
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/config"
)

// TestMain pins the host gid off for the whole package, and that is a statement
// about BuildSpec rather than a convenience: its output must depend on the
// configuration it is handed and not on the machine running the test. Without
// this, six existing assertions about the container user passed on macOS and
// failed on Linux — the same split that let the bug this file fixes ship.
// Tests that are *about* the shared group set it themselves, via pinHostGID.
func TestMain(m *testing.M) {
	hostPrimaryGID = func() string { return "" }
	os.Exit(m.Run())
}

// pinHostGID makes the per-machine input deterministic, the same way the
// timezone tests pin hostTimezone.
func pinHostGID(t *testing.T, gid string) {
	t.Helper()
	prev := hostPrimaryGID
	hostPrimaryGID = func() string { return gid }
	t.Cleanup(func() { hostPrimaryGID = prev })
}

// TestSharedGroupUser covers the whole decision table, because every row of it
// is a machine somebody runs on.
func TestSharedGroupUser(t *testing.T) {
	tests := []struct {
		name   string
		engine string
		user   string
		gid    string
		want   string
	}{
		{"linux docker: the sandbox user joins the host group", "", "sandbox", "1000", "1001:1000"},
		{"engine named explicitly is still docker", "docker", "sandbox", "1000", "1001:1000"},
		{"off linux there is nothing to solve", "", "sandbox", "", "sandbox"},
		{"podman already maps ids with keep-id", "podman", "sandbox", "1000", "sandbox"},
		{"an explicit user is the caller's decision", "", "root", "1000", "root"},
		{"an explicit uid:gid is left alone", "", "1000:1000", "1000", "1000:1000"},
		{"a host gid equal to the image's own changes nothing", "", "sandbox", "1001", "sandbox"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pinHostGID(t, tc.gid)
			if got := sharedGroupUser(tc.engine, tc.user); got != tc.want {
				t.Errorf("sharedGroupUser(%q, %q) with gid %q = %q, want %q",
					tc.engine, tc.user, tc.gid, got, tc.want)
			}
		})
	}
}

// TestBuildSpec_SharedGroupReachesBothUserFields is the half that was easy to
// get wrong: in allowlist mode the container starts as root and the entrypoint
// drops to SANDBOX_RUN_AS, so a fix applied only to --user would hold for
// exactly the runs that do not use the default network mode.
func TestBuildSpec_SharedGroupReachesBothUserFields(t *testing.T) {
	pinHostGID(t, "1000")

	cfg := config.Default()
	cfg.Network = config.NetworkSpec{Mode: "default"}
	spec, err := BuildSpec(cfg, Options{Project: t.TempDir(), Command: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	if spec.User != "1001:1000" {
		t.Errorf("User = %q, want 1001:1000 — the container cannot reach its own HOME without the host group", spec.User)
	}

	cfg = config.Default() // allowlist is the built-in default
	spec, err = BuildSpec(cfg, Options{Project: t.TempDir(), Command: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	if spec.User != "root" {
		t.Fatalf("User = %q, want root: the firewall entrypoint programs iptables before it drops", spec.User)
	}
	if got := spec.Env["SANDBOX_RUN_AS"]; got != "1001:1000" {
		t.Errorf("SANDBOX_RUN_AS = %q, want 1001:1000 — this is the user the run actually ends up as", got)
	}
}

// TestBuildSpec_UnchangedWhereIdsDoNotMeet keeps the golden --dry-run output and
// every other platform honest: this is a Linux answer to a Linux problem, and
// it must render nothing anywhere else.
func TestBuildSpec_UnchangedWhereIdsDoNotMeet(t *testing.T) {
	pinHostGID(t, "")

	cfg := config.Default()
	cfg.Network = config.NetworkSpec{Mode: "default"}
	spec, err := BuildSpec(cfg, Options{Project: t.TempDir(), Command: []string{"true"}})
	if err != nil {
		t.Fatal(err)
	}
	if spec.User != "sandbox" {
		t.Errorf("User = %q, want sandbox unchanged", spec.User)
	}
}

// TestResolveHostPrimaryGID pins the two answers that are not a gid: off Linux
// the ids never meet, and root's group is a widening rather than a fix.
func TestResolveHostPrimaryGID(t *testing.T) {
	got := resolveHostPrimaryGID()
	if runtime.GOOS != "linux" {
		if got != "" {
			t.Errorf("resolveHostPrimaryGID() = %q on %s, want empty", got, runtime.GOOS)
		}
		return
	}
	if os.Getgid() == 0 && got != "" {
		t.Errorf("resolveHostPrimaryGID() = %q as gid 0, want empty", got)
	}
}

// TestShareWithSandboxGroup checks what the container needs from a directory the
// host created at 0700: group read, write and search on the directory itself,
// group read/write on the files directly inside, and setgid so what the agent
// writes next stays readable from the host.
func TestShareWithSandboxGroup(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("bind-mount ownership is virtualized off Linux; nothing to share")
	}
	pinHostGID(t, mustGID(t))

	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	file := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(file, []byte("{}\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sub := filepath.Join(dir, "sub")
	if err := os.Mkdir(sub, 0o700); err != nil {
		t.Fatal(err)
	}

	ShareWithSandboxGroup(dir)

	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm()&0o070 != 0o070 {
		t.Errorf("directory mode = %v, want group rwx", fi.Mode().Perm())
	}
	if fi.Mode()&os.ModeSetgid == 0 {
		t.Error("directory is not setgid: files the agent writes would leave the shared group")
	}
	if fi, err = os.Stat(file); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm()&0o060 != 0o060 {
		t.Errorf("file mode = %v, want group rw — resuming a host session means appending to it", fi.Mode().Perm())
	}
	if fi, err = os.Stat(sub); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm()&0o070 != 0o070 {
		t.Errorf("subdirectory mode = %v, want group rwx", fi.Mode().Perm())
	}
}

// TestShareWithSandboxGroupIsShallowAndSafe pins the two properties that keep it
// cheap and keep it honest: it does not walk into a tree the container already
// owns, and it never follows a symlink out of the directory it was given.
func TestShareWithSandboxGroupIsShallowAndSafe(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("no-op off Linux")
	}
	pinHostGID(t, mustGID(t))

	dir := t.TempDir()
	deep := filepath.Join(dir, "node_modules", "pkg")
	if err := os.MkdirAll(deep, 0o700); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(dir, "link")); err != nil {
		t.Fatal(err)
	}

	ShareWithSandboxGroup(dir)

	if fi, err := os.Stat(deep); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm()&0o070 != 0 {
		t.Errorf("descended into %s (mode %v): the container owns what it created, and this runs on every launch", deep, fi.Mode().Perm())
	}
	if fi, err := os.Stat(outside); err != nil {
		t.Fatal(err)
	} else if fi.Mode().Perm() != 0o600 {
		t.Errorf("a symlink redirected the chmod to %s (mode %v)", outside, fi.Mode().Perm())
	}
}

// TestShareWithSandboxGroupNoOps covers the inputs that must do nothing at all
// rather than fail: no directory, and a host where the ids never meet.
func TestShareWithSandboxGroupNoOps(t *testing.T) {
	pinHostGID(t, "")
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	ShareWithSandboxGroup(dir)
	ShareWithSandboxGroup("")
	fi, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o700 {
		t.Errorf("mode = %v, want 0700 untouched where there is no host group to share with", fi.Mode().Perm())
	}
}

// mustGID returns the caller's own primary gid — the only group the test
// process is certainly a member of, and so the only one it can chgrp to.
func mustGID(t *testing.T) string {
	t.Helper()
	gid := os.Getgid()
	if gid <= 0 {
		t.Skip("running as gid 0; joining the root group is not the fix")
	}
	return strconv.Itoa(gid)
}

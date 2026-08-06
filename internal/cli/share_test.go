package cli

import (
	"os"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/config"
)

// wantShareMount is the mount entry --share is expected to append, spelled out
// here rather than taken from shareMount — a test that asked the code under test
// what it should produce would agree with it whatever it did.
func wantShareMount(t *testing.T) string {
	t.Helper()
	dir := config.SharedDir()
	if dir == "" {
		t.Fatal("config.SharedDir() returned empty")
	}
	return dir + ":" + sharedTarget + ":rw"
}

// TestShareMountsSharedDir proves the flag does the one thing it exists for:
// append a read-write bind of the sandbox-owned shared dir at /shared, and
// create that dir on the host so docker doesn't invent a root-owned one.
func TestShareMountsSharedDir(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Chdir(t.TempDir()) // outside any repo: see renderDryRun

	_, opts, err := newSession(&runFlags{share: true})
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}

	want := wantShareMount(t)
	if !containsStr(opts.ExtraMounts, want) {
		t.Errorf("ExtraMounts = %#v, want to contain %q", opts.ExtraMounts, want)
	}

	dir := filepath.Join(root, "sandbox", "shared")
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		t.Fatalf("shared dir %s not created: %v", dir, err)
	}
	if got := fi.Mode().Perm(); got != 0o700 {
		t.Errorf("shared dir mode = %o, want 700 (owner-only, like the auth persist dir)", got)
	}
}

// TestShareOffByDefault guards the isolation posture: a cross-project channel
// must never appear unless it was asked for.
func TestShareOffByDefault(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Chdir(t.TempDir()) // outside any repo: see renderDryRun

	_, opts, err := newSession(&runFlags{})
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}
	for _, m := range opts.ExtraMounts {
		if strings.Contains(m, sharedTarget) {
			t.Errorf("shared mount %q present without --share", m)
		}
	}
	if _, err := os.Stat(filepath.Join(root, "sandbox", "shared")); err == nil {
		t.Error("shared dir created without --share")
	}
}

// TestShareSeedsReadme covers the discoverability half of the feature: the mount
// alone tells an agent nothing, so the directory explains itself.
func TestShareSeedsReadme(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Chdir(t.TempDir()) // outside any repo: see renderDryRun

	if _, _, err := newSession(&runFlags{share: true}); err != nil {
		t.Fatalf("newSession: %v", err)
	}
	readme := filepath.Join(root, "sandbox", "shared", "README.md")
	b, err := os.ReadFile(readme)
	if err != nil {
		t.Fatalf("reading seeded README: %v", err)
	}
	if !strings.Contains(string(b), sharedTarget) {
		t.Errorf("seeded README does not mention %s:\n%s", sharedTarget, b)
	}
}

// TestShareDoesNotClobberReadme: the seed is a first-run nicety, not something
// that overwrites a user's own notes on every launch.
func TestShareDoesNotClobberReadme(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Chdir(t.TempDir()) // outside any repo: see renderDryRun

	dir := filepath.Join(root, "sandbox", "shared")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	readme := filepath.Join(dir, "README.md")
	const mine = "my own notes"
	if err := os.WriteFile(readme, []byte(mine), 0o600); err != nil {
		t.Fatal(err)
	}

	if _, _, err := newSession(&runFlags{share: true}); err != nil {
		t.Fatalf("newSession: %v", err)
	}
	b, err := os.ReadFile(readme)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != mine {
		t.Errorf("README overwritten: got %q, want %q", b, mine)
	}
}

// TestShareIsASandboxFlagInWrappers pins --share as sandbox's own flag in the
// agent wrappers: it must be consumed before the boundary, never forwarded to
// the agent (which would reject it).
func TestShareIsASandboxFlagInWrappers(t *testing.T) {
	cmd := newClaudeCmd()
	gotFlags, gotGuest, _ := splitWrapperArgs(cmd, []string{"--share", "--dangerously-skip-permissions"})
	if want := []string{"--share"}; !reflect.DeepEqual(gotFlags, want) {
		t.Errorf("flags = %#v, want %#v", gotFlags, want)
	}
	if want := []string{"--dangerously-skip-permissions"}; !reflect.DeepEqual(gotGuest, want) {
		t.Errorf("guest = %#v, want %#v", gotGuest, want)
	}
}

func containsStr(list []string, want string) bool {
	for _, s := range list {
		if s == want {
			return true
		}
	}
	return false
}

// #31: on native Linux under docker, bind-mount ownership is real, so a shared
// directory left at 0700 and owned by the host user is unopenable by the
// container — `ls /shared` was EACCES, let alone writing a handoff file. macOS
// virtualizes ownership and rootless podman maps the host user onto the container
// user, which is why this survived: both of those cases work, and they were the
// only ones anyone could test.
//
// This runs where the bug lives, and CI is Linux, so the wiring is checked rather
// than argued about. The *mechanism* is pinned by TestShareWithSandboxGroup in
// internal/sandbox; what this adds is that shareMount actually applies it.
func TestShareMountOpensTheSharedDirToTheContainerGroup(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("bind-mount ownership is only real on native Linux")
	}
	if os.Getgid() == 0 {
		t.Skip("running as root: sandbox-cli declines to share the root group, by design")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if _, err := shareMount(""); err != nil {
		t.Fatalf("shareMount: %v", err)
	}

	root := config.SharedDir()
	fi, err := os.Stat(root)
	if err != nil {
		t.Fatalf("stat %s: %v", root, err)
	}
	// Group needs all three: read to list, write to hand a file over, and search
	// to open anything inside it.
	if perm := fi.Mode().Perm(); perm&0o070 != 0o070 {
		t.Errorf("mode = %v, want group rwx so the container can use it", perm)
	}
	// setgid is what keeps it working: entries the container creates inherit the
	// group, so the host can still read what the agent wrote.
	if fi.Mode()&os.ModeSetgid == 0 {
		t.Errorf("mode = %v, want the setgid bit", fi.Mode())
	}

	// The seeded README has to be readable too, and this is the assertion that
	// matters most: an earlier version of this fix shared the directory *before*
	// writing the README, leaving it 0600 — unreadable by the agent it exists to
	// inform, on the one run that creates it. A directory-only check passed that
	// happily. The file is what proves the ordering.
	rfi, err := os.Stat(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatalf("stat seeded README: %v", err)
	}
	if perm := rfi.Mode().Perm(); perm&0o040 == 0 {
		t.Errorf("README mode = %v, want group read — the agent cannot read the file explaining the mount", perm)
	}
}

// A namespace gets the same treatment, and needs it separately: the root's pass
// reaches its direct entries, but the namespace is created after that pass runs.
func TestShareMountOpensANamespaceToo(t *testing.T) {
	if goruntime.GOOS != "linux" {
		t.Skip("bind-mount ownership is only real on native Linux")
	}
	if os.Getgid() == 0 {
		t.Skip("running as root")
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())

	if _, err := shareMount("work"); err != nil {
		t.Fatalf("shareMount: %v", err)
	}

	fi, err := os.Stat(filepath.Join(config.SharedDir(), "work"))
	if err != nil {
		t.Fatalf("stat namespace: %v", err)
	}
	if perm := fi.Mode().Perm(); perm&0o070 != 0o070 {
		t.Errorf("namespace mode = %v, want group rwx", perm)
	}
	if fi.Mode()&os.ModeSetgid == 0 {
		t.Errorf("namespace mode = %v, want the setgid bit", fi.Mode())
	}
}

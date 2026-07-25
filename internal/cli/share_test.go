package cli

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/config"
)

// shareMount is the mount entry --share is expected to append.
func shareMount(t *testing.T) string {
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

	_, opts, err := newSession(&runFlags{share: true})
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}

	want := shareMount(t)
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

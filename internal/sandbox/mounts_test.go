package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveWorkspace_ValidDir(t *testing.T) {
	dir := t.TempDir()
	got, err := ResolveWorkspace(dir)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	real, _ := filepath.EvalSymlinks(dir)
	if got != real {
		t.Errorf("got %q, want %q", got, real)
	}
}

func TestResolveWorkspace_RefusesRoot(t *testing.T) {
	if _, err := ResolveWorkspace("/"); err == nil {
		t.Fatal("expected refusal for filesystem root")
	}
}

func TestResolveWorkspace_RefusesHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	if _, err := ResolveWorkspace(home); err == nil {
		t.Fatalf("expected refusal for home directory %q", home)
	}
}

func TestResolveWorkspace_RefusesHomeAncestor(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home dir")
	}
	parent := filepath.Dir(home)
	if parent == home {
		t.Skip("home has no parent")
	}
	if _, err := ResolveWorkspace(parent); err == nil {
		t.Fatalf("expected refusal for home ancestor %q", parent)
	}
}

func TestResolveWorkspace_NonexistentPath(t *testing.T) {
	if _, err := ResolveWorkspace("/definitely/not/a/real/path/xyz123"); err == nil {
		t.Fatal("expected error for nonexistent path")
	}
}

func TestResolveWorkspace_FileNotDir(t *testing.T) {
	dir := t.TempDir()
	f := filepath.Join(dir, "afile")
	if err := os.WriteFile(f, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := ResolveWorkspace(f); err == nil {
		t.Fatal("expected error for a file path")
	}
}

func TestIsAncestor(t *testing.T) {
	cases := []struct {
		anc, child string
		want       bool
	}{
		{"/Users", "/Users/amit", true},
		{"/Users/amit", "/Users/amit/proj", true},
		{"/Users/amit", "/Users/amit", false},
		{"/Users/amit/proj", "/Users/amit", false},
		{"/a/b", "/a/bc", false},
	}
	for _, c := range cases {
		if got := isAncestor(c.anc, c.child); got != c.want {
			t.Errorf("isAncestor(%q, %q) = %v, want %v", c.anc, c.child, got, c.want)
		}
	}
}

// TestResolveWorkspace_HomeRefusalIgnoresCasing pins the fix for a real bypass.
// The refusal used to be a string compare on a path EvalSymlinks had resolved —
// but EvalSymlinks preserves the caller's casing, and macOS APFS (like Windows
// NTFS) is case-insensitive. So `--project /Users/AmitGhadge` was a different
// string from `/Users/amitghadge`, sailed past the check, and mounted the home
// directory; `--project /USERS` was not the filesystem root and not string-equal
// to anything, so it bypassed the ancestor check too and mounted every user's
// home at once.
//
// Comparing identity (device+inode) instead has neither failure mode, and also
// covers the unicode-normalisation version of the same bug.
func TestResolveWorkspace_HomeRefusalIgnoresCasing(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory")
	}
	// Flip the case of the final path element — /Users/amit -> /Users/Amit —
	// which is the shape the bypass actually took. Only meaningful where the
	// filesystem is case-insensitive; on a case-sensitive one the variant simply
	// does not exist, and the os.Stat below skips it.
	base := filepath.Base(home)
	if base == "" || base == string(filepath.Separator) {
		t.Skip("home path has no basename to vary")
	}
	variant := filepath.Join(filepath.Dir(home), strings.ToUpper(base[:1])+base[1:])
	if variant == home {
		variant = filepath.Join(filepath.Dir(home), strings.ToLower(base[:1])+base[1:])
	}
	upper := strings.ToUpper(home)
	for _, p := range []string{home, variant, upper} {
		if _, err := os.Stat(p); err != nil {
			continue // this filesystem is case-sensitive; nothing to bypass
		}
		if _, err := ResolveWorkspace(p); err == nil {
			t.Errorf("ResolveWorkspace(%q) was allowed; it is the home directory under another name", p)
		}
	}
	// And the ancestor check, which /USERS defeated.
	parent := filepath.Dir(home)
	for _, p := range []string{parent, strings.ToUpper(parent)} {
		if _, err := os.Stat(p); err != nil {
			continue
		}
		if _, err := ResolveWorkspace(p); err == nil {
			t.Errorf("ResolveWorkspace(%q) was allowed; it is an ancestor of the home directory", p)
		}
	}
}

// TestRefuseUnsafeHostPath covers the exported guard directly, since it now
// protects the worktree .git mount as well as the workspace.
func TestRefuseUnsafeHostPath(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		t.Skip("no home directory")
	}
	for _, p := range []string{"/", home, filepath.Dir(home)} {
		if err := RefuseUnsafeHostPath(p); err == nil {
			t.Errorf("RefuseUnsafeHostPath(%q) = nil, want a refusal", p)
		}
	}
	// An ordinary directory below the home is fine — the guard must not be so
	// broad that nothing can be mounted.
	ok := t.TempDir()
	if err := RefuseUnsafeHostPath(ok); err != nil {
		t.Errorf("RefuseUnsafeHostPath(%q) = %v, want nil", ok, err)
	}
}

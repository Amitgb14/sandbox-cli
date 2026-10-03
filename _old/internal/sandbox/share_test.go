package sandbox

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// These follow the code they test. The path-safety half of --share moved into
// this package when the Studio daemon needed the same resolution the CLI uses,
// and a refusal about symlinks is worth exactly as much as the test that proves
// it still refuses. What stayed in internal/cli is the flag wiring: which
// spellings parse, and what a dry-run renders.

// TestShareNameValidation pins the allowlist: one path segment, letters,
// digits, dot, underscore and hyphen, starting with a letter or digit.
func TestShareNameValidation(t *testing.T) {
	longName := strings.Repeat("a", 64)
	tooLongName := strings.Repeat("a", 65)

	accepted := []string{"work", "a", "A1", "team.one", "x_y-z", longName}
	rejected := []string{
		"", ".", "..", "...", "../x", "../../.ssh", "../agents/claude",
		"/etc", "a/b", `a\b`, "a:b", "a,b", "-lead", ".hidden", "a b", tooLongName,
	}

	for _, name := range accepted {
		if err := ValidateShareName(name); err != nil {
			t.Errorf("ValidateShareName(%q) = %v, want nil", name, err)
		}
	}
	for _, name := range rejected {
		if err := ValidateShareName(name); err == nil {
			t.Errorf("ValidateShareName(%q) = nil, want an error", name)
		}
	}
}

// TestShareNamespaceDirCreatesLeaf covers the happy path: a namespace
// directory is created under root, mode 0700, and creating it again (a
// second run reusing the same namespace) succeeds.
func TestShareNamespaceDirCreatesLeaf(t *testing.T) {
	root := t.TempDir()

	hostDir, target, err := ShareNamespaceDir(root, "work")
	if err != nil {
		t.Fatalf("ShareNamespaceDir: %v", err)
	}
	// The mount source is the RESOLVED path: docker resolves it again at run
	// time, so handing it the unresolved string meant checking one path and
	// mounting another. realpath here rather than filepath.Join(root, ...)
	// because t.TempDir() hands back /var/... on macOS, which is /private/var.
	if want := filepath.Join(realpath(t, root), "work"); hostDir != want {
		t.Errorf("hostDir = %q, want %q", hostDir, want)
	}
	if target != "/shared/work" {
		t.Errorf("target = %q, want /shared/work", target)
	}
	fi, err := os.Stat(hostDir)
	if err != nil || !fi.IsDir() {
		t.Fatalf("namespace dir %s not created: %v", hostDir, err)
	}
	// 0700 on every platform: this calls ShareNamespaceDir directly, and the
	// group-opening pass belongs to shareMount (see wantSharedPerm).
	if got := fi.Mode().Perm(); got != 0o700 {
		t.Errorf("namespace dir mode = %o, want 700", got)
	}

	// Idempotent: calling it again for the same namespace must succeed.
	if _, _, err := ShareNamespaceDir(root, "work"); err != nil {
		t.Errorf("second ShareNamespaceDir(root, \"work\") = %v, want nil", err)
	}
}

// TestShareNamespaceDirRefusesTraversal asserts that a namespace name which
// would step outside root is refused, and nothing is created outside root.
func TestShareNamespaceDirRefusesTraversal(t *testing.T) {
	for _, name := range []string{"..", "../x", "/etc", "a/b"} {
		root := t.TempDir()
		if _, _, err := ShareNamespaceDir(root, name); err == nil {
			t.Errorf("ShareNamespaceDir(root, %q) = nil error, want a refusal", name)
		}
		outside := filepath.Join(root, "..", "x")
		if _, err := os.Stat(outside); err == nil {
			t.Errorf("ShareNamespaceDir(root, %q) created something outside root: %s", name, outside)
		}
	}
}

// TestShareNamespaceDirRefusesSymlink pins the defect that shipped past a green
// suite: the original test planted an *absolute* symlink, which os.Root.MkdirAll
// refuses on its own, so it exercised the one variant that was already safe and
// asserted nothing. The dangerous variants are RELATIVE links that stay inside
// root — os.Root accepts those, and `ln -s .` then resolved to the shared root
// itself, handing back every namespace as the mount source.
//
// The container has this directory read-write whenever any run uses a bare
// --share, so each of these is something an agent can actually plant.
func TestShareNamespaceDirRefusesSymlink(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target string
		why    string
	}{
		{"dot", ".", "resolves to the shared root: mounts every namespace"},
		{"sibling", "other", "resolves to another namespace"},
		{"nested", "./a/../b", "relative, stays inside, still not the namespace"},
		{"absolute", "", "resolves outside root"}, // target filled in below
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.MkdirAll(filepath.Join(root, "other"), 0o700); err != nil {
				t.Fatal(err)
			}
			target := tc.target
			if target == "" {
				target = t.TempDir()
			}
			if err := os.Symlink(target, filepath.Join(root, "work")); err != nil {
				t.Fatal(err)
			}

			hostDir, _, err := ShareNamespaceDir(root, "work")
			if err == nil {
				t.Fatalf("ShareNamespaceDir(root, \"work\") accepted a symlink to %q (%s); returned mount source %s",
					target, tc.why, hostDir)
			}
		})
	}
}

// TestShareNamespaceDirRefusesNonDirectory: a namespace whose name collides with
// a regular file must refuse rather than hand docker a file as a mount source.
func TestShareNamespaceDirRefusesNonDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "work"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ShareNamespaceDir(root, "work"); err == nil {
		t.Error("ShareNamespaceDir accepted a regular file as a namespace, want a refusal")
	}
}

// TestSeedReadmeDoesNotFollowSymlink pins the second half of the same class as
// the symlink refusal above: the seeder used os.Stat (which follows symlinks)
// to decide "already there?", then os.WriteFile (which follows them too). A
// DANGLING link therefore failed the check and was then followed on write,
// creating a host file outside the shared directory at a path the container
// chose. O_EXCL is what closes it, so the test plants exactly that link.
func TestSeedReadmeDoesNotFollowSymlink(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "victim")

	if err := os.Symlink(outside, filepath.Join(dir, "README.md")); err != nil {
		t.Fatal(err)
	}

	SeedSharedReadme(dir)
	if _, err := os.Stat(outside); err == nil {
		t.Errorf("SeedSharedReadme followed a dangling symlink and created %s", outside)
	}

	SeedShareNamespaceReadme(dir, "work", "/shared/work")
	if _, err := os.Stat(outside); err == nil {
		t.Errorf("SeedShareNamespaceReadme followed a dangling symlink and created %s", outside)
	}
}

// TestSeedReadmeStillSeedsAndDoesNotClobber is the positive twin of the test
// above: a refusal test that passes because the function does nothing at all
// would be indistinguishable from a correct one.
func TestSeedReadmeStillSeedsAndDoesNotClobber(t *testing.T) {
	dir := t.TempDir()

	SeedSharedReadme(dir)
	b, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatalf("SeedSharedReadme wrote nothing: %v", err)
	}
	if !strings.Contains(string(b), "Shared sandbox directory") {
		t.Errorf("seeded README has unexpected content: %q", string(b)[:min(60, len(b))])
	}

	SeedSharedReadme(dir) // second call must not clobber
	b2, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil || string(b2) != string(b) {
		t.Error("SeedSharedReadme clobbered an existing README")
	}
}

// realpath resolves symlinks the way ShareNamespaceDir does, so the comparisons
// above hold on macOS where t.TempDir() hands back a /var -> /private/var path.
func realpath(t *testing.T, p string) string {
	t.Helper()
	real, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return real
}

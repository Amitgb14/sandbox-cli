package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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
		if err := validateShareName(name); err != nil {
			t.Errorf("validateShareName(%q) = %v, want nil", name, err)
		}
	}
	for _, name := range rejected {
		if err := validateShareName(name); err == nil {
			t.Errorf("validateShareName(%q) = nil, want an error", name)
		}
	}
}

// TestShareNamespaceDirCreatesLeaf covers the happy path: a namespace
// directory is created under root, mode 0700, and creating it again (a
// second run reusing the same namespace) succeeds.
func TestShareNamespaceDirCreatesLeaf(t *testing.T) {
	root := t.TempDir()

	hostDir, target, err := shareNamespaceDir(root, "work")
	if err != nil {
		t.Fatalf("shareNamespaceDir: %v", err)
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
	// 0700 on every platform: this calls shareNamespaceDir directly, and the
	// group-opening pass belongs to shareMount (see wantSharedPerm).
	if got := fi.Mode().Perm(); got != 0o700 {
		t.Errorf("namespace dir mode = %o, want 700", got)
	}

	// Idempotent: calling it again for the same namespace must succeed.
	if _, _, err := shareNamespaceDir(root, "work"); err != nil {
		t.Errorf("second shareNamespaceDir(root, \"work\") = %v, want nil", err)
	}
}

// TestShareNamespaceDirRefusesTraversal asserts that a namespace name which
// would step outside root is refused, and nothing is created outside root.
func TestShareNamespaceDirRefusesTraversal(t *testing.T) {
	for _, name := range []string{"..", "../x", "/etc", "a/b"} {
		root := t.TempDir()
		if _, _, err := shareNamespaceDir(root, name); err == nil {
			t.Errorf("shareNamespaceDir(root, %q) = nil error, want a refusal", name)
		}
		outside := filepath.Join(root, "..", "x")
		if _, err := os.Stat(outside); err == nil {
			t.Errorf("shareNamespaceDir(root, %q) created something outside root: %s", name, outside)
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

			hostDir, _, err := shareNamespaceDir(root, "work")
			if err == nil {
				t.Fatalf("shareNamespaceDir(root, \"work\") accepted a symlink to %q (%s); returned mount source %s",
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
	if _, _, err := shareNamespaceDir(root, "work"); err == nil {
		t.Error("shareNamespaceDir accepted a regular file as a namespace, want a refusal")
	}
}

// TestShareNamespaceMountsLeafOnly proves --share=NAME mounts only the leaf
// directory at /shared/NAME, not the shared root at /shared.
func TestShareNamespaceMountsLeafOnly(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Chdir(t.TempDir()) // outside any repo: see renderDryRun

	_, opts, err := newSession(&runFlags{share: true, shareName: "work"})
	if err != nil {
		t.Fatalf("newSession: %v", err)
	}

	want := filepath.Join(realpath(t, root), "sandbox", "shared", "work") + ":/shared/work:rw"
	if !containsStr(opts.ExtraMounts, want) {
		t.Errorf("ExtraMounts = %#v, want to contain %q", opts.ExtraMounts, want)
	}
	bareRoot := filepath.Join(root, "sandbox", "shared") + ":/shared:rw"
	if containsStr(opts.ExtraMounts, bareRoot) {
		t.Errorf("ExtraMounts = %#v, must not contain the bare-root entry %q", opts.ExtraMounts, bareRoot)
	}

	dir := filepath.Join(root, "sandbox", "shared", "work")
	fi, err := os.Stat(dir)
	if err != nil || !fi.IsDir() {
		t.Fatalf("namespace dir %s not created: %v", dir, err)
	}
	if want := wantSharedPerm(); fi.Mode().Perm() != want {
		t.Errorf("namespace dir mode = %o, want %o", fi.Mode().Perm(), want)
	}
	assertNotWorldAccessible(t, fi, "the namespace dir")
}

// TestShareNamespaceSeedsItsOwnReadme covers discoverability for a namespace:
// it gets its own explainer naming its own container path, not the shared
// root's.
func TestShareNamespaceSeedsItsOwnReadme(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Chdir(t.TempDir()) // outside any repo: see renderDryRun

	if _, _, err := newSession(&runFlags{share: true, shareName: "work"}); err != nil {
		t.Fatalf("newSession: %v", err)
	}
	readme := filepath.Join(root, "sandbox", "shared", "work", "README.md")
	b, err := os.ReadFile(readme)
	if err != nil {
		t.Fatalf("reading seeded README: %v", err)
	}
	if !strings.Contains(string(b), "/shared/work") {
		t.Errorf("seeded README does not mention /shared/work:\n%s", b)
	}
}

// TestShareNamespaceRefusedByNewSession proves a namespace that could escape
// the shared directory is refused rather than silently sanitized, and that
// refusal happens before anything is created.
func TestShareNamespaceRefusedByNewSession(t *testing.T) {
	root := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", root)
	t.Chdir(t.TempDir()) // outside any repo: see renderDryRun

	_, _, err := newSession(&runFlags{share: true, shareName: "../../.ssh"})
	if err == nil {
		t.Fatal("newSession: want error for a namespace that escapes the shared dir, got nil")
	}
	if !strings.Contains(err.Error(), "--share") {
		t.Errorf("error = %q, want it to mention --share", err.Error())
	}

	if _, statErr := os.Stat(filepath.Join(root, ".ssh")); statErr == nil {
		t.Error(".ssh directory was created next to the config root")
	}
}

// TestShareFlagsParse pins the replacement for the old optional-value --share.
// --share is a plain bool again and --share-name is a plain string, so every
// spelling either consumes its value or is a parse error -- there is no shape
// that quietly means something else.
func TestShareFlagsParse(t *testing.T) {
	for _, c := range []struct {
		in        []string
		wantShare string
		wantName  string
	}{
		{[]string{"--share"}, "true", ""},
		{[]string{}, "false", ""},
		{[]string{"--share", "--share-name", "work"}, "true", "work"}, // space form
		{[]string{"--share", "--share-name=work"}, "true", "work"},    // equals form
		{[]string{"--share=false"}, "false", ""},
	} {
		cmd := newRunCmd()
		if err := cmd.Flags().Parse(c.in); err != nil {
			t.Fatalf("Parse(%v): %v", c.in, err)
		}
		if got := cmd.Flags().Lookup("share").Value.String(); got != c.wantShare {
			t.Errorf("Parse(%v): share = %q, want %q", c.in, got, c.wantShare)
		}
		if got := cmd.Flags().Lookup("share-name").Value.String(); got != c.wantName {
			t.Errorf("Parse(%v): share-name = %q, want %q", c.in, got, c.wantName)
		}
	}
}

// TestShareNameConsumesItsValue is the regression test for the reason this flag
// exists. Under the optional-value --share, `--share work --project=/x` left
// --share bare (mounting the whole shared root rather than the leaf) and
// forwarded BOTH later tokens to the guest -- so --project was silently dropped
// and the failure was toward more access. A StringVar has no NoOptDefVal, so the
// space form consumes its value and later flags still parse as flags.
func TestShareNameConsumesItsValue(t *testing.T) {
	cmd := newRunCmd()
	if err := cmd.Flags().Parse([]string{"--share", "--share-name", "work", "--project=/x"}); err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if got := cmd.Flags().Lookup("share-name").Value.String(); got != "work" {
		t.Errorf("share-name = %q, want %q", got, "work")
	}
	if got := cmd.Flags().Lookup("project").Value.String(); got != "/x" {
		t.Errorf("project = %q, want %q -- the flag after --share-name was swallowed", got, "/x")
	}
	if rest := cmd.Flags().Args(); len(rest) != 0 {
		t.Errorf("leftover args = %#v, want none", rest)
	}
}

// TestShareBoolSpellingsAreBoolsAgain: --share is a pflag.BoolVar once more, so
// every ParseBool spelling means on/off and nothing else can be mistaken for a
// namespace. --share=no is a parse error, as it was before the flag ever took a
// value -- which is the fail-open regression this design removes by construction
// rather than by a reserved-word list.
func TestShareBoolSpellingsAreBoolsAgain(t *testing.T) {
	for _, s := range []string{"--share=0", "--share=FALSE", "--share=f"} {
		cmd := newRunCmd()
		if err := cmd.Flags().Parse([]string{s}); err != nil {
			t.Fatalf("Parse(%q): %v", s, err)
		}
		if got := cmd.Flags().Lookup("share").Value.String(); got != "false" {
			t.Errorf("Parse(%q): share = %q, want false", s, got)
		}
	}
	for _, s := range []string{"--share=no", "--share=off", "--share=yes", "--share=work"} {
		if err := newRunCmd().Flags().Parse([]string{s}); err == nil {
			t.Errorf("Parse(%q) = nil, want a parse error (it is a bool flag)", s)
		}
	}
}

// TestShareNameRejectsUnsafeName proves the CLI refuses an escaping namespace at
// the point of use rather than accepting it and failing deeper in.
func TestShareNameRejectsUnsafeName(t *testing.T) {
	for _, name := range []string{"../../.ssh", "/etc", "a/b", ""} {
		rf := &runFlags{share: true, shareName: name}
		if name == "" {
			continue // empty means "no namespace", not an invalid one
		}
		if _, _, err := newSession(rf); err == nil {
			t.Errorf("newSession(--share-name %q) = nil error, want a refusal", name)
		}
	}
}

// TestShareNameNeedsShare: naming a namespace without asking to share is refused
// rather than implied. Implying it would make one flag switch on the
// cross-project channel, and would leave --share=false --share-name X as a
// contradiction resolvable only by guessing.
func TestShareNameNeedsShare(t *testing.T) {
	if _, _, err := newSession(&runFlags{shareName: "work"}); err == nil {
		t.Error("newSession(--share-name work, no --share) = nil error, want a refusal")
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

	seedSharedReadme(dir)
	if _, err := os.Stat(outside); err == nil {
		t.Errorf("seedSharedReadme followed a dangling symlink and created %s", outside)
	}

	seedShareNamespaceReadme(dir, "work", "/shared/work")
	if _, err := os.Stat(outside); err == nil {
		t.Errorf("seedShareNamespaceReadme followed a dangling symlink and created %s", outside)
	}
}

// TestSeedReadmeStillSeedsAndDoesNotClobber is the positive twin of the test
// above: a refusal test that passes because the function does nothing at all
// would be indistinguishable from a correct one.
func TestSeedReadmeStillSeedsAndDoesNotClobber(t *testing.T) {
	dir := t.TempDir()

	seedSharedReadme(dir)
	b, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil {
		t.Fatalf("seedSharedReadme wrote nothing: %v", err)
	}
	if !strings.Contains(string(b), "Shared sandbox directory") {
		t.Errorf("seeded README has unexpected content: %q", string(b)[:min(60, len(b))])
	}

	seedSharedReadme(dir) // second call must not clobber
	b2, err := os.ReadFile(filepath.Join(dir, "README.md"))
	if err != nil || string(b2) != string(b) {
		t.Error("seedSharedReadme clobbered an existing README")
	}
}

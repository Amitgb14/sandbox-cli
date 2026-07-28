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
	if want := filepath.Join(root, "work"); hostDir != want {
		t.Errorf("hostDir = %q, want %q", hostDir, want)
	}
	if target != "/shared/work" {
		t.Errorf("target = %q, want /shared/work", target)
	}
	fi, err := os.Stat(hostDir)
	if err != nil || !fi.IsDir() {
		t.Fatalf("namespace dir %s not created: %v", hostDir, err)
	}
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

	want := filepath.Join(root, "sandbox", "shared", "work") + ":/shared/work:rw"
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
	if got := fi.Mode().Perm(); got != 0o700 {
		t.Errorf("namespace dir mode = %o, want 700", got)
	}
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

// TestShareFlagOptionalValue pins the three shapes --share must parse: a bare
// flag (NoOptDefVal), --flag=NAME, and absent. NoOptDefVal being "true" is
// what makes the value optional at all, and is also what stops the agent
// wrappers' splitter (run.go:91) from swallowing the token that follows.
func TestShareFlagOptionalValue(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{[]string{"--share"}, "true"},
		{[]string{"--share=work"}, "work"},
		{[]string{}, "false"},
	}
	for _, c := range cases {
		cmd := newRunCmd()
		if err := cmd.Flags().Parse(c.in); err != nil {
			t.Fatalf("Parse(%v): %v", c.in, err)
		}
		fl := cmd.Flags().Lookup("share")
		if got := fl.Value.String(); got != c.want {
			t.Errorf("Parse(%v): share value = %q, want %q", c.in, got, c.want)
		}
		if fl.NoOptDefVal != "true" {
			t.Errorf("share.NoOptDefVal = %q, want %q", fl.NoOptDefVal, "true")
		}
	}
}

// TestShareFlagAcceptsAllBoolSpellings proves --share still recognizes every
// spelling strconv.ParseBool accepts, not just the literal "true"/"false"
// pflag's own BoolVar used to parse. Scripts written against the old
// BoolVar-backed --share pass values like --share=0 or --share=FALSE
// expecting sharing to turn off; those must still disable sharing rather than
// being treated as a namespace name (which would silently turn it on).
func TestShareFlagAcceptsAllBoolSpellings(t *testing.T) {
	off := []string{"0", "f", "F", "false", "FALSE", "False"}
	on := []string{"1", "t", "T", "true", "TRUE", "True"}

	for _, s := range off {
		rf := &runFlags{}
		if err := (&shareValue{rf: rf}).Set(s); err != nil {
			t.Errorf("Set(%q) = %v, want nil", s, err)
			continue
		}
		if rf.share {
			t.Errorf("Set(%q): share = true, want false", s)
		}
		if rf.shareName != "" {
			t.Errorf("Set(%q): shareName = %q, want empty", s, rf.shareName)
		}
	}

	for _, s := range on {
		rf := &runFlags{}
		if err := (&shareValue{rf: rf}).Set(s); err != nil {
			t.Errorf("Set(%q) = %v, want nil", s, err)
			continue
		}
		if !rf.share {
			t.Errorf("Set(%q): share = false, want true", s)
		}
		if rf.shareName != "" {
			t.Errorf("Set(%q): shareName = %q, want empty (no namespace)", s, rf.shareName)
		}
	}

	// End to end through the real flag, matching how a script actually invokes
	// the CLI: --share=0 must land the flag's own String() at "false".
	for _, s := range []string{"--share=0", "--share=FALSE"} {
		cmd := newRunCmd()
		if err := cmd.Flags().Parse([]string{s}); err != nil {
			t.Fatalf("Parse(%v): %v", s, err)
		}
		if got := cmd.Flags().Lookup("share").Value.String(); got != "false" {
			t.Errorf("Parse(%q): share value = %q, want %q", s, got, "false")
		}
	}
}

// TestShareFlagRejectsUnsafeName proves the CLI parse step itself refuses a
// namespace that would escape the shared directory, rather than accepting it
// and failing later inside newSession.
func TestShareFlagRejectsUnsafeName(t *testing.T) {
	if err := newRunCmd().Flags().Parse([]string{"--share=../../.ssh"}); err == nil {
		t.Error("Parse(--share=../../.ssh) = nil, want an error")
	}
	// An empty value must be an error, not a silent no-op: --share= is not the
	// same request as a bare --share, and validateShareName already refuses "".
	if err := newRunCmd().Flags().Parse([]string{"--share="}); err == nil {
		t.Error("Parse(--share=) = nil, want an error")
	}
}

// TestShareFlagHelpIsUnchangedShape guards the pflag rendering choice: with
// Type() reporting "bool", pflag renders a plain --share with no "[=...]"
// value hint, matching the old BoolVar's help line. It inspects only the flag
// column (the token right after "--"), not the description text, which
// legitimately mentions "--share=NAME" in prose.
func TestShareFlagHelpIsUnchangedShape(t *testing.T) {
	usage := newRunCmd().Flags().FlagUsages()
	var flagCol string
	found := false
	for _, line := range strings.Split(usage, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "--share") {
			continue
		}
		found = true
		fields := strings.Fields(trimmed)
		flagCol = fields[0]
		break
	}
	if !found {
		t.Fatalf("FlagUsages() has no --share line:\n%s", usage)
	}
	if flagCol != "--share" {
		t.Errorf("--share flag column = %q, want %q (a value hint changed the rendered shape)", flagCol, "--share")
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

// TestShareValueRefusesOffSpellings pins the fail-open regression: --share=no
// was a hard parse error under the old pflag.BoolVar, and once --share took an
// optional value "no" became a namespace name with sharing switched ON. An
// operator disabling the cross-project channel would have silently enabled it.
func TestShareValueRefusesOffSpellings(t *testing.T) {
	for _, s := range []string{"no", "No", "NO", "n", "off", "OFF", "yes", "y", "on"} {
		rf := &runFlags{}
		v := &shareValue{rf: rf}
		if err := v.Set(s); err == nil {
			t.Errorf("--share=%s accepted: share=%v shareName=%q, want a refusal", s, rf.share, rf.shareName)
		}
	}
}

// TestShareValueBoolSpellingsStillWork: the ParseBool spellings must keep
// meaning on and off, since scripts in the wild already pass them.
func TestShareValueBoolSpellingsStillWork(t *testing.T) {
	for _, tc := range []struct {
		in   string
		want bool
	}{{"true", true}, {"TRUE", true}, {"1", true}, {"t", true},
		{"false", false}, {"FALSE", false}, {"0", false}, {"f", false}} {
		rf := &runFlags{}
		if err := (&shareValue{rf: rf}).Set(tc.in); err != nil {
			t.Errorf("--share=%s = %v, want nil", tc.in, err)
			continue
		}
		if rf.share != tc.want || rf.shareName != "" {
			t.Errorf("--share=%s -> share=%v name=%q, want share=%v name=\"\"", tc.in, rf.share, rf.shareName, tc.want)
		}
	}
}

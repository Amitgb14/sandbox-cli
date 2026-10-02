package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

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

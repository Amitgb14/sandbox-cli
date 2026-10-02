package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// A read-only listing must not fail on a project config it does not use.
//
// This is a regression test for a fix that overshot. A review finding said `serve`
// should not record `profile: dev` for a repository whose config will not load —
// correct, and it writes. Resolving the profile for *every* caller then made
// `pane list` refuse in any repository with a restricted `.sandbox.yaml`, while
// `sandbox-cli list` answered the same question about the same containers without
// complaint. Strictness in the wrong place reads as a broken command.
func TestListingNeedsNoProfileButServingDoes(t *testing.T) {
	repo := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(repo); err == nil {
		repo = resolved
	}
	// A project config the trust boundary refuses — `env:` can hijack the
	// interpreter, so it is on the unconditional list.
	if err := os.WriteFile(filepath.Join(repo, ".sandbox.yaml"),
		[]byte("env:\n  PATH: /workspace/.bin\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(repo); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.Chdir(wd) })

	// The reader: no profile asked for, so nothing to fail on.
	if _, err := openSession("", "docker", ""); err != nil {
		// A non-git directory fails for a different and legitimate reason; what must
		// not appear is the config refusal.
		if strings.Contains(err.Error(), "may not") {
			t.Errorf("a listing refused because of a project config it does not use: %v", err)
		}
	}

	// The writer: refuses, and says why it cares.
	_, err = servingProfile("")
	if err == nil {
		t.Fatal("serve accepted a config it could not load; it records the profile")
	}
	if !strings.Contains(err.Error(), "records which profile is in force") {
		t.Errorf("the refusal does not say why serve cares: %v", err)
	}
}

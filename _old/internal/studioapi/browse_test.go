package studioapi

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

// The folder picker is the widest read in this API — it is the only one that
// leaves a repository — so what it refuses to say is the part worth pinning.
func TestBrowseListsDirectoriesOnlyAndHidesDotDirectories(t *testing.T) {
	s, _ := newTestServer(t)
	root := t.TempDir()

	repo := filepath.Join(root, "myrepo")
	if err := os.MkdirAll(filepath.Join(repo, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, d := range []string{"plain", ".ssh", ".config"} {
		if err := os.Mkdir(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// The thing this must never reveal: a file, by name.
	if err := os.WriteFile(filepath.Join(root, "tax-2025.pdf"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".ssh", "id_ed25519"), []byte("secret"), 0o600); err != nil {
		t.Fatal(err)
	}

	var got BrowseResponse
	rec := doJSON(t, s, http.MethodGet, "/v1/browse?path="+root, nil, &got)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/browse = %d: %s", rec.Code, rec.Body.String())
	}

	names := map[string]BrowseEntry{}
	for _, e := range got.Entries {
		names[e.Name] = e
	}
	if _, ok := names["tax-2025.pdf"]; ok {
		t.Error("a file was listed; this is a directory picker, and listing files makes it a way to find out what someone has")
	}
	if _, ok := names[".ssh"]; ok {
		t.Error(".ssh was enumerated; dot-directories are never listed, which is most of what keeps this from being a tour of a home directory")
	}
	if _, ok := names[".config"]; ok {
		t.Error("a dot-directory was enumerated")
	}
	if _, ok := names["plain"]; !ok {
		t.Error("an ordinary directory was not listed, so the picker cannot be navigated")
	}
	if e := names["myrepo"]; !e.Repo {
		t.Error("a directory holding .git was not marked as a repository, so the picker cannot point at what is worth adding")
	}
	if e := names["plain"]; e.Repo {
		t.Error("a directory with no .git was marked as a repository")
	}
	// Names and a path, and nothing else — the response has nowhere to put a
	// size or a modification time, and that is the point.
	if got.Parent == "" {
		t.Error("no parent reported, so the picker cannot go up")
	}
}

// A repository already registered is marked, so the picker can say so instead of
// letting somebody add it twice and hunt for a row that will never appear.
func TestBrowseMarksAlreadyRegisteredRepositories(t *testing.T) {
	s, _ := newTestServer(t)
	parent := t.TempDir()
	repo := filepath.Join(parent, "known")
	if err := os.Mkdir(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	initGitRepo(t, repo)
	rec, err := validateProjectPath(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Projects.add(rec); err != nil {
		t.Fatal(err)
	}

	var got BrowseResponse
	doJSON(t, s, http.MethodGet, "/v1/browse?path="+parent, nil, &got)
	for _, e := range got.Entries {
		if e.Name == "known" && !e.Registered {
			t.Error("an already-registered repository was not marked, so the picker offers adding it again")
		}
	}
}

func TestBrowseRefusesRelativePathsAndMissingDirectories(t *testing.T) {
	s, _ := newTestServer(t)
	if rec := doJSON(t, s, http.MethodGet, "/v1/browse?path=code/thing", nil, nil); rec.Code != http.StatusBadRequest {
		t.Errorf("a relative path = %d, want 400: the daemon has no working directory to read it against", rec.Code)
	}
	if rec := doJSON(t, s, http.MethodGet, "/v1/browse?path=/definitely/not/here", nil, nil); rec.Code != http.StatusNotFound {
		t.Errorf("a missing directory = %d, want 404", rec.Code)
	}
}

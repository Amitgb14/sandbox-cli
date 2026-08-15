package studioapi

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

// The registry's whole reason to exist is that POST /projects is the only place
// a host path is accepted, so this is where every refusal about a path lives.
// A case that stops being refused here stops being refused everywhere.
func TestValidateProjectPathRefusals(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo)
	plain := t.TempDir() // a real directory, no git in it

	cases := []struct {
		name string
		path string
		want string // substring of the refusal, so the message stays readable
	}{
		{"empty", "", "path is required"},
		{"relative", "some/repo", "not an absolute path"},
		{"missing", filepath.Join(repo, "nope"), "no such directory"},
		{"a file, not a directory", writeFile(t, repo, "README.md"), "not a directory"},
		{"not a repository", plain, "not a git repository"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateProjectPath(tc.path)
			if err == nil {
				t.Fatalf("validateProjectPath(%q) was accepted; every path this daemon will touch has to be refused here or nowhere", tc.path)
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("refusal for %q = %q, want it to mention %q", tc.path, err, tc.want)
			}
		})
	}
}

// A path is recorded as the repository *root*, whichever directory inside it was
// named. Studio addresses work by branch and a branch belongs to a repository,
// so a registered subdirectory would answer every branch-addressed request for a
// repository the user never named.
func TestValidateProjectPathResolvesToTheRepositoryRoot(t *testing.T) {
	repo := t.TempDir()
	initGitRepo(t, repo)
	sub := filepath.Join(repo, "internal", "deep")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}

	rec, err := validateProjectPath(sub)
	if err != nil {
		t.Fatalf("registering a subdirectory of a repository: %v", err)
	}
	realRepo, err := filepath.EvalSymlinks(repo)
	if err != nil {
		t.Fatal(err)
	}
	if rec.Root != realRepo {
		t.Errorf("recorded root = %q, want the repository root %q", rec.Root, realRepo)
	}
	if rec.ID == "" {
		t.Error("recorded no repo id; the id is what containers are labelled with, so a project without one can never be matched to its runs")
	}
	if rec.Name != filepath.Base(realRepo) {
		t.Errorf("recorded name = %q, want %q", rec.Name, filepath.Base(realRepo))
	}
}

// The store is a file, and the point of a file is that it is still there next
// time. Adding the same repository twice is one row, not two — otherwise every
// re-add of a path grows the list a client renders.
func TestProjectStorePersistsAndDeduplicates(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects.json")
	st := loadProjectStore(path)

	rec := projectRecord{Root: "/tmp/one", ID: "one-1234abcd", Name: "one"}
	if err := st.add(rec); err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := st.add(rec); err != nil {
		t.Fatalf("re-add: %v", err)
	}
	if got := len(st.records()); got != 1 {
		t.Errorf("adding the same repository twice recorded %d rows, want 1", got)
	}

	if got := len(loadProjectStore(path).records()); got != 1 {
		t.Errorf("reloaded store has %d rows, want 1: the registry has to survive a restart or the UI forgets every repository on it", got)
	}

	removed, err := st.remove("one-1234abcd")
	if err != nil || !removed {
		t.Fatalf("remove: removed=%v err=%v", removed, err)
	}
	if got := len(loadProjectStore(path).records()); got != 0 {
		t.Errorf("reloaded store has %d rows after a remove, want 0", got)
	}
}

// A registry that cannot be parsed is empty, not fatal. It is a convenience
// list; a daemon that would not answer because of it is harder to recover than
// the file is to rewrite.
func TestUnreadableRegistryLoadsEmpty(t *testing.T) {
	path := filepath.Join(t.TempDir(), "projects.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := len(loadProjectStore(path).records()); got != 0 {
		t.Errorf("records from a corrupt registry = %d, want 0", got)
	}
}

// scopeFor is the rule the registry buys: an id resolves, an unregistered id
// refuses, and a path is never a repository this daemon will accept from a
// request — not even a real one sitting right next to a registered repo.
func TestScopeForResolvesIdsAndRefusesEverythingElse(t *testing.T) {
	s, _ := newTestServer(t)
	repo := t.TempDir()
	initGitRepo(t, repo)
	rec, err := validateProjectPath(repo)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Projects.add(rec); err != nil {
		t.Fatal(err)
	}

	t.Run("empty is the daemon's own project", func(t *testing.T) {
		sc, err := s.scopeFor("")
		if err != nil {
			t.Fatalf("empty repo id: %v", err)
		}
		if sc.Project != s.Project || sc.RepoID != s.RepoID {
			t.Errorf("scope = %+v, want the default %s/%s — every request written before repositories were plural has to keep meaning what it meant", sc, s.Project, s.RepoID)
		}
	})

	t.Run("a registered id resolves", func(t *testing.T) {
		sc, err := s.scopeFor(rec.ID)
		if err != nil {
			t.Fatalf("registered id %q: %v", rec.ID, err)
		}
		if sc.Project != rec.Root {
			t.Errorf("scope project = %q, want %q", sc.Project, rec.Root)
		}
		if sc.RepoID != rec.ID {
			t.Errorf("scope repo id = %q, want %q — the id is what container labels are filtered on", sc.RepoID, rec.ID)
		}
	})

	t.Run("an unregistered id refuses", func(t *testing.T) {
		if _, err := s.scopeFor("never-registered-0000abcd"); err == nil {
			t.Error("an unregistered repo id resolved; the registry is only worth having if an id nobody added is refused")
		}
	})

	t.Run("a host path is not an id", func(t *testing.T) {
		other := t.TempDir()
		initGitRepo(t, other)
		if _, err := s.scopeFor(other); err == nil {
			t.Errorf("a host path (%q) resolved as a repository; requests name repositories by id, never by path", other)
		}
	})
}

// A registered repository that has gone away is listed and refused, not dropped
// and not silently answered for. Dropping it loses a row the user asked for;
// answering for it would read whatever is at that path now.
func TestMissingRepositoryIsListedAndRefused(t *testing.T) {
	s, _ := newTestServer(t)
	gone := filepath.Join(t.TempDir(), "was-here")
	if err := s.Projects.add(projectRecord{Root: gone, ID: "was-here-1234abcd", Name: "was-here"}); err != nil {
		t.Fatal(err)
	}

	var found *Project
	for _, p := range s.projects() {
		if p.ID == "was-here-1234abcd" {
			cp := p
			found = &cp
		}
	}
	if found == nil {
		t.Fatal("a repository whose directory is gone was dropped from the listing; an absent checkout is not the same as one nobody asked for")
	}
	if !found.Missing {
		t.Error("a repository whose directory is gone was not marked missing, so a client would offer it as usable")
	}
	if _, err := s.scopeFor("was-here-1234abcd"); err == nil {
		t.Error("a missing repository resolved to a scope; git would be asked about a directory that is not there")
	}
}

// The default project is what -project named. It is always listed — a registry
// that was emptied still leaves Studio able to describe where it is standing.
func TestDefaultProjectIsAlwaysListedAndCannotBeRemoved(t *testing.T) {
	s, _ := newTestServer(t)

	var projects ProjectsResponse
	rec := doJSON(t, s, http.MethodGet, "/v1/projects", nil, &projects)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/projects = %d, want 200", rec.Code)
	}
	if len(projects.Projects) != 1 {
		t.Fatalf("listed %d projects, want just the default", len(projects.Projects))
	}
	def := projects.Projects[0]
	if !def.Default {
		t.Error("the daemon's own project is not marked default; a client cannot tell which one every unqualified request is about")
	}
	if def.ID != s.RepoID || def.Root != s.Project {
		t.Errorf("default project = %+v, want id %q root %q", def, s.RepoID, s.Project)
	}

	rec = doJSON(t, s, http.MethodDelete, "/v1/projects/"+s.RepoID, nil, nil)
	if rec.Code != http.StatusConflict {
		t.Errorf("DELETE of the default project = %d, want 409: it would be back on the next listing, which is a worse answer than saying so", rec.Code)
	}
}

// End to end over HTTP: add a repository, see it listed, and see a repo-scoped
// read reach it — which is the whole feature, and the part a unit test of the
// store cannot show.
func TestAddedRepositoryIsListedAndScopesReads(t *testing.T) {
	s, _ := newTestServer(t)
	repo := t.TempDir()
	initGitRepo(t, repo)

	var added Project
	rec := doJSON(t, s, http.MethodPost, "/v1/projects", ProjectCreateRequest{Path: repo}, &added)
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/projects = %d, want 201; body %s", rec.Code, rec.Body.String())
	}
	if added.ID == "" || added.Missing {
		t.Fatalf("added project = %+v, want a usable one with an id", added)
	}

	var projects ProjectsResponse
	doJSON(t, s, http.MethodGet, "/v1/projects", nil, &projects)
	if len(projects.Projects) != 2 {
		t.Fatalf("listed %d projects after adding one, want 2 (the default and the new one)", len(projects.Projects))
	}

	// The listing reaches the new repository rather than the daemon's own.
	var wts WorktreesResponse
	rec = doJSON(t, s, http.MethodGet, "/v1/worktrees?repo="+added.ID, nil, &wts)
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /v1/worktrees?repo= = %d, want 200; body %s", rec.Code, rec.Body.String())
	}
	for _, wt := range wts.Worktrees {
		if wt.RepoID != added.ID {
			t.Errorf("worktree %q reported repo %q, want %q — a row stamped with the daemon's repo id would file it under a repository it does not belong to", wt.Branch, wt.RepoID, added.ID)
		}
	}

	// And an id nobody registered is refused rather than answered for the default.
	rec = doJSON(t, s, http.MethodGet, "/v1/worktrees?repo=nope-0000abcd", nil, nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("GET /v1/worktrees?repo=<unregistered> = %d, want 404", rec.Code)
	}

	// Removing it is forgetting, never deleting: the checkout stays on disk.
	rec = doJSON(t, s, http.MethodDelete, "/v1/projects/"+added.ID, nil, nil)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("DELETE /v1/projects/{id} = %d, want 204", rec.Code)
	}
	if _, err := os.Stat(repo); err != nil {
		t.Errorf("the repository directory is gone after removing it from the registry: %v", err)
	}
}

// A run names its repository by id too, and an id nobody registered must not
// quietly become the daemon's own project — that would start an agent in a
// repository the request did not name.
func TestCreateRunRefusesAnUnregisteredRepo(t *testing.T) {
	s, _ := newTestServer(t)
	rec := doJSON(t, s, http.MethodPost, "/v1/runs", RunCreateRequest{
		Repo: "nope-0000abcd", Agent: "claude", Prompt: "hello",
	}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("POST /v1/runs with an unregistered repo = %d, want 400; body %s", rec.Code, rec.Body.String())
	}
	rec = doJSON(t, s, http.MethodPost, "/v1/runs", RunCreateRequest{
		Repo: "a", Project: "/tmp/b", Agent: "claude", Prompt: "hello",
	}, nil)
	if rec.Code != http.StatusBadRequest {
		t.Errorf("POST /v1/runs with both repo and project = %d, want 400", rec.Code)
	}
}

// doJSON runs one request against the routed handler, decoding into out when
// given. Kept here rather than shared: it is four lines, and the existing
// helpers in server_test.go each answer a slightly different question.
func doJSON(t *testing.T, s *Server, method, path string, body any, out any) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, newTestRequest(t, method, path, body))
	if out != nil && rec.Code >= 200 && rec.Code < 300 {
		if err := json.Unmarshal(rec.Body.Bytes(), out); err != nil {
			t.Fatalf("decoding %s %s: %v\nbody: %s", method, path, err, rec.Body.String())
		}
	}
	return rec
}

func writeFile(t *testing.T, dir, name string) string {
	t.Helper()
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

// One fact, one name, across every type in the contract.
//
// A run's repository id was `repo` while a worktree's was `repoId`, and the
// frontend was written against the second spelling — so filtering runs by
// repository compared every row against a field that did not exist and produced
// an empty table. It read as "this repository has no runs", which is why it
// survived: the failure is indistinguishable from the ordinary empty case unless
// you pick a repository you *know* has runs.
func TestRunAndWorktreeSpellRepoIdTheSameWay(t *testing.T) {
	run, err := json.Marshal(toRun(runtime.ContainerInfo{
		ID:     "abcdef012345",
		Labels: map[string]string{sandbox.LabelRepo: "sandbox-cli-82799c04"},
	}, "docker"))
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(run, &fields); err != nil {
		t.Fatal(err)
	}
	if _, ok := fields["repoId"]; !ok {
		t.Errorf("a run does not carry repoId; Worktree does, and a client filtering both by repository can only be right about one of them:\n%s", run)
	}
	if _, ok := fields["repo"]; ok {
		t.Errorf("a run still carries the old `repo` spelling as well; two names for one fact is what this test exists to stop:\n%s", run)
	}
	if got := fields["repoName"]; got != "sandbox-cli" {
		t.Errorf("repoName = %v, want %q — the display half of the id", got, "sandbox-cli")
	}
}

// The name is recovered by inverting how the id was built, so an id that was
// not built that way is handed back whole rather than trimmed into nonsense.
func TestRepoNameFromID(t *testing.T) {
	cases := map[string]string{
		"sandbox-cli-82799c04": "sandbox-cli",
		"intrupt_api-fdce81c9": "intrupt_api",
		"repoB-651fcf35":       "repoB",
		"":                     "",
		"noHashHere":           "noHashHere",
		"trailing-zzzzzzzz":    "trailing-zzzzzzzz", // not hex, so not a hash
		"short-1234":           "short-1234",
	}
	for id, want := range cases {
		if got := repoNameFromID(id); got != want {
			t.Errorf("repoNameFromID(%q) = %q, want %q", id, got, want)
		}
	}
}

// "All repositories" has to mean all of them. The absent parameter cannot serve
// that meaning, because it already means "the one this daemon was started in" —
// which is why `repo=all` has its own spelling rather than a changed default.
func TestWorktreesRepoAllSpansEveryRegisteredRepository(t *testing.T) {
	s, _ := newTestServer(t)
	initGitRepo(t, s.Project)
	other := t.TempDir()
	initGitRepo(t, other)
	rec, err := validateProjectPath(other)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Projects.add(rec); err != nil {
		t.Fatal(err)
	}

	var all, one WorktreesResponse
	if r := doJSON(t, s, http.MethodGet, "/v1/worktrees?repo=all", nil, &all); r.Code != http.StatusOK {
		t.Fatalf("GET /v1/worktrees?repo=all = %d: %s", r.Code, r.Body.String())
	}
	doJSON(t, s, http.MethodGet, "/v1/worktrees?repo="+rec.ID, nil, &one)

	seen := map[string]bool{}
	for _, w := range all.Worktrees {
		seen[w.RepoID] = true
	}
	// Both repositories are represented, each row under its own id — a listing
	// that stamped one repo id on every row would file another repository's
	// branches under this one.
	if len(all.Worktrees) < len(one.Worktrees) {
		t.Errorf("repo=all returned %d worktrees, fewer than the %d for one repository", len(all.Worktrees), len(one.Worktrees))
	}
	for _, w := range one.Worktrees {
		if w.RepoID != rec.ID {
			t.Errorf("a scoped listing returned repo %q, want %q", w.RepoID, rec.ID)
		}
	}
	// An unregistered id is still refused; "all" is a value, not a bypass.
	if r := doJSON(t, s, http.MethodGet, "/v1/worktrees?repo=nope-0000abcd", nil, nil); r.Code != http.StatusNotFound {
		t.Errorf("unregistered repo id = %d, want 404", r.Code)
	}
}

// The audit log has no repo field, so which repository a record belongs to is
// derived from the workspace it did record. Two shapes are exact; everything
// else is honestly unknown rather than filed under a guess.
func TestRepoIDForWorkspace(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	projects := []Project{
		{ID: "sandbox-cli-82799c04", Root: "/Users/x/code/sandbox-cli"},
		{ID: "nested-11111111", Root: "/Users/x/code/sandbox-cli/vendor/nested"},
	}
	root := config.ConfigRoot()

	cases := []struct {
		name, ws, want string
	}{
		{"a managed worktree carries the id in its path",
			filepath.Join(root, "worktrees", "repoB-651fcf35", "feat-x"), "repoB-651fcf35"},
		{"a registered repository root", "/Users/x/code/sandbox-cli", "sandbox-cli-82799c04"},
		{"a directory inside one", "/Users/x/code/sandbox-cli/internal", "sandbox-cli-82799c04"},
		{"the nearest repository wins, not the outer one",
			"/Users/x/code/sandbox-cli/vendor/nested/src", "nested-11111111"},
		{"a sibling whose name is a prefix", "/Users/x/code/sandbox-cli-secrets", ""},
		{"an unregistered checkout", "/private/tmp/pwnrepo", ""},
		{"nothing recorded", "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := repoIDForWorkspace(tc.ws, projects); got != tc.want {
				t.Errorf("repoIDForWorkspace(%q) = %q, want %q", tc.ws, got, tc.want)
			}
		})
	}
}

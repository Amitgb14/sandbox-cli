package studioapi

import (
	"encoding/json"
	"net/http"
	"testing"
)

// The base branch was a text box, and the base is stamped as a label that
// `fleet land` reads back to decide what to merge into — so a typo survived
// until landing. The picker needs a list of what actually exists.
func TestBranchesListsWhatTheRepositoryHas(t *testing.T) {
	s, _ := newTestServer(t)
	snapshotRepo(t, s)
	h := s.Handler()

	gitIn(t, s.Project, "branch", "feat/one")
	gitIn(t, s.Project, "branch", "fix/two")

	rec := doRequest(t, h, http.MethodGet, "/v1/branches", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("branches = %d: %s", rec.Code, rec.Body)
	}
	var got BranchList
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}

	want := map[string]bool{"feat/one": false, "fix/two": false}
	for _, b := range got.Branches {
		if _, ok := want[b]; ok {
			want[b] = true
		}
	}
	for b, found := range want {
		if !found {
			t.Errorf("branch %q missing from %v", b, got.Branches)
		}
	}

	// The checked-out branch comes back separately rather than decorated into the
	// list, so a caller wanting a default does not parse names apart.
	if got.Current == "" {
		t.Error("no current branch reported for a repository that has one checked out")
	}
	for _, b := range got.Branches {
		if b == got.Current {
			return
		}
	}
	t.Errorf("current %q is not among the branches %v", got.Current, got.Branches)
}

// The base belongs to one repository by construction — it is what a run there
// will be landed into — so there is no `repo=all`, and an id nothing registered
// matches is refused rather than silently answered from the default project.
func TestBranchesRefusesAnUnknownRepository(t *testing.T) {
	s, _ := newTestServer(t)
	snapshotRepo(t, s)

	rec := doRequest(t, s.Handler(), http.MethodGet, "/v1/branches?repo=nosuchrepo", nil)
	if rec.Code != http.StatusNotFound {
		t.Errorf("unknown repo = %d, want 404: %s", rec.Code, rec.Body)
	}
}

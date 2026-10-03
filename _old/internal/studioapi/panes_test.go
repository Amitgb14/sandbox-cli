package studioapi

import (
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/protocol"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
	"github.com/Amitgb14/sandbox-cli/internal/session"
)

// A run started from Studio is a pane: it gets an id, it is in the catalog, and the
// container carries the labels a later `serve` rebinds it by.
//
// The reason this is more than bookkeeping: a pane is what the snapshot keeper
// protects, and a Studio run is a detached run — the kind that had no crash safety net
// at all. Before this, the only snapshot a Studio run had was the *baseline* taken
// before the agent started, which is how a file somebody watched an agent write came
// back missing.
func TestStudioRunsAreRecordedAsPanes(t *testing.T) {
	srv, fr := newTestServer(t)
	srv.Project = gitRepoAt(t)
	srv.Panes = openCatalog(t, srv.Project, srv.Engine)

	rec := doRequest(t, srv.Handler(), http.MethodPost, "/v1/runs", RunCreateRequest{
		Command: []string{"true"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/runs = %d: %s", rec.Code, rec.Body.String())
	}

	panes := srv.Panes.Snapshot().Panes
	if len(panes) != 1 {
		t.Fatalf("catalog holds %d panes, want the run: %+v", len(panes), panes)
	}
	p := panes[0]
	if p.ID == "" {
		t.Error("the pane has no id")
	}
	if p.Kind != protocol.PaneCommand {
		t.Errorf("kind = %q, want command for a plain argv", p.Kind)
	}

	// And the labels, which are what survive this process: a pane whose id is not on
	// its container is one no later `serve` can rebind.
	if len(fr.started) == 0 {
		t.Fatal("nothing was started")
	}
	labels := fr.started[len(fr.started)-1].Labels
	for _, k := range []string{sandbox.LabelPane, sandbox.LabelPaneKind, sandbox.LabelPaneSession} {
		if labels[k] == "" {
			t.Errorf("container carries no %s, so a restarted serve could not rebind it", k)
		}
	}
	if labels[sandbox.LabelPane] != p.ID {
		t.Errorf("container says pane %q and the catalog says %q", labels[sandbox.LabelPane], p.ID)
	}
}

// A run with a verify is recorded as a verify pane, because its exit code is the
// verdict `land` reads — the label and the container must not disagree about what the
// thing is.
func TestAStudioRunWithAVerifyIsAVerifyPane(t *testing.T) {
	srv, _ := newTestServer(t)
	srv.Project = gitRepoAt(t)
	srv.Panes = openCatalog(t, srv.Project, srv.Engine)

	rec := doRequest(t, srv.Handler(), http.MethodPost, "/v1/runs", RunCreateRequest{
		Command: []string{"true"}, Verify: "test -f built",
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/runs = %d: %s", rec.Code, rec.Body.String())
	}
	panes := srv.Panes.Snapshot().Panes
	if len(panes) != 1 || panes[0].Kind != protocol.PaneVerify {
		t.Errorf("kind = %v, want verify", panes)
	}
}

// No catalog is supported and must not stop a launch: a project that is not a git
// repository has no session to open, and `--api-in-docker` mounts only the project.
// The bookkeeping is a courtesy, and refusing a run because a directory is unwritable
// would trade the feature for a record of it.
func TestAStudioRunLaunchesWithNoCatalog(t *testing.T) {
	srv, fr := newTestServer(t)
	if srv.Panes != nil {
		t.Fatal("newTestServer came with a catalog; this case is about not having one")
	}
	rec := doRequest(t, srv.Handler(), http.MethodPost, "/v1/runs", RunCreateRequest{
		Command: []string{"true"},
	})
	if rec.Code != http.StatusCreated {
		t.Fatalf("POST /v1/runs = %d with no catalog: %s", rec.Code, rec.Body.String())
	}
	if len(fr.started) == 0 {
		t.Error("nothing was started")
	}
}

func gitRepoAt(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil {
		dir = resolved
	}
	for _, args := range [][]string{
		{"init", "-q", "-b", "main"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "T"},
		{"commit", "-q", "--allow-empty", "-m", "first"},
	} {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Skipf("git is needed here: %v: %s", err, out)
		}
	}
	return dir
}

// openCatalog opens a session under a short config root, because a unix socket address
// holds about 100 bytes and Go's own t.TempDir() on macOS is long enough on its own to
// push the session socket past it.
func openCatalog(t *testing.T, project, engine string) *session.Server {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "sbxs-")
	if err != nil {
		dir, err = os.MkdirTemp(t.TempDir(), "sbxs-")
		if err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	if resolved, rerr := filepath.EvalSymlinks(dir); rerr == nil {
		dir = resolved
	}
	t.Setenv("XDG_CONFIG_HOME", dir)
	srv, err := session.Open(project, "dev", engine)
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	return srv
}

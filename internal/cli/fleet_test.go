package cli

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/fleet"
	"github.com/Amitgb14/sandbox-cli/internal/server"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// A fleet task's sandbox takes the user's configuration and profile as a run
// does. It used to build its own request: under prod, with hosts named in the
// config, a task ran on the server's default allowlist — github.com in it —
// and a task's allow always added that baseline back.
func TestFleetTasksTakeTheConfigAndProfile(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("no git")
	}
	home := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", home)
	os.MkdirAll(filepath.Join(home, "sandbox"), 0o700)
	os.WriteFile(filepath.Join(home, "sandbox", "config.yaml"),
		[]byte("profile: prod\nnetwork:\n  allow: [api.example]\nenv:\n  FROM_CONFIG: yes\n"), 0o600)

	repo := t.TempDir()
	for _, args := range [][]string{{"init", "-q", "-b", "main"}, {"-c", "user.name=t", "-c", "user.email=t@x", "commit", "-q", "--allow-empty", "-m", "base"}} {
		cmd := exec.Command("git", args...)
		cmd.Dir = repo
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v %s", args, err, out)
		}
	}

	srv := httptest.NewServer((&server.Server{Backend: fake.New(api.CapEgressAllowlist), Policy: spec.DefaultPolicy()}).Handler())
	defer srv.Close()
	c, _ := api.NewClient(srv.URL, "")
	ctx := context.Background()
	caps, err := c.Capabilities(ctx)
	if err != nil {
		t.Fatal(err)
	}
	r := &fleet.Runner{Client: c, Repo: repo, Keep: true, Out: &bytes.Buffer{}, Prepare: fleetPrepare(repo, "", caps)}
	sp := fleet.Spec{Agent: "claude", Tasks: []fleet.Task{{Branch: "a", Prompt: "p", Allow: []string{"b.example"}}}}
	if _, err := r.Run(ctx, sp); err != nil {
		t.Fatal(err)
	}
	sbs, err := c.Sandboxes(ctx)
	if err != nil || len(sbs) != 1 {
		t.Fatalf("sandboxes %v %v", sbs, err)
	}
	want := api.NetworkPolicy{Mode: api.NetworkAllowlist, Allow: []string{"api.example", "b.example"}}
	if got := sbs[0].Network; got.Mode != want.Mode || !reflect.DeepEqual(got.Allow, want.Allow) {
		t.Errorf("the task's network is %+v, want %+v", got, want)
	}

	if _, err := (&fleet.Runner{Client: c, Repo: repo, Out: &bytes.Buffer{}}).Run(ctx, sp); err == nil {
		t.Error("a runner with nothing to apply the configuration ran")
	}
}

package cli

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/server"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
	"github.com/Amitgb14/sandbox-cli/internal/workspace"
)

// recover says where each run's work still is: a live sandbox can be brought
// back, a gone one has its checkpoint or is said to be lost, a record whose work
// is home and whose sandbox is gone is pruned, and other repositories stay out
// of it unless asked.
func TestRecoverSaysWhereTheWorkIs(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("SANDBOX_CONTEXT", "")
	srv := httptest.NewServer((&server.Server{Backend: fake.New(api.CapEgressAllowlist), Policy: spec.DefaultPolicy()}).Handler())
	defer srv.Close()
	cf, _ := loadContexts()
	cf.Contexts["t"] = endpointContext{Endpoint: srv.URL}
	if err := cf.save(); err != nil {
		t.Fatal(err)
	}
	c, _ := api.NewClient(srv.URL, "")
	live, err := c.CreateSandbox(context.Background(), api.CreateSandboxRequest{})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	for _, s := range []workspace.Session{
		{Sandbox: live.ID, Context: "t", Repo: "/r", Started: now.Add(-time.Hour)},
		{Sandbox: "sbx_ckpt", Context: "t", Repo: "/r", Started: now.Add(-2 * time.Hour),
			Checkpoint: workspace.CheckpointRefPrefix + "sbx_ckpt", CheckpointAt: now.Add(-10 * time.Minute)},
		{Sandbox: "sbx_lost", Context: "t", Repo: "/r", Started: now.Add(-3 * time.Hour)},
		{Sandbox: "sbx_home", Context: "t", Repo: "/r", Started: now, Done: true},
		{Sandbox: "sbx_elsewhere", Context: "t", Repo: "/other", Started: now},
		{Sandbox: "sbx_unreachable", Context: "nope", Repo: "/r", Started: now},
	} {
		if err := s.Save(); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	if err := listRecoverable(context.Background(), &out, "/r", now); err != nil {
		t.Fatal(err)
	}
	got := out.String()
	for _, want := range []string{
		"sandbox-cli bring-back " + live.ID,
		"its last checkpoint is refs/sandbox/checkpoints/sbx_ckpt",
		"10m ago",
		"sbx_lost: the sandbox is gone and no checkpoint was taken",
		"sbx_unreachable: its sandboxd (context nope) did not answer",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "sbx_home") || strings.Contains(got, "sbx_elsewhere") {
		t.Errorf("listed a finished run or another repository:\n%s", got)
	}
	if _, err := os.Stat(filepath.Join(workspace.ConfigDir(), "sessions", "sbx_home.json")); err == nil {
		t.Error("a finished run whose sandbox is gone was not pruned")
	}
	if _, err := os.Stat(filepath.Join(workspace.ConfigDir(), "sessions", "sbx_lost.json")); err != nil {
		t.Error("an unrecovered run's record was removed; only forget may do that")
	}
}

// Nothing checkpoints a detached run, so asking for checkpoints on one is
// refused before anything starts; left at its default, the flag is not.
func TestDetachRefusesCheckpointEvery(t *testing.T) {
	run := func(args ...string) error {
		rf := &runFlags{}
		cmd := &cobra.Command{}
		rf.register(cmd)
		if err := cmd.ParseFlags(args); err != nil {
			t.Fatal(err)
		}
		rf.context = "no-such-context" // anything past the refusal stops at the client
		_, err := runSandbox(context.Background(), rf, runSpec{argv: []string{"true"}})
		return err
	}
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	if err := run("--detach", "--checkpoint-every", "1m"); err == nil || !strings.Contains(err.Error(), "--checkpoint-every") {
		t.Errorf("got %v", err)
	}
	if err := run("--detach"); err != nil && strings.Contains(err.Error(), "--checkpoint-every") {
		t.Errorf("the default was refused: %v", err)
	}
	if err := run("--detach", "--checkpoint-every", "0"); err != nil && strings.Contains(err.Error(), "--checkpoint-every") {
		t.Errorf("turning checkpoints off was refused: %v", err)
	}
}

// attach checkpoints a run only when this host started it on a repository that
// is still here, on the same sandboxd, and it has not finished; a name resolves
// to the run started under it.
func TestAttachedSession(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	srv := httptest.NewServer((&server.Server{Backend: fake.New(api.CapEgressAllowlist), Policy: spec.DefaultPolicy()}).Handler())
	defer srv.Close()
	c, _ := api.NewClient(srv.URL, "")
	ctx := context.Background()
	repo := t.TempDir()
	mk := func(name string, s workspace.Session) string {
		sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{Name: name})
		if err != nil {
			t.Fatal(err)
		}
		s.Sandbox = sb.ID
		if err := s.Save(); err != nil {
			t.Fatal(err)
		}
		return sb.ID
	}
	mk("live", workspace.Session{Context: "t", Repo: repo})
	mk("done", workspace.Session{Context: "t", Repo: repo, Done: true})
	mk("moved", workspace.Session{Context: "t", Repo: filepath.Join(repo, "gone")})
	mk("other", workspace.Session{Context: "elsewhere", Repo: repo})
	if _, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{Name: "norecord"}); err != nil {
		t.Fatal(err)
	}

	if s, ok := attachedSession(ctx, c, "t", "live"); !ok || s.Repo != repo {
		t.Errorf("live: %v %v", s, ok)
	}
	for _, ref := range []string{"done", "moved", "other", "norecord", "nonexistent"} {
		if _, ok := attachedSession(ctx, c, "t", ref); ok {
			t.Errorf("%s was checkpointed", ref)
		}
	}
}

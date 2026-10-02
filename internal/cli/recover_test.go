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

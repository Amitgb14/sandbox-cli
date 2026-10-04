//go:build unix

package cli

import (
	"context"
	"errors"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/server"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// exec and shell connect to a sandbox that is already running: the command's
// exit status is theirs, the sandbox outlives it, and one that is not running
// is refused with what to do about it rather than started.
func TestConnectRunsInAnExistingSandbox(t *testing.T) {
	be := fake.New(api.CapEgressAllowlist, api.CapSuspend)
	srv := httptest.NewServer((&server.Server{Backend: be, Policy: spec.DefaultPolicyFor(be.Capabilities())}).Handler())
	defer srv.Close()
	c, _ := api.NewClient(srv.URL, "")
	ctx := context.Background()
	sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{Name: "conn"})
	if err != nil {
		t.Fatal(err)
	}

	if err := connectWith(ctx, c, "conn", []string{"true"}, "/sandbox/home"); err != nil {
		t.Errorf("true: %v", err)
	}
	var ee exitError
	if err := connectWith(ctx, c, "conn", []string{"false"}, "/sandbox/home"); !errors.As(err, &ee) || ee.code != 1 {
		t.Errorf("false: %v; want exit status 1", err)
	}
	if got, _ := c.Sandbox(ctx, sb.ID); got.State != api.StateRunning {
		t.Errorf("the sandbox is %s after the command; it must keep running", got.State)
	}
	if err := connectWith(ctx, c, "nope", []string{"true"}, ""); err == nil {
		t.Error("an unknown sandbox was connected to")
	}
	if _, err := c.Suspend(ctx, sb.ID); err != nil {
		t.Fatal(err)
	}
	if err := connectWith(ctx, c, "conn", []string{"true"}, ""); err == nil || !strings.Contains(err.Error(), "resume it first") {
		t.Errorf("a suspended sandbox: %v", err)
	}
}

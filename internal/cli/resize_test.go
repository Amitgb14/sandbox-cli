package cli

import (
	"bytes"
	"context"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/server"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// resize says what changed and what did not, and says so when the size
// asked for is the one it already has.
func TestResizeShowsWhatChanged(t *testing.T) {
	be := fake.New(api.CapResize)
	srv := httptest.NewServer((&server.Server{Backend: be, Policy: spec.DefaultPolicyFor(be.Capabilities())}).Handler())
	defer srv.Close()
	useEndpoint(t, srv.URL)
	c, err := api.NewClient(srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	sb, err := c.CreateSandbox(context.Background(), api.CreateSandboxRequest{CPUs: 1, MemoryMB: 512, DiskMB: 1024})
	if err != nil {
		t.Fatal(err)
	}
	run := func(args ...string) string {
		t.Helper()
		root := NewRootCmd()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(append([]string{"resize", sb.ID}, args...))
		if err := root.Execute(); err != nil {
			t.Fatalf("resize %v: %v\n%s", args, err, out.String())
		}
		return out.String()
	}
	out := run("--memory", "1024", "--disk", "2048")
	for _, want := range []string{"resized and restarted", "CPUs    1 (unchanged)", "memory  512 MiB -> 1024 MiB", "disk    1024 MiB -> 2048 MiB"} {
		if !strings.Contains(out, want) {
			t.Errorf("a resize: missing %q in\n%s", want, out)
		}
	}
	out = run("--memory", "1024")
	if !strings.Contains(out, "already that size; not restarted") || !strings.Contains(out, "memory  1024 MiB (unchanged)") {
		t.Errorf("the same size again:\n%s", out)
	}
}

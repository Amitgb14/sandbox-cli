package cli

import (
	"bytes"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/server"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// image pull waits for the install and says when it is done, ls lists it,
// a failed pull is an error with the registry's reason, and rm removes it.
func TestImageCommands(t *testing.T) {
	be := fake.New(api.CapEgressAllowlist)
	srv := httptest.NewServer((&server.Server{Backend: be, Policy: spec.DefaultPolicyFor(be.Capabilities())}).Handler())
	defer srv.Close()
	useEndpoint(t, srv.URL)
	run := func(args ...string) (string, error) {
		root := NewRootCmd()
		var out bytes.Buffer
		root.SetOut(&out)
		root.SetErr(&out)
		root.SetArgs(append([]string{"image"}, args...))
		err := root.Execute()
		return out.String(), err
	}
	const ref = "registry.example/team/tool:1.0"
	if out, err := run("pull", ref); err != nil || !strings.Contains(out, "installed "+ref) || strings.Contains(out, "\x1b") {
		t.Fatalf("pull, not to a terminal: %q, %v; want no escapes", out, err)
	}
	out, err := run("ls")
	if err != nil || !strings.Contains(out, ref) || !strings.Contains(out, "installed") || !strings.Contains(out, "64 MiB") {
		t.Fatalf("ls: %q, %v", out, err)
	}
	if out, err := run("pull", "x/"+fake.MissingImage+":1"); err == nil || !strings.Contains(err.Error(), "manifest unknown") {
		t.Fatalf("a failed pull: %q, %v", out, err)
	}
	out, err = run("rm", ref, "never/installed:1")
	if err == nil || !strings.Contains(out, "removed "+ref) || !strings.Contains(out, "never/installed:1") {
		t.Fatalf("rm of one installed and one not: %q, %v", out, err)
	}
}

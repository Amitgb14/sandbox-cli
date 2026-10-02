package macos

import (
	"context"
	"flag"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/api/conformance"
	"github.com/Amitgb14/sandbox-cli/internal/backend"
	"github.com/Amitgb14/sandbox-cli/internal/image"
	"github.com/Amitgb14/sandbox-cli/internal/server"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

var update = flag.Bool("update", false, "rewrite golden files")

func golden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)
	if *update {
		_ = os.MkdirAll("testdata", 0o755)
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("%v (run with -update once, then review the file)", err)
	}
	if got != string(want) {
		t.Errorf("%s changed:\n--- want\n%s--- got\n%s", path, want, got)
	}
}

// What a sandbox is, as the runtime is told: the agent read-only, the network
// off unless open was asked for, a bind only when there is one, and "--" before
// the image so a reference can never be read as a flag.
func TestBuildRunArgsGolden(t *testing.T) {
	base := backend.Spec{ID: "sbx_0123456789abcdef", Image: "img:1", CPUs: 1.5, MemoryMB: 2048, DiskMB: 10240}
	var out strings.Builder
	for _, c := range []struct {
		name string
		mod  func(*backend.Spec)
	}{
		{"none", func(s *backend.Spec) { s.Network = api.NetworkPolicy{Mode: api.NetworkNone} }},
		{"open", func(s *backend.Spec) { s.Network = api.NetworkPolicy{Mode: api.NetworkOpen} }},
		{"bind read-only", func(s *backend.Spec) {
			s.Network = api.NetworkPolicy{Mode: api.NetworkNone}
			s.Bind = &backend.Bind{HostPath: "/Users/dev/project", ReadOnly: true}
		}},
	} {
		s := base
		c.mod(&s)
		args, err := BuildRunArgs(s, "/usr/local/libexec/sandboxd")
		if err != nil {
			t.Fatal(err)
		}
		out.WriteString("# " + c.name + "\n" + strings.Join(args, " ") + "\n")
	}
	out.WriteString("# exec\n" + strings.Join(BuildExecArgs("sbx_0123456789abcdef"), " ") + "\n")
	golden(t, "args.txt", out.String())
}

// An allowlist this backend cannot enforce is refused, never rendered open.
func TestBuildRunArgsRefusesAnAllowlist(t *testing.T) {
	s := backend.Spec{ID: "sbx_1", Image: "img", Network: api.NetworkPolicy{Mode: api.NetworkAllowlist, Allow: []string{"github.com"}}}
	if _, err := BuildRunArgs(s, "/agent"); err == nil {
		t.Fatal("an allowlist was rendered")
	}
	s.Network = api.NetworkPolicy{Mode: api.NetworkNone}
	s.Bind = &backend.Bind{HostPath: "/a,b"}
	if _, err := BuildRunArgs(s, "/agent"); err == nil {
		t.Fatal("a bind path with a comma was rendered")
	}
}

// fakeContainer is a `container` CLI that runs each sandbox as a directory
// holding an alpine root filesystem, and each exec as the real guest agent in a
// user namespace chrooted into it. It is everything the macOS runtime does that
// this backend relies on, minus the VM.
const fakeContainer = `#!/bin/sh
set -e
cmd=$1; shift
case "$cmd" in
ls) echo "[]" ;;
run)
  while [ $# -gt 0 ]; do
    case "$1" in --name) id=$2; shift 2 ;; --) shift; break ;; *) shift ;; esac
  done
  cp -a "$FAKE_TEMPLATE" "$FAKE_ROOT/$id"
  mkdir -p "$FAKE_ROOT/$id/.sbx" "$FAKE_ROOT/$id/workspace" "$FAKE_ROOT/$id/tmp"
  cp "$FAKE_AGENT" "$FAKE_ROOT/$id/.sbx/sandbox-guestd"
  echo "$id" ;;
exec)
  shift # --interactive
  id=$1; shift
  exec unshare -r --root="$FAKE_ROOT/$id" "$@" --uid -1 --gid -1 ;;
rm)
  shift # --force
  rm -rf "$FAKE_ROOT/$1" ;;
esac
`

// TestConformanceThroughAFakeRuntime runs the API conformance suite against this
// backend, with the runtime replaced by fakeContainer. Linux only (user
// namespaces); needs network access once, to pull alpine.
func TestConformanceThroughAFakeRuntime(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("the fake runtime uses Linux user namespaces")
	}
	if err := exec.Command("unshare", "-r", "true").Run(); err != nil {
		t.Skip("unprivileged user namespaces are not available")
	}
	work := t.TempDir()
	ctx := context.Background()

	// An alpine root filesystem, unpacked by the same code that builds disks.
	cache := filepath.Join(os.TempDir(), "sandbox-test-images")
	pulled, err := (&image.Puller{Cache: cache}).Pull(ctx, "alpine:3.20")
	if err != nil {
		t.Skipf("cannot pull alpine: %v", err)
	}
	template := filepath.Join(work, "template")
	if err := os.Mkdir(template, 0o755); err != nil {
		t.Fatal(err)
	}
	var st image.UnpackStats
	for _, l := range pulled.Layers {
		f, _ := os.Open(l)
		err := image.Unpack(template, f, 1<<30, &st)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}

	agentDir := filepath.Join(work, "agent")
	os.Mkdir(agentDir, 0o755)
	agent := filepath.Join(agentDir, "sandbox-guestd")
	build := exec.Command("go", "build", "-o", agent, "../../../cmd/sandbox-guestd")
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("building the guest agent: %v\n%s", err, out)
	}
	script := filepath.Join(work, "container")
	os.WriteFile(script, []byte(fakeContainer), 0o755)
	roots := filepath.Join(work, "sandboxes")
	os.Mkdir(roots, 0o755)
	t.Setenv("FAKE_TEMPLATE", template)
	t.Setenv("FAKE_ROOT", roots)
	t.Setenv("FAKE_AGENT", agent)

	be, err := New(Config{Container: script, Agent: agent, Logf: t.Logf})
	if err != nil {
		t.Fatal(err)
	}
	pol := spec.DefaultPolicy()
	pol.DefaultImage = "alpine:3.20"
	pol, notes := pol.FitTo(be.Capabilities())
	for _, n := range notes {
		t.Log("policy: " + n)
	}
	srv := &server.Server{Backend: be, Policy: pol, Token: "macos-fake"}
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	defer be.Close()
	conformance.Run(t, api.NewClientWithHTTP(ts.URL, "macos-fake", ts.Client()))
}

// The runtime's argv is visible to every process on the Mac (ps), so a
// sandbox's environment is set inside the guest, never passed as -e.
func TestBuildRunArgsCarryNoEnvironmentValue(t *testing.T) {
	const secret = "s3cret-value-in-env"
	s := backend.Spec{ID: "sbx_0123456789abcdef", Image: "img:1", Env: map[string]string{"API_TOKEN": secret},
		Network: api.NetworkPolicy{Mode: api.NetworkNone}}
	args, err := BuildRunArgs(s, "/agent")
	if err != nil {
		t.Fatal(err)
	}
	if j := strings.Join(args, " "); strings.Contains(j, secret) || strings.Contains(j, "API_TOKEN") {
		t.Errorf("an environment variable reached the runtime's argv: %s", j)
	}
}

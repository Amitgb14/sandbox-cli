package cli

import (
	"strings"
	"testing"

	"github.com/Amitgb14/sandbox-cli/internal/runtime"
)

// TestDryRunInvariants is the cheap, no-Docker proof of the security invariant:
// the rendered docker command mounts only the project, sets the fake HOME, uses
// --rm, and never mounts the host home directory.
func TestDryRunInvariants(t *testing.T) {
	spec := runtime.RunSpec{
		Image:    "sandbox-base:0.1.0",
		Workdir:  "/workspace",
		Command:  []string{"echo", "hi there"},
		Remove:   true,
		Hostname: "sandbox",
		Home:     "/sandbox/home",
		User:     "root",
		Mounts:   []runtime.Mount{{Source: "/Users/dev/proj", Target: "/workspace"}},

		NoNewPrivileges: true,
		CapDrop:         []string{"ALL"},
		PidsLimit:       1024,
	}
	line := dockerCommandLine(spec)

	mustContain := []string{
		"--rm",
		"-e HOME=/sandbox/home",
		"-w /workspace",
		"type=bind,source=/Users/dev/proj,target=/workspace",
		"--security-opt no-new-privileges",
		"--cap-drop ALL",
		"--pids-limit 1024",
	}
	for _, s := range mustContain {
		if !strings.Contains(line, s) {
			t.Errorf("dry-run line missing %q:\n%s", s, line)
		}
	}

	// The only bind mount is the project; the host home is never mounted.
	if strings.Count(line, "type=bind") != 1 {
		t.Errorf("expected exactly one bind mount:\n%s", line)
	}
	// An argument with a space must be quoted so the line is copy-pasteable.
	if !strings.Contains(line, "'hi there'") {
		t.Errorf("expected quoted argument:\n%s", line)
	}

	// Nothing is reachable from the host unless it was asked for.
	if strings.Contains(line, "-p ") {
		t.Errorf("a spec with no ports must publish nothing:\n%s", line)
	}
}

// TestDryRunShowsPublishedPorts: publishing is the one reach that points inward,
// so --dry-run has to show it — with the address it will actually bind, not the
// shorthand that was typed.
func TestDryRunShowsPublishedPorts(t *testing.T) {
	spec := runtime.RunSpec{
		Image:   "sandbox-base:0.1.0",
		Workdir: "/workspace",
		Command: []string{"npm", "run", "dev"},
		Home:    "/sandbox/home",
		Ports:   []string{"127.0.0.1:3000:3000", "0.0.0.0:8080:80"},
	}
	line := dockerCommandLine(spec)

	for _, want := range []string{"-p 127.0.0.1:3000:3000", "-p 0.0.0.0:8080:80"} {
		if !strings.Contains(line, want) {
			t.Errorf("dry-run line missing %q:\n%s", want, line)
		}
	}
}

// TestDryRunDetached is the same proof for a background run: --detach changes how
// the container is attached and how long it survives, and nothing else. The
// mounts and HOME it prints are the ones the foreground case pins above.
func TestDryRunDetached(t *testing.T) {
	line := dockerCommandLine(runtime.RunSpec{
		Image:   "sandbox-base:0.1.0",
		Name:    "sandbox-app-1234abcd-feature-a",
		Workdir: "/workspace",
		Command: []string{"claude", "-p", "implement the login form"},
		Detach:  true,
		Home:    "/sandbox/home",
		Labels:  map[string]string{"sandbox.repo": "app-1234abcd", "sandbox.branch": "feature-a"},
		Mounts:  []runtime.Mount{{Source: "/Users/dev/proj", Target: "/workspace"}},

		NoNewPrivileges: true,
		CapDrop:         []string{"ALL"},
	})

	for _, s := range []string{
		"-d",
		"--label sandbox.branch=feature-a",
		"--label sandbox.repo=app-1234abcd",
		"--name sandbox-app-1234abcd-feature-a",
		"-e HOME=/sandbox/home",
		"type=bind,source=/Users/dev/proj,target=/workspace",
		"--security-opt no-new-privileges",
		"--cap-drop ALL",
	} {
		if !strings.Contains(line, s) {
			t.Errorf("detached dry-run line missing %q:\n%s", s, line)
		}
	}
	// Retained on purpose: the exit code and logs of an unattended run are the
	// only evidence it ran, and --rm would discard both.
	if strings.Contains(line, "--rm") {
		t.Errorf("a detached container must not be --rm:\n%s", line)
	}
	// Nothing is attached, so no pty and no stdin.
	if strings.Contains(line, "-it") {
		t.Errorf("a detached container must not get a pty:\n%s", line)
	}
	if strings.Count(line, "type=bind") != 1 {
		t.Errorf("expected exactly one bind mount:\n%s", line)
	}
}

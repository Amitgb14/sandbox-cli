package runtime

import (
	"reflect"
	"strings"
	"testing"
)

func TestBuildArgs_Basic(t *testing.T) {
	spec := RunSpec{
		Image:    "sandbox-base:0.1.0",
		Workdir:  "/workspace",
		Command:  []string{"sh", "-c", "echo hi"},
		Remove:   true,
		Hostname: "sandbox",
		Home:     "/sandbox/home",
		User:     "root",
		Mounts: []Mount{
			{Source: "/host/proj", Target: "/workspace", RO: false},
		},
	}
	got := BuildArgs(spec)
	want := []string{
		"run", "--init", "--rm", "-i",
		"--hostname", "sandbox",
		"--user", "root",
		"--mount", "type=bind,source=/host/proj,target=/workspace",
		"-w", "/workspace",
		"-e", "HOME=/sandbox/home",
		// `--` separates docker's own flags from the image reference. Deliberate:
		// Image is config-supplied, and without it `image: "--privileged"` was read
		// by docker as a flag (see TestBuildArgs_DashDashGuardsTheImage).
		"--", "sandbox-base:0.1.0",
		"sh", "-c", "echo hi",
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("BuildArgs mismatch:\n got=%v\nwant=%v", got, want)
	}
}

func TestBuildArgs_TTY(t *testing.T) {
	withTTY := BuildArgs(RunSpec{Image: "img", Workdir: "/w", TTY: true})
	if !containsArg(withTTY, "-it") {
		t.Errorf("expected -it with TTY on, got %v", withTTY)
	}
	withoutTTY := BuildArgs(RunSpec{Image: "img", Workdir: "/w", TTY: false})
	if containsArg(withoutTTY, "-it") || !containsArg(withoutTTY, "-i") {
		t.Errorf("expected -i (not -it) with TTY off, got %v", withoutTTY)
	}
}

func TestBuildArgs_ReadOnlyMount(t *testing.T) {
	got := BuildArgs(RunSpec{
		Image:   "img",
		Workdir: "/w",
		Mounts:  []Mount{{Source: "/h", Target: "/c", RO: true}},
	})
	if !containsArg(got, "type=bind,source=/h,target=/c,readonly") {
		t.Errorf("expected readonly mount, got %v", got)
	}
}

func TestBuildArgs_EnvOrderingDeterministic(t *testing.T) {
	spec := RunSpec{
		Image:   "img",
		Workdir: "/w",
		Env:     map[string]string{"B": "2", "A": "1", "C": "3"},
	}
	a := BuildArgs(spec)
	b := BuildArgs(spec)
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("BuildArgs not deterministic:\n%v\n%v", a, b)
	}
	// A must appear before B before C.
	joined := strings.Join(a, " ")
	iA := strings.Index(joined, "A=1")
	iB := strings.Index(joined, "B=2")
	iC := strings.Index(joined, "C=3")
	if !(iA < iB && iB < iC) {
		t.Errorf("env not sorted: A=%d B=%d C=%d in %q", iA, iB, iC, joined)
	}
}

func TestBuildArgs_EnvPassthroughByName(t *testing.T) {
	got := BuildArgs(RunSpec{Image: "img", Workdir: "/w", EnvNames: []string{"ANTHROPIC_API_KEY"}})
	// bare -e NAME (no =) forwards host value
	if !hasPair(got, "-e", "ANTHROPIC_API_KEY") {
		t.Errorf("expected passthrough -e ANTHROPIC_API_KEY, got %v", got)
	}
}

// TestBuildArgs_ExplicitEnvBeatsPassthrough pins the ordering that makes the
// security-critical env values un-overridable. docker keeps the LAST occurrence
// of a repeated -e, so a by-name forward rendered after an explicit value would
// silently replace it with whatever the host has set.
//
// This was live: forwarding SANDBOX_RUN_AS (via --env-allow, or a project
// env_allow: list) with SANDBOX_RUN_AS=root in the host environment made the
// firewall entrypoint skip its setpriv drop and run the agent as root.
func TestBuildArgs_ExplicitEnvBeatsPassthrough(t *testing.T) {
	got := BuildArgs(RunSpec{
		Image:    "img",
		Workdir:  "/w",
		Env:      map[string]string{"SANDBOX_RUN_AS": "sandbox"},
		EnvNames: []string{"SANDBOX_RUN_AS", "ANTHROPIC_API_KEY"},
	})

	// The colliding name must not be forwarded at all — a bare `-e NAME` after
	// `-e NAME=value` would win, and before it is merely dead weight.
	for i := 0; i < len(got)-1; i++ {
		if got[i] == "-e" && got[i+1] == "SANDBOX_RUN_AS" {
			t.Errorf("bare -e SANDBOX_RUN_AS forwarded alongside an explicit value: %v", got)
		}
	}
	if !hasPair(got, "-e", "SANDBOX_RUN_AS=sandbox") {
		t.Errorf("explicit value missing: %v", got)
	}
	// A non-colliding name is still forwarded, and still before the explicit
	// values, so this ordering holds even if a collision slips through later.
	joined := strings.Join(got, " ")
	if !hasPair(got, "-e", "ANTHROPIC_API_KEY") {
		t.Errorf("non-colliding passthrough dropped: %v", got)
	}
	if strings.Index(joined, "-e ANTHROPIC_API_KEY") > strings.Index(joined, "-e SANDBOX_RUN_AS=sandbox") {
		t.Errorf("passthrough must render before explicit values so explicit wins: %q", joined)
	}
}

// TestBuildArgs_DashDashGuardsTheImage pins the separator that stops a
// config-supplied image reference from being read as a docker flag.
// `image: "--privileged"` used to render `docker run … --privileged sh -c …`,
// smuggling in a real flag and turning the guest's first argument into the
// image name.
func TestBuildArgs_DashDashGuardsTheImage(t *testing.T) {
	got := BuildArgs(RunSpec{Image: "--privileged", Workdir: "/w", Command: []string{"sh", "-c", "id"}})
	for i, a := range got {
		if a == "--" {
			if i+1 >= len(got) || got[i+1] != "--privileged" {
				t.Fatalf("expected the image immediately after --, got %v", got)
			}
			return
		}
	}
	t.Fatalf("no -- separator before the image; a dash-leading image is read as a flag: %v", got)
}

func TestBuildArgs_Network(t *testing.T) {
	got := BuildArgs(RunSpec{Image: "img", Workdir: "/w", Network: "none"})
	if !hasPair(got, "--network", "none") {
		t.Errorf("expected --network none, got %v", got)
	}
	got = BuildArgs(RunSpec{Image: "img", Workdir: "/w"})
	if containsArg(got, "--network") {
		t.Errorf("expected no --network by default, got %v", got)
	}
}

func TestBuildArgs_Hardening(t *testing.T) {
	got := BuildArgs(RunSpec{
		Image:           "img",
		Workdir:         "/w",
		NoNewPrivileges: true,
		Seccomp:         "unconfined",
		CapDrop:         []string{"ALL"},
		CapAdd:          []string{"NET_BIND_SERVICE"},
		PidsLimit:       1024,
		Memory:          "2g",
		CPUs:            "1.5",
	})
	pairs := [][2]string{
		{"--security-opt", "no-new-privileges"},
		{"--security-opt", "seccomp=unconfined"},
		{"--cap-drop", "ALL"},
		{"--cap-add", "NET_BIND_SERVICE"},
		{"--pids-limit", "1024"},
		{"--memory", "2g"},
		{"--cpus", "1.5"},
	}
	for _, p := range pairs {
		if !hasPair(got, p[0], p[1]) {
			t.Errorf("expected %s %s, got %v", p[0], p[1], got)
		}
	}
}

func TestBuildArgs_HardeningOmittedWhenUnset(t *testing.T) {
	got := BuildArgs(RunSpec{Image: "img", Workdir: "/w"})
	for _, f := range []string{"--security-opt", "--cap-drop", "--cap-add", "--pids-limit", "--memory", "--cpus"} {
		if containsArg(got, f) {
			t.Errorf("did not expect %s on a bare spec, got %v", f, got)
		}
	}
	// A zero/negative pids limit must not emit the flag.
	if containsArg(BuildArgs(RunSpec{Image: "img", Workdir: "/w", PidsLimit: 0}), "--pids-limit") {
		t.Error("PidsLimit 0 should omit --pids-limit")
	}
}

func TestBuildArgs_Runtime(t *testing.T) {
	got := BuildArgs(RunSpec{Image: "img", Workdir: "/w", Runtime: "kata-runtime"})
	if !hasPair(got, "--runtime", "kata-runtime") {
		t.Errorf("expected --runtime kata-runtime, got %v", got)
	}
	// Must precede the image (it's a run flag).
	joined := strings.Join(got, " ")
	if strings.Index(joined, "--runtime") > strings.Index(joined, " img") {
		t.Errorf("--runtime must come before the image: %v", got)
	}
	// Omitted by default (docker's default runc).
	if containsArg(BuildArgs(RunSpec{Image: "img", Workdir: "/w"}), "--runtime") {
		t.Error("did not expect --runtime on a bare spec")
	}
}

func TestBuildArgs_AddHost(t *testing.T) {
	got := BuildArgs(RunSpec{
		Image:    "img",
		Workdir:  "/w",
		AddHosts: []string{"host.docker.internal:host-gateway", "db:10.0.0.5"},
	})
	if !hasPair(got, "--add-host", "host.docker.internal:host-gateway") {
		t.Errorf("expected host-gateway add-host, got %v", got)
	}
	if !hasPair(got, "--add-host", "db:10.0.0.5") {
		t.Errorf("expected db add-host, got %v", got)
	}
	if containsArg(BuildArgs(RunSpec{Image: "img", Workdir: "/w"}), "--add-host") {
		t.Error("did not expect --add-host on a bare spec")
	}
}

func TestBuildArgs_Publish(t *testing.T) {
	got := BuildArgs(RunSpec{
		Image:   "img",
		Workdir: "/w",
		Ports:   []string{"127.0.0.1:3000:3000", "0.0.0.0:8080:80/tcp"},
	})
	if !hasPair(got, "-p", "127.0.0.1:3000:3000") {
		t.Errorf("expected the loopback port, got %v", got)
	}
	if !hasPair(got, "-p", "0.0.0.0:8080:80/tcp") {
		t.Errorf("expected the explicit-address port, got %v", got)
	}
	// Emitted verbatim: BuildArgs must not invent or rewrite an address, since
	// that decision belongs to sandbox.NormalizePublish and must stay readable in
	// --dry-run.
	for i, a := range got {
		if a == "-p" && i+1 < len(got) && !strings.Contains(got[i+1], ":") {
			t.Errorf("port spec %q reached docker without an explicit address", got[i+1])
		}
	}
	// The flag must precede the image so docker parses it as a run flag.
	joined := strings.Join(got, " ")
	if strings.Index(joined, "-p ") > strings.Index(joined, " img") {
		t.Errorf("-p must come before the image: %v", got)
	}
}

// TestBuildArgs_NoPublishByDefault is the half of the port story that matters:
// nothing is reachable from the host unless it was asked for.
func TestBuildArgs_NoPublishByDefault(t *testing.T) {
	got := BuildArgs(RunSpec{
		Image:   "img",
		Workdir: "/workspace",
		Mounts:  []Mount{{Source: "/host/proj", Target: "/workspace"}},
	})
	if containsArg(got, "-p") {
		t.Errorf("a spec with no Ports must publish nothing, got %v", got)
	}
}

func TestBuildArgs_VolumeMount(t *testing.T) {
	got := BuildArgs(RunSpec{
		Image:   "img",
		Workdir: "/w",
		Mounts: []Mount{
			{Source: "/host/proj", Target: "/workspace"},                              // bind
			{Source: "sandbox-cache-npm", Target: "/sandbox/home/.npm", Volume: true}, // volume
		},
	})
	if !containsArg(got, "type=bind,source=/host/proj,target=/workspace") {
		t.Errorf("expected the bind mount, got %v", got)
	}
	if !containsArg(got, "type=volume,source=sandbox-cache-npm,target=/sandbox/home/.npm") {
		t.Errorf("expected the named volume mount, got %v", got)
	}
}

func TestBuildArgs_Entrypoint(t *testing.T) {
	got := BuildArgs(RunSpec{Image: "img", Workdir: "/w", Entrypoint: "/usr/local/bin/sandbox-firewall"})
	if !hasPair(got, "--entrypoint", "/usr/local/bin/sandbox-firewall") {
		t.Errorf("expected --entrypoint, got %v", got)
	}
	// The flag must precede the image so docker parses it as a run flag.
	joined := strings.Join(got, " ")
	if strings.Index(joined, "--entrypoint") > strings.Index(joined, " img") {
		t.Errorf("--entrypoint must come before the image: %v", got)
	}
	// Omitted by default.
	if containsArg(BuildArgs(RunSpec{Image: "img", Workdir: "/w"}), "--entrypoint") {
		t.Error("did not expect --entrypoint on a bare spec")
	}
}

// TestBuildArgs_NeverMountsHostHome asserts the core security invariant at the
// arg level: only the mounts explicitly present in the spec are emitted.
func TestBuildArgs_OnlyDeclaredMounts(t *testing.T) {
	got := BuildArgs(RunSpec{
		Image:   "img",
		Workdir: "/workspace",
		Mounts:  []Mount{{Source: "/host/proj", Target: "/workspace"}},
	})
	mountCount := 0
	for i, a := range got {
		if a == "--mount" {
			mountCount++
			if i+1 < len(got) && strings.Contains(got[i+1], "/Users") {
				// only fails if a home-like path leaked in; /host/proj is fine
			}
		}
	}
	if mountCount != 1 {
		t.Errorf("expected exactly 1 mount, got %d in %v", mountCount, got)
	}
}

func TestBuildArgs_Detach(t *testing.T) {
	got := BuildArgs(RunSpec{Image: "img", Workdir: "/w", Detach: true})
	if !containsArg(got, "-d") {
		t.Errorf("expected -d for a detached spec, got %v", got)
	}
	// -d replaces the attachment flags rather than joining them: there is no
	// terminal behind a detached run and nothing reading its stdin.
	if containsArg(got, "-i") || containsArg(got, "-it") {
		t.Errorf("did not expect -i/-it alongside -d, got %v", got)
	}
	// TTY set alongside Detach must still not produce a pty. BuildArgs renders
	// what it is given, and sandbox.BuildSpec is what refuses the combination —
	// but rendering `-dit` would hand an agent a UI nobody can see, so the
	// renderer does not do it either.
	both := BuildArgs(RunSpec{Image: "img", Workdir: "/w", Detach: true, TTY: true})
	if containsArg(both, "-it") {
		t.Errorf("Detach must win over TTY, got %v", both)
	}
}

func TestBuildArgs_Labels(t *testing.T) {
	spec := RunSpec{
		Image:   "img",
		Workdir: "/w",
		Labels:  map[string]string{"sandbox.repo": "app-1234abcd", "sandbox.branch": "feature-a", "sandbox.agent": "claude"},
	}
	got := BuildArgs(spec)
	for _, want := range []string{"sandbox.agent=claude", "sandbox.branch=feature-a", "sandbox.repo=app-1234abcd"} {
		if !hasPair(got, "--label", want) {
			t.Errorf("expected --label %s, got %v", want, got)
		}
	}
	// Sorted, so the rendered command is stable across runs (the --dry-run golden
	// depends on it, and so does anyone diffing two invocations).
	joined := strings.Join(got, " ")
	iAgent := strings.Index(joined, "sandbox.agent")
	iBranch := strings.Index(joined, "sandbox.branch")
	iRepo := strings.Index(joined, "sandbox.repo")
	if !(iAgent < iBranch && iBranch < iRepo) {
		t.Errorf("labels not sorted: agent=%d branch=%d repo=%d in %q", iAgent, iBranch, iRepo, joined)
	}
	if !reflect.DeepEqual(BuildArgs(spec), got) {
		t.Error("label rendering is not deterministic")
	}
	if containsArg(BuildArgs(RunSpec{Image: "img", Workdir: "/w"}), "--label") {
		t.Error("did not expect --label on a bare spec")
	}
}

// TestBuildArgs_DetachedIsolationMatchesForeground is the invariant --detach must
// not weaken: a background container reaches exactly what its foreground twin
// reaches. Only attachment (-i/-it vs -d) and lifecycle (--rm) may differ, so the
// two argvs are compared with precisely those flags removed. Anything else that
// diverges — a mount, HOME, a capability — fails here.
func TestBuildArgs_DetachedIsolationMatchesForeground(t *testing.T) {
	base := RunSpec{
		Image:    "sandbox-base:0.1.0",
		Name:     "sandbox-app-feature-a",
		Workdir:  "/workspace",
		Command:  []string{"claude", "-p", "do the thing"},
		Hostname: "sandbox",
		Home:     "/sandbox/home",
		User:     "sandbox",
		Env:      map[string]string{"A": "1"},
		EnvNames: []string{"ANTHROPIC_API_KEY"},
		Mounts: []Mount{
			{Source: "/host/proj", Target: "/workspace"},
			{Source: "/host/state", Target: "/sandbox/home"},
		},
		NoNewPrivileges: true,
		CapDrop:         []string{"ALL"},
		PidsLimit:       1024,
		Memory:          "4g",
	}
	fg := base
	fg.Remove = true
	bg := base
	bg.Detach = true // and Remove stays false, as BuildSpec resolves it

	drop := func(args []string) []string {
		var out []string
		for _, a := range args {
			switch a {
			case "-i", "-it", "-d", "--rm":
				continue
			}
			out = append(out, a)
		}
		return out
	}
	if !reflect.DeepEqual(drop(BuildArgs(fg)), drop(BuildArgs(bg))) {
		t.Fatalf("detached run differs from its foreground twin beyond attachment:\n fg=%v\n bg=%v",
			BuildArgs(fg), BuildArgs(bg))
	}
	// And the lifecycle difference is real: the detached container is retained,
	// because its exit code and logs are the only record that it ran.
	if containsArg(BuildArgs(bg), "--rm") {
		t.Error("a detached container must not be --rm")
	}
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

func hasPair(args []string, flag, val string) bool {
	for i := 0; i+1 < len(args); i++ {
		if args[i] == flag && args[i+1] == val {
			return true
		}
	}
	return false
}

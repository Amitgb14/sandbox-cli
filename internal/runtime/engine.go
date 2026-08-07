package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"
)

// Engines differ in three places, and only three: how they answer questions
// about the host, how they isolate containers from each other, and what they
// call their binary. Everything else — the argv `BuildArgs` produces, the
// firewall the entrypoint programs, the mounts — is identical, which is why this
// is a small dialect rather than a second backend.
//
// The one thing that had to be measured rather than assumed: whether a rootless
// engine can program the egress firewall from inside the container. It can —
// nat, owner, conntrack and REDIRECT all succeed under rootless Podman — so the
// allowlist applies unchanged and there is no weaker mode to design.
type Engine string

const (
	EngineDocker Engine = "docker"
	EnginePodman Engine = "podman"
)

// NewEngine returns a backend for the named engine, defaulting to docker.
//
// An unknown name is not rejected here: it is treated as the binary to run, so a
// wrapper script or an unusual install path still works, and the dialect falls
// back to docker's. config.Validate is where a typo is caught.
func NewEngine(name string) *DockerCLI {
	if name == "" {
		name = string(EngineDocker)
	}
	return &DockerCLI{Bin: name, Stderr: os.Stderr}
}

// KnownEngine reports whether name is an engine sandbox-cli speaks.
func KnownEngine(name string) bool {
	return name == string(EngineDocker) || name == string(EnginePodman)
}

// EngineNames lists the engines, docker first because it is the default.
func EngineNames() []string { return []string{string(EngineDocker), string(EnginePodman)} }

// engine is the dialect in force, derived from the binary name so that pointing
// Bin at a podman path is enough. Explicitly setting Engine wins, for the case
// where the binary has been renamed or wrapped.
func (d *DockerCLI) engine() Engine {
	if d.Engine != "" {
		return d.Engine
	}
	// Match the base name, not the whole path: /opt/homebrew/bin/podman is podman.
	base := d.bin()
	if i := strings.LastIndexAny(base, "/\\"); i >= 0 {
		base = base[i+1:]
	}
	if strings.HasPrefix(base, "podman") {
		return EnginePodman
	}
	return EngineDocker
}

// IsPodman reports whether the podman dialect is in force.
func (d *DockerCLI) IsPodman() bool { return d.engine() == EnginePodman }

// seccompApplied asks the engine whether it applies a syscall filter.
//
// The two answer differently in kind, not just in field name: docker reports a
// list of security *option strings* to be searched, podman a boolean. Parsing
// one shape out of the other is why `docker info --format {{json .Runtimes}}`
// against podman fails with "can't evaluate field" rather than returning
// something misleading — which is the good failure, but it made every check read
// "cannot tell", and prod refuses on those.
func (d *DockerCLI) seccompApplied(ctx context.Context) (applied, known bool) {
	if d.engine() == EnginePodman {
		// Asked as JSON, not as a template field. Podman's Go struct name and its
		// JSON key differ — `{{.Host.Security.SeccompEnabled}}` and the lowercase
		// spelling both fail with "can't evaluate field", which reads as "the
		// daemon could not be asked" and, under prod, refuses the run. The JSON
		// key is stable and documented; the struct field name is neither.
		out, err := exec.CommandContext(ctx, d.bin(), "info", "--format", "{{json .Host.Security}}").Output()
		if err != nil {
			return false, false
		}
		var sec struct {
			SeccompEnabled *bool `json:"seccompEnabled"`
		}
		if json.Unmarshal(bytes.TrimSpace(out), &sec) != nil || sec.SeccompEnabled == nil {
			return false, false
		}
		return *sec.SeccompEnabled, true
	}
	out, err := exec.CommandContext(ctx, d.bin(), "info", "--format", "{{json .SecurityOptions}}").Output()
	if err != nil {
		return false, false
	}
	var opts []string
	if json.Unmarshal(bytes.TrimSpace(out), &opts) != nil {
		return false, false
	}
	return !seccompDisabled(opts), true
}

// runtimeNames lists the OCI runtimes the engine has registered.
//
// Podman reports the one it is using rather than a set, which is the honest
// answer for it: there is no registry to enumerate, so a single-element list is
// not a lossy translation of a docker map — it is what podman knows.
func (d *DockerCLI) runtimeNames(ctx context.Context) ([]string, error) {
	if d.engine() == EnginePodman {
		out, err := exec.CommandContext(ctx, d.bin(), "info", "--format", "{{.Host.OCIRuntime.Name}}").Output()
		if err != nil {
			return nil, err
		}
		name := strings.TrimSpace(string(out))
		if name == "" {
			return nil, fmt.Errorf("podman reported no OCI runtime")
		}
		return []string{name}, nil
	}
	out, err := exec.CommandContext(ctx, d.bin(), "info", "--format", "{{json .Runtimes}}").Output()
	if err != nil {
		return nil, err
	}
	return parseRuntimeNames(out)
}

// HostMemoryBytes reports how much memory the engine says the machine has, and
// whether it could be asked at all.
//
// "The machine" is the engine's machine, not this process's: on macOS and
// Windows the daemon runs in a VM with a memory budget of its own, and that
// budget — not the laptop's 64GB — is what a container's --memory cap competes
// for. Asking docker is therefore not a roundabout way of reading /proc; it is
// the only way to get the right number.
//
// The same JSON-key-versus-template-field split as everywhere else in this
// dialect: podman's Go struct names and its JSON keys disagree, so it is asked
// for JSON and read by key.
//
// A false second return means "could not be determined", never zero — a caller
// that treated an unknown host as an empty one would refuse every fleet.
func (d *DockerCLI) HostMemoryBytes(ctx context.Context) (int64, bool) {
	if d.engine() == EnginePodman {
		out, err := exec.CommandContext(ctx, d.bin(), "info", "--format", "{{json .Host}}").Output()
		if err != nil {
			return 0, false
		}
		var host struct {
			MemTotal *int64 `json:"memTotal"`
		}
		if json.Unmarshal(bytes.TrimSpace(out), &host) != nil || host.MemTotal == nil {
			return 0, false
		}
		return *host.MemTotal, *host.MemTotal > 0
	}
	out, err := exec.CommandContext(ctx, d.bin(), "info", "--format", "{{json .MemTotal}}").Output()
	if err != nil {
		return 0, false
	}
	var total int64
	if json.Unmarshal(bytes.TrimSpace(out), &total) != nil {
		return 0, false
	}
	return total, total > 0
}

// networkCreateArgs is how each engine is asked for a network no other sandbox
// can reach into.
//
// The two need opposite shapes, and the reason is a measured difference rather
// than a preference. Docker's `enable_icc=false` turns off traffic *between
// containers on the same bridge*, so one shared network is enough. netavark
// rejects that option outright, and its nearest-looking one — `isolate=true` —
// does something else: it blocks traffic between *different* networks while
// leaving same-network peers reachable. Verified by reading one container's data
// from another on an isolate=true network.
//
// So under podman each sandbox gets its own isolated network: no peers by
// construction, and `isolate` then stops it reaching sandboxes on other
// networks. Stronger than the docker arrangement, at the cost of a network per
// run rather than one shared one.
func (d *DockerCLI) networkCreateArgs(name string) []string {
	if d.engine() == EnginePodman {
		return []string{"network", "create", "--opt", "isolate=true", name}
	}
	return []string{"network", "create", "--opt", "com.docker.network.bridge.enable_icc=false", name}
}

// networkIsolated reports whether an existing network has the property it exists
// for, and whether that could be determined.
//
// Same question, two spellings: docker records enable_icc in Options, podman
// records isolate there.
func (d *DockerCLI) networkIsolated(ctx context.Context, name string) (isolated, known bool, raw string) {
	key := `{{index .Options "com.docker.network.bridge.enable_icc"}}`
	want := "false"
	if d.engine() == EnginePodman {
		key = `{{index .Options "isolate"}}`
		want = "true"
	}
	out, err := exec.CommandContext(ctx, d.bin(), "network", "inspect", name, "--format", key).Output()
	if err != nil {
		return false, false, ""
	}
	got := strings.TrimSpace(string(out))
	return got == want, true, got
}

// PerRunNetwork reports whether this engine needs a network of its own for each
// sandbox rather than one shared between them.
//
// True for podman, for the isolation reason above. Callers use it to decide
// whether the network's lifetime is the run's or the machine's.
func (d *DockerCLI) PerRunNetwork() bool { return d.engine() == EnginePodman }

// RemoveNetwork deletes a network, ignoring "no such network".
//
// Best-effort by design: a leaked network is untidy, a failed run because
// cleanup did not work is worse, and `sandbox-cli clean` reaps the rest.
func (d *DockerCLI) RemoveNetwork(ctx context.Context, name string) {
	if name == "" || name == SandboxNetwork {
		return // never remove the shared docker network from under another run
	}
	_ = exec.CommandContext(ctx, d.bin(), "network", "rm", name).Run()
}

// StrongerRuntimeSupport asks the daemon what it can offer in the way of a
// kernel of its own: which stronger runtimes it has registered, and whether it
// could be given one at all.
//
// **The daemon is asked, not the client.** The obvious version of this reads
// runtime.GOOS and calls Linux registrable — and it is wrong in the direction
// that matters, because the engine is not always where the client is. A macOS
// developer with DOCKER_HOST pointed at a Linux build box would have the demand
// waived on a host that could perfectly well satisfy it, silently, which is the
// opposite of what a boundary control should do when it is unsure.
//
// The registrability answer comes from what the engine says it *is*. Docker
// Desktop keeps every container in its own managed VM and does not allow
// registering a custom OCI runtime; it reports "Docker Desktop" as its
// operating system, which is the documented, stable way to recognise it.
// Anything else that answers is a daemon where a runtime can be installed.
//
// Known is false when the daemon could not be asked. Callers under prod refuse
// on that rather than assuming, for the reason enforceSeccomp does.
func (d *DockerCLI) StrongerRuntimeSupport(ctx context.Context) RuntimeSupport {
	names, err := d.Runtimes(ctx)
	if err != nil {
		return RuntimeSupport{}
	}
	var strong []string
	for _, n := range names {
		if StrongerRuntime(n) {
			strong = append(strong, n)
		}
	}
	sort.Strings(strong)
	if len(strong) > 0 {
		// Registrable is not asked when one is already registered: the evidence
		// answers the question, and an unanswerable follow-up would turn a host
		// that demonstrably supports it into an unknown.
		return RuntimeSupport{Registered: strong, Registrable: true, Known: true}
	}
	registrable, ok := d.runtimeRegistrable(ctx)
	if !ok {
		return RuntimeSupport{}
	}
	return RuntimeSupport{Registrable: registrable, Known: true}
}

// runtimeRegistrable reports whether this engine can be given an OCI runtime of
// its own, and whether it could be asked.
func (d *DockerCLI) runtimeRegistrable(ctx context.Context) (registrable, ok bool) {
	if d.engine() == EnginePodman {
		// Podman answers by JSON key rather than by template field name, as it
		// does for memory. A machine VM is remote from the client's point of
		// view, and that is the same situation Docker Desktop is in: the engine
		// lives in a VM whose image the user does not compose.
		//
		// Written from podman's documented `host.serviceIsRemote` and, unlike
		// the docker branch, not yet exercised against a live podman machine —
		// so an absent field reads as "could not be asked", which prod refuses,
		// rather than as a permissive default.
		out, err := exec.CommandContext(ctx, d.bin(), "info", "--format", "{{json .Host}}").Output()
		if err != nil {
			return false, false
		}
		var host struct {
			ServiceIsRemote *bool `json:"serviceIsRemote"`
		}
		if json.Unmarshal(bytes.TrimSpace(out), &host) != nil || host.ServiceIsRemote == nil {
			return false, false
		}
		return !*host.ServiceIsRemote, true
	}
	out, err := exec.CommandContext(ctx, d.bin(), "info", "--format", "{{.OperatingSystem}}").Output()
	if err != nil {
		return false, false
	}
	os := strings.TrimSpace(string(out))
	if os == "" {
		return false, false
	}
	return !strings.Contains(os, "Docker Desktop"), true
}

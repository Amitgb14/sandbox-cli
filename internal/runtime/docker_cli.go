package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/Amitgb14/sandbox-cli/internal/metrics"
)

// DockerCLI is the MVP Runtime backend. It shells out to the `docker` binary,
// inheriting the real stdio file descriptors so `docker run -it` handles pty /
// raw-mode natively — no pty library or manual attach/resize plumbing needed.
type DockerCLI struct {
	// Bin is the container engine executable name/path. Defaults to "docker";
	// set it to "podman" (or a path to one) to use that instead.
	Bin string
	// Engine overrides the dialect that would be inferred from Bin. Only needed
	// when the binary has been renamed or wrapped; see engine.go.
	Engine Engine
	// Stderr receives image-build progress and diagnostics. Defaults to os.Stderr.
	Stderr *os.File
	// builder builds a missing image; wired by the image package via SetBuilder
	// to avoid an import cycle. embeddedRef is the one reference that builder
	// knows how to produce — anything else must be pulled, not built.
	builder     Builder
	embeddedRef string
}

// NewDockerCLI returns a DockerCLI with sensible defaults.
func NewDockerCLI() *DockerCLI {
	return &DockerCLI{Bin: "docker", Stderr: os.Stderr}
}

func (d *DockerCLI) bin() string {
	if d.Bin == "" {
		return "docker"
	}
	return d.Bin
}

func (d *DockerCLI) stderr() *os.File {
	if d.Stderr == nil {
		return os.Stderr
	}
	return d.Stderr
}

// Available checks that docker is on PATH and the daemon is reachable.
func (d *DockerCLI) Available(ctx context.Context) error {
	if _, err := exec.LookPath(d.bin()); err != nil {
		return fmt.Errorf("%s not found on PATH: %w", d.engine(), err)
	}
	// The liveness probe has to be a field the engine actually has. `.ServerVersion`
	// is docker's; podman has no such key, so the template failed and a perfectly
	// healthy podman reported "daemon not reachable" — a wrong answer that read
	// like a real one.
	format := "{{.ServerVersion}}"
	hint := "is Docker Desktop running?"
	if d.engine() == EnginePodman {
		format = "{{.Version.Version}}"
		hint = "try `podman machine start`"
	}
	cmd := exec.CommandContext(ctx, d.bin(), "info", "--format", format)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s not reachable (%s): %s", d.engine(), hint, strings.TrimSpace(string(out)))
	}
	return nil
}

// EnsureImage checks whether ref exists locally. The actual build is delegated
// to a Builder set by the image package to avoid an import cycle; if none is
// set, a missing image is reported as an error.
func (d *DockerCLI) EnsureImage(ctx context.Context, ref string, forceBuild bool) error {
	if !forceBuild {
		insp := exec.CommandContext(ctx, d.bin(), "image", "inspect", ref)
		if err := insp.Run(); err == nil {
			return nil // already present
		}
	}
	if d.builder == nil {
		return fmt.Errorf("image %q not found locally and no builder configured", ref)
	}
	// Only *our* image is built from the embedded Dockerfile. Any other reference
	// is somebody else's image and has to be pulled: building it would tag the
	// sandbox base as, say, `node:22` in the user's local image store, poisoning
	// that name for every other project on the machine — and quietly handing back
	// something that is not what was asked for.
	if d.embeddedRef != "" && ref != d.embeddedRef {
		pull := exec.CommandContext(ctx, d.bin(), "pull", ref)
		pull.Stdout = d.stderr()
		pull.Stderr = d.stderr()
		if err := pull.Run(); err != nil {
			return fmt.Errorf("image %q is not present locally and could not be pulled: %w", ref, err)
		}
		return nil
	}
	return d.builder(ctx, ref)
}

// ImagePresent reports whether ref is already in the local image store, and
// whether that could be determined at all.
//
// The second return is not decoration. `image inspect` fails both when the image
// is absent and when the daemon cannot be reached, and those are different
// answers: "your first run will spend a few minutes building" is useful, "your
// first run will fail" is a different report entirely. A caller that collapsed
// them would tell someone with a stopped daemon to expect a slow build.
func (d *DockerCLI) ImagePresent(ctx context.Context, ref string) (present, known bool) {
	if err := exec.CommandContext(ctx, d.bin(), "image", "inspect", ref).Run(); err == nil {
		return true, true
	}
	// Absent and unreachable both land here, so ask a question only a reachable
	// daemon can answer to tell them apart.
	if err := exec.CommandContext(ctx, d.bin(), "image", "ls", "-q").Run(); err != nil {
		return false, false
	}
	return false, true
}

// Builder builds the given image reference. Set by the image package.
type Builder func(ctx context.Context, ref string) error

// SetBuilder wires a build function used by EnsureImage when an image is absent,
// together with the single image reference that function produces.
func (d *DockerCLI) SetBuilder(b Builder, embeddedRef string) {
	d.builder = b
	d.embeddedRef = embeddedRef
}

// resolveRuntime verifies the engine has the requested OCI runtime and returns
// the name to hand it: the engine's **own** key for that runtime.
//
// Returning a name rather than only a verdict is what makes matching by runtime
// safe. A containerd-backed daemon lists `io.containerd.runc.v2` while calling
// its default `runc`, so accepting `--runtime runc` there and then passing
// `runc` through to `docker run` would trade one failure for a worse one — the
// preflight goes quiet and the launch dies with docker's terse "unknown or
// invalid runtime name", which is precisely the message this check exists to
// replace. Whatever it accepts, it now names.
//
// Exact match first, normalised second. If the user typed a spelling the engine
// itself uses, that is the one to send; only when nothing matches literally does
// it fall back to asking which entry *means* the same runtime.
//
// Failing to ask the engine is non-fatal — the run proceeds and the engine
// surfaces its own error — so this only ever turns an already-broken run into a
// friendlier message. The default (empty) runtime is nobody's business here.
func (d *DockerCLI) resolveRuntime(ctx context.Context, name string) (string, error) {
	if name == "" {
		return name, nil
	}
	// runtimeNames rather than the docker template inline: podman does not have a
	// .Runtimes field at all, so asking for one there failed, returned nil, and
	// made this whole preflight silently inert on that engine.
	avail, err := d.runtimeNames(ctx)
	if err != nil || len(avail) == 0 {
		return name, nil
	}
	if match, ok := matchRuntime(name, avail); ok {
		return match, nil
	}
	if d.engine() == EnginePodman {
		// podman reports only the runtime it is *using*, so absence from that list
		// is not evidence of absence from the host. Refusing on it would fail every
		// run that named a second registered runtime — the same asymmetry doctor
		// records as RuntimeSupport.Complete.
		return name, nil
	}
	return name, runtimeHint(name, avail)
}

// matchRuntime picks the engine's own name for the requested runtime, or
// reports that it has none. Pure, so the whole table is testable without a
// daemon — which matters because the interesting hosts are the rare ones.
func matchRuntime(name string, avail []string) (string, bool) {
	for _, r := range avail {
		if r == name {
			return r, true
		}
	}
	for _, r := range avail {
		if SameRuntime(r, name) {
			return r, true
		}
	}
	return "", false
}

// parseRuntimeNames extracts the sorted runtime names from `docker info
// --format '{{json .Runtimes}}'` output (a JSON object keyed by runtime name).
func parseRuntimeNames(jsonOut []byte) ([]string, error) {
	var m map[string]json.RawMessage
	if err := json.Unmarshal(bytes.TrimSpace(jsonOut), &m); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(m))
	for k := range m {
		names = append(names, k)
	}
	sort.Strings(names)
	return names, nil
}

// runtimeHint returns nil if name is among avail, else a friendly, actionable
// error. Pure, so it is unit-tested without a daemon.
func runtimeHint(name string, avail []string) error {
	for _, r := range avail {
		// By runtime rather than by spelling: a containerd-backed daemon lists
		// io.containerd.runc.v2 while naming its default `runc`, and comparing the
		// two as strings told the user runc was not installed on a host running it.
		if SameRuntime(r, name) {
			return nil
		}
	}
	list := strings.Join(avail, ", ")
	if list == "" {
		list = "(none reported)"
	}
	return fmt.Errorf("runtime %q is not registered with the Docker daemon\n"+
		"  available runtimes: %s\n"+
		"  register it in /etc/docker/daemon.json (\"runtimes\": {%q: {\"path\": \"...\"}}) and restart docker,\n"+
		"  or install gVisor with `runsc install`. See docs/GUIDE.md → \"Stronger isolation\".",
		name, list, name)
}

// Run executes the spec and returns the guest exit code. A non-zero guest exit
// is returned as (code, nil); only failures to launch docker itself return err.
func (d *DockerCLI) Run(ctx context.Context, spec RunSpec) (int, error) {
	// Under podman the sandbox network belongs to this run alone, so it is
	// created here and removed on the way out. See perRunNetwork.
	cleanup, err := d.perRunNetwork(ctx, &spec)
	if err != nil {
		return 1, err
	}
	if cleanup != nil {
		defer cleanup()
	}

	// Pre-flight the requested OCI runtime so a typo or missing install fails with
	// a helpful hint instead of docker's terse "unknown or invalid runtime name".
	resolved, err := d.resolveRuntime(ctx, spec.Runtime)
	if err != nil {
		return 1, err
	}
	// The engine's own name for it, so a spelling it accepted here is the spelling
	// it is asked to run.
	spec.Runtime = resolved

	// A full-screen guest agent that crashes can leave the host terminal in a
	// mode it turned on by writing escape codes — mouse reporting, bracketed
	// paste, application cursor keys. docker restores the termios line discipline
	// but not these app-set DEC private modes, so a crashed agent leaves the
	// shell spewing mouse-report gibberish on every pointer move. Undo the common
	// leaks on the way out whenever we handed the guest a real terminal; each
	// sequence resets to the terminal default, so a clean exit that already
	// restored them is unaffected.
	if spec.TTY {
		defer restoreTerminalModes(os.Stdout)
	}

	args := BuildArgs(spec)
	cmd := exec.CommandContext(ctx, d.bin(), args...)
	cmd.Env = childEnv(spec)
	cmd.Stdin = os.Stdin

	if spec.Name != "" && spec.ShowMetrics {
		return d.runWithLiveGauge(cmd, spec)
	}
	if spec.Name != "" && spec.ShowSummary {
		return d.runWithSummary(cmd, spec)
	}

	cmd.Stdout = os.Stdout
	cmd.Stderr = newDenyTap(os.Stderr, spec.Denials)
	return exitCodeOf(cmd.Run())
}

// Start launches the spec in the background and returns the container's name,
// without waiting for the guest. The container outlives this process on purpose:
// its exit code and output are read back later with `docker inspect` / `docker
// logs`, which is what lets one terminal launch several agents.
//
// It refuses a non-detached spec rather than quietly launching one. BuildArgs
// would render `-i` for that spec, attaching stdin to a container nothing is
// reading from, and the caller would get a name back for a container that is
// waiting on input that will never come.
func (d *DockerCLI) Start(ctx context.Context, spec RunSpec) (string, error) {
	if !spec.Detach {
		return "", fmt.Errorf("Start requires a detached spec (RunSpec.Detach)")
	}
	// A detached run outlives this process, so its network must too — no cleanup
	// here. `sandbox-cli clean` reaps it along with the container.
	if _, err := d.perRunNetwork(ctx, &spec); err != nil {
		return "", err
	}
	resolved, err := d.resolveRuntime(ctx, spec.Runtime)
	if err != nil {
		return "", err
	}
	// The engine's own name for it, so a spelling it accepted here is the spelling
	// it is asked to run.
	spec.Runtime = resolved

	cmd := exec.CommandContext(ctx, d.bin(), BuildArgs(spec)...)
	cmd.Env = childEnv(spec)
	// `docker run -d` prints the container id on stdout and its own diagnostics on
	// stderr; the id is captured, the diagnostics belong to the user. Stdin stays
	// nil (/dev/null) — this run has no terminal behind it.
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = d.stderr()
	if err := cmd.Run(); err != nil {
		// docker has already written the reason to stderr; naming the container
		// here is what makes it actionable, since a detached name is deterministic
		// and a refusal usually means one is already running under it.
		if spec.Name != "" {
			return "", fmt.Errorf("starting detached container %s: %w", spec.Name, err)
		}
		return "", fmt.Errorf("starting detached container: %w", err)
	}

	// Prefer the name: it is the handle every later command addresses, and for a
	// detached run it is deterministic, so it stays true across invocations.
	if spec.Name != "" {
		return spec.Name, nil
	}
	return strings.TrimSpace(out.String()), nil
}

// runWithLiveGauge forwards the container's output through a sticky footer that
// shows a live resource gauge, erases the gauge on exit, and prints a summary.
func (d *DockerCLI) runWithLiveGauge(cmd *exec.Cmd, spec RunSpec) (int, error) {
	footer := metrics.NewTermFooter(os.Stderr)
	outR, outW := io.Pipe()
	errR, errW := io.Pipe()
	cmd.Stdout = outW
	cmd.Stderr = newDenyTap(errW, spec.Denials)

	var pumps sync.WaitGroup
	pumps.Add(2)
	go pump(&pumps, outR, os.Stdout, footer)
	go pump(&pumps, errR, os.Stderr, footer)

	meter := metrics.NewMeter(d.bin(), spec.Name, spec.Branch, footer)
	meter.Start()

	runErr := cmd.Run()

	// Drain forwarders, then stop the gauge and erase it.
	outW.Close()
	errW.Close()
	pumps.Wait()
	meter.Stop()

	printSummary(spec, meter)
	return exitCodeOf(runErr)
}

// runWithSummary keeps direct stdio (so an interactive TTY works) while sampling
// resource usage silently, then prints a one-line summary after the run.
func (d *DockerCLI) runWithSummary(cmd *exec.Cmd, spec RunSpec) (int, error) {
	cmd.Stdout = os.Stdout
	cmd.Stderr = newDenyTap(os.Stderr, spec.Denials)

	meter := metrics.NewMeter(d.bin(), spec.Name, spec.Branch, nil) // nil footer => silent
	meter.Start()
	runErr := cmd.Run()
	meter.Stop()

	printSummary(spec, meter)
	return exitCodeOf(runErr)
}

// printSummary writes the meter's summary line to stderr, if any was captured.
func printSummary(spec RunSpec, meter *metrics.Meter) {
	if !spec.ShowSummary {
		return
	}
	if s := meter.Summary(); s != "" {
		fmt.Fprintln(os.Stderr, "\033[2m"+s+"\033[0m")
	}
}

// pump forwards src to dst through the footer until src is exhausted.
func pump(wg *sync.WaitGroup, src io.Reader, dst *os.File, footer *metrics.TermFooter) {
	defer wg.Done()
	buf := make([]byte, 4096)
	for {
		n, err := src.Read(buf)
		if n > 0 {
			footer.Print(dst, buf[:n])
		}
		if err != nil {
			return
		}
	}
}

// terminalRestoreSeq disables the DEC private modes a full-screen guest app
// commonly turns on and may not turn off if it crashes: mouse reporting
// (?1000/?1002/?1003 plus the ?1006/?1015 report encodings) and bracketed paste
// (?2004). It also re-shows the cursor, restores normal cursor-key (?1l) and
// keypad (ESC >) modes, and clears any lingering SGR attributes. Every code
// resets a mode to its terminal default, so emitting the whole sequence after a
// clean exit — which already restored these — is a harmless no-op. It
// deliberately does not leave the alternate screen (?1049l): that has a cursor
// side effect on the normal buffer, so it would visibly disturb the common
// clean-exit case; recovering from a crash inside the alternate screen still
// wants a full `reset`.
const terminalRestoreSeq = "\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l\x1b[?1015l" +
	"\x1b[?2004l" + // bracketed paste
	"\x1b[?25h" + // show cursor
	"\x1b[?1l\x1b>" + // normal cursor keys + normal keypad
	"\x1b[0m" // clear SGR attributes

// restoreTerminalModes writes terminalRestoreSeq to w, but only when w is a real
// terminal — writing escape codes into a pipe or file would corrupt captured
// output.
func restoreTerminalModes(w *os.File) {
	if !isCharDevice(w) {
		return
	}
	io.WriteString(w, terminalRestoreSeq)
}

// isCharDevice reports whether f is a character device (a terminal), using only
// the standard library to honor the project's dependency constraint.
func isCharDevice(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

func exitCodeOf(err error) (int, error) {
	if err == nil {
		return 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return exitErr.ExitCode(), nil
	}
	return 1, fmt.Errorf("failed to run docker: %w", err)
}

// childEnv is the environment for the docker process: ours, plus the values for
// any bare `-e NAME` arguments BuildArgs emitted.
//
// Those values reach the container this way rather than through os.Setenv on
// sandbox-cli itself. That mattered: secrets used to be placed in our own
// environment, so a secret named PATH or DOCKER_HOST redirected the docker and
// git subprocesses spawned after the injection — and the preflight ran before
// it, so nothing noticed. Scoping them to the child narrows the class sharply:
// git and every later subprocess of ours are out of reach.
//
// It does not remove it, and the comment used to overstate this. The child is
// docker, so a value named DOCKER_HOST, DOCKER_CONFIG, DOCKER_CERT_PATH,
// DOCKER_TLS_VERIFY or DOCKER_CONTEXT still steers the docker client itself —
// at another daemon, or at a config.json naming credential helpers. Those names
// are refused by config.IsReservedEnv for that reason; this scoping is what
// makes the rest of the environment safe to forward.
//
// Returns nil when there is nothing extra, which makes exec inherit our
// environment exactly as before.
func childEnv(spec RunSpec) []string {
	if len(spec.ForwardedEnv) == 0 {
		return nil
	}
	env := os.Environ()
	for _, k := range sortedKeys(spec.ForwardedEnv) {
		env = append(env, k+"="+spec.ForwardedEnv[k])
	}
	return env
}

// SandboxNetwork is the docker network every sandbox joins.
//
// One shared network rather than one per run, which matters: a per-run network
// is a docker object that outlives a crash, so it would reintroduce exactly the
// orphaned-state cleanup this tool is otherwise free of. A single network is
// created once and reused forever.
//
// The point of it is `enable_icc=false`. On the default bridge every container
// can reach every other on any port — confirmed by reading workspace data out of
// one sandbox from another — and running several agents at once is the advertised
// workflow, so a compromised agent in one repository could dial into the agent
// working on another. With inter-container communication off they cannot see each
// other at all, while DNS, egress and published ports are unaffected.
const SandboxNetwork = "sandbox-cli"

// ownedNetwork is the one network sandbox-cli creates, and so the only one it is
// entitled to vouch for. A var rather than a direct use of the constant so tests
// can exercise the ICC check against a scratch network — the real one usually
// has live containers attached, and a test must not go near those.
var ownedNetwork = SandboxNetwork

// EnsureNetwork creates the shared sandbox network if it is not already there,
// and verifies that an existing one actually has the property it exists for.
//
// A failure is returned rather than swallowed: falling back to the default bridge
// would silently put the container somewhere every other container can reach,
// which is the condition this exists to prevent.
//
// The same argument applies to a network that merely *looks* right. This used to
// accept any network of the name, so one created by hand, by an older build, or
// by a compose file passed and was used as-is with inter-container communication
// left on — a silent fail-open in the exact layer being added. The point of the
// network is `enable_icc=false`; existence is not the property, so existence is
// not what is checked.
func (d *DockerCLI) EnsureNetwork(ctx context.Context, name string) error {
	if name == "" {
		return nil
	}
	// Only a network sandbox-cli creates is sandbox-cli's to vouch for.
	//
	// `none` is the other value that reaches here, and it is a *predefined*
	// network: inspect succeeds and its options are empty, which an
	// unconditional check read as "inter-container communication enabled" and
	// refused — telling the user to remove a network the engine will not remove.
	// It is also the right rule on its own terms: `none` gives the container no
	// interfaces at all, so isolation is not a property it can have, and a
	// network the user named themselves is theirs to configure.
	//
	// Under podman the name is per-run rather than the shared one, so ownership
	// is decided by prefix instead.
	if !d.ownsNetwork(name) {
		return nil
	}
	if isolated, known, raw := d.networkIsolated(ctx, name); known {
		if !isolated {
			return fmt.Errorf("the %s network %q exists but does not isolate containers from each other "+
				"(%s), so another container could reach this sandbox; remove it with `%s network rm %s` "+
				"and it will be recreated correctly",
				d.engine(), name, d.isolationOptionForMessage(raw), d.bin(), name)
		}
		return nil
	}
	out, err := exec.CommandContext(ctx, d.bin(), append([]string{}, d.networkCreateArgs(name)...)...).CombinedOutput()
	if err != nil {
		// A concurrent run may have created it between the inspect and the create;
		// that is success, not a race to report.
		if _, known, _ := d.networkIsolated(ctx, name); known {
			return nil
		}
		return fmt.Errorf("creating the sandbox network %q: %s: %w", name, strings.TrimSpace(string(out)), err)
	}
	return nil
}

// ownsNetwork reports whether this is a network sandbox-cli created and may
// therefore make claims about.
//
// Docker uses one shared network; podman needs one per sandbox, because
// netavark has no equivalent of enable_icc and its `isolate` option leaves
// same-network peers reachable. So under podman ownership is by prefix.
func (d *DockerCLI) ownsNetwork(name string) bool {
	if d.engine() == EnginePodman {
		return strings.HasPrefix(name, SandboxNetwork+"-")
	}
	return name == ownedNetwork
}

// isolationOptionForMessage renders the isolation option for a human. Both
// engines omit the key entirely when it was never set, which prints as empty and
// reads as though nothing is wrong — but unset *is* the permissive setting,
// which is the whole problem.
func (d *DockerCLI) isolationOptionForMessage(v string) string {
	key := "com.docker.network.bridge.enable_icc"
	if d.engine() == EnginePodman {
		key = "isolate"
	}
	if v == "" || v == "<no value>" {
		return key + " is unset, which means containers can reach each other"
	}
	return key + "=" + v
}

// networkICC reports the enable_icc option of an existing network. The error is
// how "no such network" is distinguished from "exists but unreadable" — only the

// iccValueForMessage renders the option for a human. Docker omits the key
// entirely when it was never set, which prints as an empty string and reads as
// though nothing is wrong — but an unset enable_icc *is* ICC on, which is the
// whole problem.
func iccValueForMessage(v string) string {
	if v == "" || v == "<no value>" {
		return "unset, which means enabled"
	}
	return v
}

// Seccomp is the syscall filter docker normally applies. sandbox-cli does not
// ship a profile of its own — docker's default is good, and maintaining a custom
// one is a large ongoing cost for a small marginal gain — so `Seccomp: ""` on the
// spec means "whatever the daemon does".
//
// The problem is that the daemon may do nothing, silently. On the machine this
// was found, `docker info` reported profile=unconfined and a container showed
// `Seccomp: 0` in /proc/self/status: the full syscall table available, plus
// unprivileged user namespaces (`unshare -r` gave uid 0). Everything sandbox-cli
// says about hardening still read as true while one of its layers was absent.
//
// So it is reported rather than assumed. Not refused: seccomp being off is a
// property of the user's docker installation, fixable in its settings, and
// refusing to run would make the tool unusable on a machine that is merely
// configured badly — a much worse trade than the firewall's fail-closed rule,
// where the thing that failed was something sandbox-cli itself asked for.

// seccompDisabled reports whether the daemon's own security options say no
// syscall filter is applied. Pure, so the parsing is tested without a daemon.
func seccompDisabled(securityOptions []string) bool {
	sawSeccomp := false
	for _, o := range securityOptions {
		if !strings.Contains(o, "name=seccomp") {
			continue
		}
		sawSeccomp = true
		if strings.Contains(o, "profile=unconfined") {
			return true
		}
	}
	// No seccomp entry at all means the daemon is not applying one either.
	return !sawSeccomp
}

// SeccompUnavailable reports whether the daemon applies no syscall filter, and
// whether that question could be answered at all.
//
// Separated from the warning because prod refuses on the same fact that dev
// merely reports. ok=false means the daemon could not be queried: dev stays
// silent rather than warning about a question it could not ask, and prod refuses
// rather than assuming the answer it would prefer.
func (d *DockerCLI) SeccompUnavailable(ctx context.Context) (unavailable, ok bool) {
	applied, known := d.seccompApplied(ctx)
	return !applied, known
}

// WarnIfSeccompDisabled prints one line when the engine applies no syscall
// filter. Best-effort: an engine that cannot be queried is not a reason to say
// anything, since the alternative is warning on every run for a question we
// could not ask.
//
// Goes through the dialect like everything else. It used to query docker's
// SecurityOptions directly, which errors under podman — so a podman host
// applying no filter got silence, which is exactly the "fails in the way that
// looks like an answer" this work is about.
func (d *DockerCLI) WarnIfSeccompDisabled(ctx context.Context) {
	applied, known := d.seccompApplied(ctx)
	if !known || applied {
		return
	}
	fmt.Fprintln(d.stderr(), "sandbox-cli: this "+string(d.engine())+" daemon applies no seccomp profile, so the container "+
		"has the full syscall table\n"+
		"  the other hardening still applies (non-root, cap-drop, no-new-privileges), but this layer is absent\n"+
		"  "+seccompRemedy(d.engine()))
}

// seccompRemedy names the fix for the engine in hand rather than assuming Docker
// Desktop, which is not even the common case on Linux.
func seccompRemedy(e Engine) string {
	if e == EnginePodman {
		return "check seccomp is enabled for your podman install (containers.conf `seccomp_profile`)"
	}
	return "on Docker Desktop: Settings > Docker Engine, remove \"seccomp-profile\": \"unconfined\""
}

// Runtimes lists the OCI runtimes the daemon has registered.
//
// Exported for the preflight: prod may carry untrusted agents, for which a
// shared kernel is not a boundary, so "is a stronger-isolation runtime actually
// available here" is a question about the host that has to be answerable before
// a run rather than discovered during one.
func (d *DockerCLI) Runtimes(ctx context.Context) ([]string, error) {
	return d.runtimeNames(ctx)
}

// FirewallProbe is the outcome of asking whether a container here can program
// the egress firewall.
type FirewallProbe int

const (
	// FirewallUnknown: the question could not be asked — no image to test with,
	// or the probe timed out. Distinct from Blocked on purpose: a caller that
	// treats "could not tell" as "broken" turns a slow daemon into a hard
	// refusal. Returned as a value rather than sniffed out of the reason string,
	// which used to be a substring match across a package boundary.
	//
	// It is the zero value deliberately. An unset field, a zero-valued struct, or
	// a future path that returns without deciding then reports "I do not know",
	// which prod refuses — rather than "this host is fine", which it would accept.
	// The rule for this subsystem is to fail closed, and an enum whose accidental
	// value is the permissive one does the opposite.
	FirewallUnknown FirewallProbe = iota
	// FirewallOK: the container programmed the rules the entrypoint needs.
	FirewallOK
	// FirewallBlocked: it could not. The reason says what failed.
	FirewallBlocked
)

// FirewallProgrammable reports whether a container on this daemon can actually
// program the egress firewall.
//
// This is the check that matters most on an unfamiliar host. Enforcing an
// allowlist means starting as root with NET_ADMIN and running iptables, then
// dropping privileges — and rootless Docker, a userns-remapped daemon and some
// CI runners cannot grant that. The firewall fails closed, so those runs abort
// rather than proceeding unfiltered, which is the right direction but a poor
// thing to discover from an unattended job at 3am.
//
// It is answered by trying it, in a throwaway container, because the failure is
// a property of the daemon's configuration rather than of anything queryable.
//
// The probe *writes* rather than lists. `iptables -L` only proves the table can
// be read, and a host that lists rules perfectly well can still lack the nat
// table, xt_REDIRECT, xt_owner or conntrack — every one of which the real
// entrypoint needs (`-t nat -N`, `-j REDIRECT`, `-m owner --uid-owner`,
// `-m conntrack`), and each of which is a separate kernel module. Passing
// a read-only probe and then failing the run is exactly the 3am failure this
// command exists to prevent, so the probe programs the same kinds of rule the
// entrypoint does. The container is --rm and its netns is its own, so the rules
// go away with it.
func (d *DockerCLI) FirewallProgrammable(ctx context.Context, image, runtimeName string) (FirewallProbe, string) {
	if image == "" {
		return FirewallUnknown, "no base image to test with"
	}
	if err := exec.CommandContext(ctx, d.bin(), "image", "inspect", image).Run(); err != nil {
		// The deadline can land here too, and reporting an unbuilt image for one
		// that exists sends the user to build something they already have.
		if ctx.Err() != nil {
			return FirewallUnknown, "the probe timed out"
		}
		return FirewallUnknown, "the base image is not built yet"
	}
	// `--entrypoint sh` rather than an absolute path: where iptables lives is the
	// Dockerfile's business, and hardcoding /usr/sbin/iptables here would turn a
	// packaging change into a reported host defect. This is a diagnostic, not a
	// privilege boundary, so resolving through the image's PATH is fine.
	//
	// Through sandbox-iptables for the backend, because the entrypoint chooses the
	// same way and a preflight that answers differently from the launch is worse
	// than no preflight. Asking bare `iptables` here reported "this host cannot
	// program the firewall" on exactly the gVisor hosts where the run works, and
	// advised turning the allowlist off to fix it.
	const probe = `set -e
IPT="$(sandbox-iptables)"
"$IPT" -t nat -N SANDBOX_DOCTOR
"$IPT" -A OUTPUT -m owner --uid-owner 0 -j ACCEPT
"$IPT" -A OUTPUT -m conntrack --ctstate ESTABLISHED -j ACCEPT
"$IPT" -t nat -A SANDBOX_DOCTOR -p tcp --dport 443 -j REDIRECT --to-ports 3128`
	// Under the runtime the run will actually use, not the engine's default.
	// Whether a container can program iptables is a property of the *kernel* it
	// gets, and those differ: gVisor serves only the legacy backend, and only
	// when installed with --net-raw. Probing the default runtime on a host whose
	// runs select runsc answers a question nobody asked, and answers it "fine".
	resolvedRuntime := ""
	if runtimeName != "" {
		// Through resolveRuntime, the same translation the run path makes. A
		// containerd-backed daemon lists `io.containerd.runc.v2` while a config
		// says `runtime: runc`, so sending the user's spelling verbatim made the
		// probe die on "unknown or invalid runtime name" and report that a host
		// whose runs program the firewall perfectly well cannot — the very
		// disagreement this probe was scoped to a runtime to prevent, inverted.
		resolved, rerr := d.resolveRuntime(ctx, runtimeName)
		if rerr != nil {
			// The engine does not have it. That is a fact about the *runtime*, not
			// about whether this host can filter, and checkRuntimes reports it
			// properly one line below. Answering "unknown" keeps this check from
			// blaming the daemon — and from advising that the egress allowlist be
			// switched off to fix a misspelled flag.
			return FirewallUnknown, "the runtime " + strconv.Quote(runtimeName) + " is not registered, so nothing could be probed on it"
		}
		resolvedRuntime = resolved
	}
	out, err := exec.CommandContext(ctx, d.bin(),
		firewallProbeArgs(image, resolvedRuntime, probe)...).CombinedOutput()
	if err != nil {
		// A cancelled or timed-out probe answered nothing. Reporting it as
		// "cannot program iptables" would fail prod for a question never asked.
		if ctx.Err() != nil {
			return FirewallUnknown, "the probe timed out"
		}
		return FirewallBlocked, strings.TrimSpace(lastLine(string(out)))
	}
	return FirewallOK, ""
}

// firewallProbeArgs is the probe's argv, pure so it can be asserted without a
// daemon.
//
// It is built here rather than by BuildArgs, so the --dry-run golden test does
// not cover it and nothing else would notice if the runtime stopped being sent —
// or if it were appended after the image, where docker hands it to the container
// as a command argument instead of using it.
func firewallProbeArgs(image, resolvedRuntime, probe string) []string {
	args := []string{"run", "--rm",
		"--cap-add", "NET_ADMIN", "--cap-add", "NET_RAW", "--user", "0",
		"--network", "none", "--entrypoint", "sh"}
	if resolvedRuntime != "" {
		args = append(args, "--runtime", resolvedRuntime)
	}
	// Every flag before the image reference; everything after it is the guest's.
	return append(args, image, "-c", probe)
}

// lastLine is the most useful part of a docker error, which is usually verbose
// above and specific at the end.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	return lines[len(lines)-1] // Split never returns an empty slice
}

// perRunNetwork gives a podman run its own isolated network and returns the
// cleanup that removes it.
//
// It rewrites spec.Network in place rather than making the caller decide,
// because which engine is in force is this layer's knowledge and nothing above
// it should have to branch on the difference. Returns a nil cleanup when the
// engine shares one network, which is docker.
//
// The name is derived from the container's, so an abandoned network is
// attributable to the run that leaked it and `clean` can reap it.
//
// A failure here refuses the run. There is no graceful degradation available:
// falling back to the shared name cannot work, because under podman nothing ever
// creates `sandbox-cli` — ownsNetwork returns false for that exact name, so
// EnsureNetwork no-ops on it — and the run would die on podman's own
// "network not found" with the real reason discarded. In the one case where the
// name *does* exist (a user made it, or a docker-era artifact), joining it is
// worse: ownsNetwork still returns false, so its isolation is never checked and
// the run silently gets the peer-reachability hole this design exists to close.
// Refusing is the only outcome consistent with failing closed.
func (d *DockerCLI) perRunNetwork(ctx context.Context, spec *RunSpec) (func(), error) {
	if !d.PerRunNetwork() || spec.Network != SandboxNetwork {
		return nil, nil
	}
	if spec.Name == "" {
		return nil, fmt.Errorf("cannot isolate this %s run: it has no container name to derive a network from, "+
			"and sharing one network would let another sandbox reach it", d.engine())
	}
	name := SandboxNetwork + "-" + spec.Name
	if err := d.EnsureNetwork(ctx, name); err != nil {
		return nil, fmt.Errorf("creating this run's isolated network: %w", err)
	}
	spec.Network = name
	return func() { d.RemoveNetwork(context.WithoutCancel(ctx), name) }, nil
}

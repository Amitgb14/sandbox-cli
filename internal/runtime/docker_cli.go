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
	"strings"
	"sync"

	"github.com/Amitgb14/sandbox-cli/internal/metrics"
)

// DockerCLI is the MVP Runtime backend. It shells out to the `docker` binary,
// inheriting the real stdio file descriptors so `docker run -it` handles pty /
// raw-mode natively — no pty library or manual attach/resize plumbing needed.
type DockerCLI struct {
	// Bin is the docker executable name/path. Defaults to "docker".
	Bin string
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
		return fmt.Errorf("docker not found on PATH: %w", err)
	}
	cmd := exec.CommandContext(ctx, d.bin(), "info", "--format", "{{.ServerVersion}}")
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("docker daemon not reachable (is Docker Desktop running?): %s", string(out))
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

// Builder builds the given image reference. Set by the image package.
type Builder func(ctx context.Context, ref string) error

// SetBuilder wires a build function used by EnsureImage when an image is absent,
// together with the single image reference that function produces.
func (d *DockerCLI) SetBuilder(b Builder, embeddedRef string) {
	d.builder = b
	d.embeddedRef = embeddedRef
}

// checkRuntime verifies the daemon has the requested OCI runtime registered.
// Failing to query the daemon is non-fatal — the run proceeds and docker
// surfaces its own error — so this only ever turns an already-broken run into a
// friendlier message. Returns nil for the default (empty) runtime.
func (d *DockerCLI) checkRuntime(ctx context.Context, name string) error {
	if name == "" {
		return nil
	}
	out, err := exec.CommandContext(ctx, d.bin(), "info", "--format", "{{json .Runtimes}}").Output()
	if err != nil {
		return nil // can't tell; let the run proceed and docker report
	}
	avail, err := parseRuntimeNames(out)
	if err != nil {
		return nil
	}
	return runtimeHint(name, avail)
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
		if r == name {
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
	// Pre-flight the requested OCI runtime so a typo or missing install fails with
	// a helpful hint instead of docker's terse "unknown or invalid runtime name".
	if err := d.checkRuntime(ctx, spec.Runtime); err != nil {
		return 1, err
	}

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
	cmd.Stderr = os.Stderr
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
	if err := d.checkRuntime(ctx, spec.Runtime); err != nil {
		return "", err
	}

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
	cmd.Stderr = errW

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
	cmd.Stderr = os.Stderr

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
	// Only the network sandbox-cli creates is sandbox-cli's to vouch for.
	//
	// `none` is the other value that reaches here, and it is a *predefined*
	// docker network: `network inspect` succeeds, its Options map is empty, so an
	// unconditional check read that as "ICC enabled" and refused to run — telling
	// the user to `docker network rm none`, which docker declines because the
	// network is predefined. That broke the strictest posture the tool has, and
	// the one the empty-allowlist refusal points people at.
	//
	// It is also the right rule on its own terms: `none` gives the container no
	// interfaces at all, so inter-container communication is not a property it
	// can have, and a network the user named themselves is theirs to configure.
	if name != ownedNetwork {
		return nil
	}
	if icc, err := d.networkICC(ctx, name); err == nil {
		if icc != "false" {
			return fmt.Errorf("the docker network %q exists but has inter-container communication enabled "+
				"(com.docker.network.bridge.enable_icc=%s), so another container could reach this sandbox; "+
				"remove it with `docker network rm %s` and it will be recreated correctly",
				name, iccValueForMessage(icc), name)
		}
		return nil
	}
	out, err := exec.CommandContext(ctx, d.bin(), "network", "create",
		"--opt", "com.docker.network.bridge.enable_icc=false", name).CombinedOutput()
	if err != nil {
		// A concurrent run may have created it between the inspect and the create;
		// that is success, not a race to report.
		if exec.CommandContext(ctx, d.bin(), "network", "inspect", name).Run() == nil {
			return nil
		}
		return fmt.Errorf("creating the sandbox network %q: %s: %w", name, strings.TrimSpace(string(out)), err)
	}
	return nil
}

// networkICC reports the enable_icc option of an existing network. The error is
// how "no such network" is distinguished from "exists but unreadable" — only the
// first is a reason to create one.
func (d *DockerCLI) networkICC(ctx context.Context, name string) (string, error) {
	out, err := exec.CommandContext(ctx, d.bin(), "network", "inspect", name,
		"--format", `{{index .Options "com.docker.network.bridge.enable_icc"}}`).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

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
	out, err := exec.CommandContext(ctx, d.bin(), "info", "--format", "{{json .SecurityOptions}}").Output()
	if err != nil {
		return false, false
	}
	var opts []string
	if json.Unmarshal(bytes.TrimSpace(out), &opts) != nil {
		return false, false
	}
	return seccompDisabled(opts), true
}

// WarnIfSeccompDisabled prints one line when the daemon applies no syscall
// filter. Best-effort: a daemon that cannot be queried is not a reason to say
// anything, since the alternative is warning on every run for a question we
// could not ask.
func (d *DockerCLI) WarnIfSeccompDisabled(ctx context.Context) {
	out, err := exec.CommandContext(ctx, d.bin(), "info", "--format", "{{json .SecurityOptions}}").Output()
	if err != nil {
		return
	}
	var opts []string
	if json.Unmarshal(bytes.TrimSpace(out), &opts) != nil {
		return
	}
	if !seccompDisabled(opts) {
		return
	}
	fmt.Fprintln(d.stderr(), "sandbox-cli: this docker daemon applies no seccomp profile, so the container "+
		"has the full syscall table\n"+
		"  the other hardening still applies (non-root, cap-drop, no-new-privileges), but this layer is absent\n"+
		"  Docker Desktop: Settings > Docker Engine, remove \"seccomp-profile\": \"unconfined\"")
}

// Runtimes lists the OCI runtimes the daemon has registered.
//
// Exported for the preflight: prod may carry untrusted agents, for which a
// shared kernel is not a boundary, so "is a stronger-isolation runtime actually
// available here" is a question about the host that has to be answerable before
// a run rather than discovered during one.
func (d *DockerCLI) Runtimes(ctx context.Context) ([]string, error) {
	out, err := exec.CommandContext(ctx, d.bin(), "info", "--format", "{{json .Runtimes}}").Output()
	if err != nil {
		return nil, err
	}
	return parseRuntimeNames(out)
}

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
// Returns a reason when it cannot.
func (d *DockerCLI) FirewallProgrammable(ctx context.Context, image string) (bool, string) {
	if image == "" {
		return false, "no base image to test with"
	}
	if err := exec.CommandContext(ctx, d.bin(), "image", "inspect", image).Run(); err != nil {
		return false, "base image is not built yet; run any sandbox command once, or `sandbox-cli run --build -- true`"
	}
	out, err := exec.CommandContext(ctx, d.bin(), "run", "--rm",
		"--cap-add", "NET_ADMIN", "--user", "0",
		"--entrypoint", "/usr/sbin/iptables", image, "-L", "-n").CombinedOutput()
	if err != nil {
		return false, strings.TrimSpace(lastLine(string(out)))
	}
	return true, ""
}

// lastLine is the most useful part of a docker error, which is usually verbose
// above and specific at the end.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimSpace(s), "\n")
	if len(lines) == 0 {
		return ""
	}
	return lines[len(lines)-1]
}

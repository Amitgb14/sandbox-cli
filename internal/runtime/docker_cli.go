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
	// to avoid an import cycle.
	builder Builder
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
	return d.builder(ctx, ref)
}

// Builder builds the given image reference. Set by the image package.
type Builder func(ctx context.Context, ref string) error

// SetBuilder wires a build function used by EnsureImage when an image is absent.
func (d *DockerCLI) SetBuilder(b Builder) { d.builder = b }

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

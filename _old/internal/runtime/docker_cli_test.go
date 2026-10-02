package runtime

import (
	"context"
	"io"
	"os"
	"strings"
	"testing"
)

func TestParseRuntimeNames(t *testing.T) {
	// Shape of `docker info --format '{{json .Runtimes}}'`.
	out := []byte(`{"io.containerd.runc.v2":{"path":"runc"},"runc":{"path":"runc"},"runsc":{"path":"/usr/local/bin/runsc"}}`)
	got, err := parseRuntimeNames(out)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"io.containerd.runc.v2", "runc", "runsc"} // sorted
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("parseRuntimeNames = %v, want %v", got, want)
	}
	if _, err := parseRuntimeNames([]byte("not json")); err == nil {
		t.Error("expected an error on malformed JSON")
	}
}

func TestRuntimeHint(t *testing.T) {
	// Registered runtime -> no error.
	if err := runtimeHint("runsc", []string{"runc", "runsc"}); err != nil {
		t.Errorf("registered runtime should pass, got %v", err)
	}
	// Unregistered -> actionable error naming the runtime and what's available.
	err := runtimeHint("kata-runtime", []string{"runc", "runsc"})
	if err == nil {
		t.Fatal("expected an error for an unregistered runtime")
	}
	msg := err.Error()
	for _, want := range []string{"kata-runtime", "not registered", "runc, runsc", "runsc install"} {
		if !strings.Contains(msg, want) {
			t.Errorf("hint missing %q; got: %s", want, msg)
		}
	}
	// Empty availability still yields a sensible message.
	if !strings.Contains(runtimeHint("x", nil).Error(), "(none reported)") {
		t.Error("expected a placeholder when no runtimes are reported")
	}
}

func TestTerminalRestoreSeqDisablesMouseReporting(t *testing.T) {
	// The reported crash symptom is the shell spewing SGR mouse reports, so the
	// restore sequence must at minimum turn mouse tracking back off. ?1003l is
	// "any-motion" tracking (the noisiest) and ?1006l its report encoding.
	for _, want := range []string{"\x1b[?1003l", "\x1b[?1006l", "\x1b[?2004l", "\x1b[?25h"} {
		if !strings.Contains(terminalRestoreSeq, want) {
			t.Errorf("restore sequence missing %q", want)
		}
	}
	// It must not leave the alternate screen: ?1049l has a cursor side effect
	// that would disturb every clean interactive exit.
	if strings.Contains(terminalRestoreSeq, "?1049") {
		t.Error("restore sequence should not touch the alternate screen (?1049)")
	}
}

func TestRestoreTerminalModesSkipsNonTerminal(t *testing.T) {
	// Writing escape codes into a pipe or file would corrupt captured output, so
	// the restore is a no-op unless the target is a real terminal.
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	restoreTerminalModes(w)
	w.Close()

	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("expected no write to a non-terminal, got %q", got)
	}
}

// TestEnsureImage_OnlyBuildsTheEmbeddedRef pins that a foreign image reference is
// never built from the embedded Dockerfile. Building it would tag the sandbox
// base as, say, `node:22` in the user's local image store — poisoning that name
// for every other project on the machine, and handing back something that is not
// the image they asked for.
func TestEnsureImage_OnlyBuildsTheEmbeddedRef(t *testing.T) {
	var built []string
	d := &DockerCLI{Bin: "/nonexistent-docker-for-test"}
	d.SetBuilder(func(ctx context.Context, ref string) error {
		built = append(built, ref)
		return nil
	}, "sandbox-base:0.0.1-abcdef12")

	// The embedded ref is built. (image inspect fails because the bin does not
	// exist, which is the "not present locally" path.)
	if err := d.EnsureImage(context.Background(), "sandbox-base:0.0.1-abcdef12", false); err != nil {
		t.Fatalf("the embedded ref must be buildable: %v", err)
	}
	if len(built) != 1 || built[0] != "sandbox-base:0.0.1-abcdef12" {
		t.Fatalf("builder calls = %v, want exactly the embedded ref", built)
	}

	// Anything else is pulled instead — which fails here, but must not build.
	if err := d.EnsureImage(context.Background(), "node:22", false); err == nil {
		t.Error("a foreign image that cannot be pulled should error, not silently build")
	}
	if len(built) != 1 {
		t.Errorf("builder was called for a foreign ref: %v", built)
	}
}

// TestSeccompDisabled covers the parsing behind the warning. sandbox-cli ships no
// profile of its own — docker's default is good and a custom one is a large
// ongoing cost — so `Seccomp: ""` means "whatever the daemon does". The daemon may
// do nothing, and on the machine where this was found it did: `docker info`
// reported profile=unconfined, a container showed `Seccomp: 0`, and `unshare -r`
// gave uid 0. Every claim sandbox-cli makes about hardening still read as true
// while one of its layers was simply absent.
func TestSeccompDisabled(t *testing.T) {
	disabled := [][]string{
		{"name=seccomp,profile=unconfined", "name=cgroupns"}, // observed in the wild
		{"name=cgroupns"}, // no seccomp entry at all
		{},                // daemon reports nothing
	}
	for _, opts := range disabled {
		if !seccompDisabled(opts) {
			t.Errorf("seccompDisabled(%v) = false, want true", opts)
		}
	}

	enabled := [][]string{
		{"name=seccomp,profile=builtin"},
		{"name=apparmor", "name=seccomp,profile=builtin", "name=cgroupns"},
		{"name=seccomp,profile=/etc/docker/seccomp.json"},
	}
	for _, opts := range enabled {
		if seccompDisabled(opts) {
			t.Errorf("seccompDisabled(%v) = true, want false — warning here would be noise", opts)
		}
	}
}

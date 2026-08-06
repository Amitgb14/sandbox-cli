package runtime

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestAttachRendersSigProxyFalse pins the one flag that decides whether looking
// at an agent can end it.
//
// Without --sig-proxy=false, `docker attach` forwards signals to the container:
// the Ctrl-C someone presses to stop *watching* becomes a SIGINT the agent
// receives, from a command whose entire purpose is to observe one. Deleting the
// flag breaks no behaviour this process can see — the client still exits, the
// listing is unchanged — so nothing catches it except an assertion on the argv.
//
// The integration suite cannot cover this on its own: cancelling a context
// SIGKILLs the client, which no signal-forwarding setting can intercept, so the
// guest survives either way. This test and
// `TestAttachAgainstDocker/ctrl-c_ends_the_looking,_not_the_run` are two halves
// of one claim — that the argv carries the flag, and that docker with the flag
// leaves the guest alone.
func TestAttachRendersSigProxyFalse(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the recorder is a shell script")
	}
	dir := t.TempDir()
	argvFile := filepath.Join(dir, "argv")
	fake := filepath.Join(dir, "fake-engine")
	script := "#!/bin/sh\nprintf '%s\\n' \"$@\" > " + argvFile + "\n"
	if err := os.WriteFile(fake, []byte(script), 0o755); err != nil {
		t.Fatalf("writing the recorder: %v", err)
	}

	if err := NewEngine(fake).Attach(context.Background(), "deadbeefcafe", nil, nil, nil); err != nil {
		t.Fatalf("Attach: %v", err)
	}

	recorded, err := os.ReadFile(argvFile)
	if err != nil {
		t.Fatalf("reading the recorded argv: %v", err)
	}
	args := strings.Fields(string(recorded))
	want := []string{"attach", "--sig-proxy=false", "deadbeefcafe"}
	if len(args) != len(want) {
		t.Fatalf("Attach argv = %v, want %v", args, want)
	}
	for i := range want {
		if args[i] != want[i] {
			t.Errorf("Attach argv[%d] = %q, want %q (full: %v)", i, args[i], want[i], args)
		}
	}
}

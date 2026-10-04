package cli

import (
	"io"
	"os"
	"strings"
	"testing"
	"time"
)

// captureStderr runs fn with os.Stderr redirected, and returns what it wrote.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	old := os.Stderr
	os.Stderr = w
	fn()
	os.Stderr = old
	w.Close()
	out, _ := io.ReadAll(r)
	return string(out)
}

// A warm start takes milliseconds and prints nothing; a slow one says what it
// is waiting for and why, once (stderr here is a pipe, not a terminal), and
// how long it took.
func TestProgressReportsOnlySlowSteps(t *testing.T) {
	old := progressDelay
	progressDelay = 50 * time.Millisecond
	t.Cleanup(func() { progressDelay = old })

	if out := captureStderr(t, func() { progress("creating the sandbox", "why")(true) }); out != "" {
		t.Errorf("a fast step printed %q", out)
	}
	out := captureStderr(t, func() {
		done := progress("creating the sandbox", "the first use of an image pulls it")
		time.Sleep(200 * time.Millisecond)
		done(true)
	})
	for _, want := range []string{"creating the sandbox… (the first use of an image pulls it)", "creating the sandbox took"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Count(out, "\n") != 2 || strings.Contains(out, "\r") {
		t.Errorf("not a terminal, so two plain lines and no ticks:\n%q", out)
	}
	out = captureStderr(t, func() {
		done := progress("creating the sandbox", "why")
		time.Sleep(200 * time.Millisecond)
		done(false)
	})
	if !strings.Contains(out, "creating the sandbox failed after") {
		t.Errorf("a failed step: %q", out)
	}
}

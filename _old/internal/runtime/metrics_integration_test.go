//go:build docker_integration

package runtime

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

func dockerUp() bool {
	return exec.Command("docker", "info").Run() == nil
}

// TestRunWithMetrics_ForwardsOutputAndExit drives the ShowMetrics path against a
// real container: it swaps os.Stdout/os.Stderr for pipes so the sticky footer
// and forwarded output are captured, then asserts the container's stdout arrives
// intact and the exit code is propagated. Also exercises the docker-stats
// sampler for the container's lifetime.
func TestRunWithMetrics_ForwardsOutputAndExit(t *testing.T) {
	if !dockerUp() {
		t.Skip("docker daemon not available")
	}

	origOut, origErr := os.Stdout, os.Stderr
	outR, outW, _ := os.Pipe()
	errR, errW, _ := os.Pipe()
	os.Stdout, os.Stderr = outW, errW

	var buf, errBuf bytes.Buffer
	var drain sync.WaitGroup
	drain.Add(2)
	go func() { defer drain.Done(); io.Copy(&buf, outR) }()
	go func() { defer drain.Done(); io.Copy(&errBuf, errR) }() // footer / gauge text

	spec := RunSpec{
		Image:       "alpine",
		Name:        "sandbox-metrics-test-" + time.Now().Format("150405.000"),
		Workdir:     "/",
		Command:     []string{"sh", "-c", "echo LINE_A; echo LINE_B; sleep 2; echo LINE_C"},
		Remove:      true,
		Home:        "/sandbox/home",
		ShowMetrics: true,
	}

	code, err := NewDockerCLI().Run(context.Background(), spec)

	os.Stdout, os.Stderr = origOut, origErr
	outW.Close()
	errW.Close()
	drain.Wait()

	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	out := buf.String()
	for _, want := range []string{"LINE_A", "LINE_B", "LINE_C"} {
		if !strings.Contains(out, want) {
			t.Errorf("forwarded output missing %q; got:\n%s", want, out)
		}
	}
	if !(strings.Index(out, "LINE_A") < strings.Index(out, "LINE_C")) {
		t.Errorf("output out of order:\n%s", out)
	}
	// The live gauge text must have been drawn to stderr.
	if es := errBuf.String(); !strings.Contains(es, "sandbox-cli") {
		t.Errorf("gauge text not drawn to stderr; got: %q", es)
	}
}

// TestRunWithSummary_PrintsPeak drives the summary-only path (as used for
// interactive runs): direct stdio, a silent sampler, and a one-line peak summary
// printed to stderr after the container exits.
func TestRunWithSummary_PrintsPeak(t *testing.T) {
	if !dockerUp() {
		t.Skip("docker daemon not available")
	}

	origErr := os.Stderr
	errR, errW, _ := os.Pipe()
	os.Stderr = errW
	var errBuf bytes.Buffer
	var drain sync.WaitGroup
	drain.Add(1)
	go func() { defer drain.Done(); io.Copy(&errBuf, errR) }()

	spec := RunSpec{
		Image:   "alpine",
		Name:    "sandbox-summary-test-" + time.Now().Format("150405.000"),
		Workdir: "/",
		// Long enough for at least one `docker stats` sample (~2s each) to land.
		Command:     []string{"sh", "-c", "sleep 6"},
		Remove:      true,
		Home:        "/sandbox/home",
		ShowSummary: true, // interactive-style: summary but no live gauge
	}

	code, err := NewDockerCLI().Run(context.Background(), spec)

	os.Stderr = origErr
	errW.Close()
	drain.Wait()

	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	if es := errBuf.String(); !strings.Contains(es, "peak mem") {
		t.Errorf("summary line not printed; got: %q", es)
	}
}

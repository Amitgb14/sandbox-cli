//go:build unix

package cli

import (
	"context"
	"errors"
	"net/http/httptest"
	"os"
	"os/signal"
	"syscall"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/backend/fake"
	"github.com/Amitgb14/sandbox-cli/internal/server"
	"github.com/Amitgb14/sandbox-cli/internal/spec"
)

// Ctrl-C while watching a detached run detaches and leaves the run alone; in
// a run started in the foreground it stops the process, as it would locally.
func TestInterruptDetachesAWatcherAndStopsAForegroundRun(t *testing.T) {
	// Caught here first, so an interrupt that lands before attach is listening
	// cannot kill the test binary.
	guard := make(chan os.Signal, 8)
	signal.Notify(guard, os.Interrupt)
	defer signal.Stop(guard)

	srv := httptest.NewServer((&server.Server{Backend: fake.New(api.CapEgressAllowlist), Policy: spec.DefaultPolicyFor(fake.New(api.CapEgressAllowlist).Capabilities())}).Handler())
	defer srv.Close()
	c, _ := api.NewClient(srv.URL, "")
	ctx := context.Background()

	run := func(forward bool) (error, api.Process) {
		sb, err := c.CreateSandbox(ctx, api.CreateSandboxRequest{})
		if err != nil {
			t.Fatal(err)
		}
		p, err := c.StartProcess(ctx, sb.ID, api.RunRequest{Argv: []string{"sleep", "30"}})
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan error, 1)
		go func() { _, err := attach(ctx, c, sb.ID, p.PID, false, forward); done <- err }()
		tick := time.NewTicker(50 * time.Millisecond)
		defer tick.Stop()
		deadline := time.After(10 * time.Second)
		for {
			select {
			case err := <-done:
				time.Sleep(100 * time.Millisecond) // let a forwarded signal land
				info, _ := c.Process(ctx, sb.ID, p.PID)
				return err, info
			case <-tick.C:
				_ = syscall.Kill(os.Getpid(), syscall.SIGINT)
			case <-deadline:
				t.Fatal("attach never returned")
			}
		}
	}

	err, info := run(false)
	if !errors.Is(err, errDetached) {
		t.Errorf("watching: %v, want errDetached", err)
	}
	if info.State != api.ProcessRunning {
		t.Errorf("watching: the interrupt reached the process (%s)", info.State)
	}

	err, info = run(true)
	if errors.Is(err, errDetached) {
		t.Error("a foreground run detached instead of passing the interrupt on")
	}
	if info.State == api.ProcessRunning {
		t.Error("a foreground run's interrupt did not reach the process")
	}
}

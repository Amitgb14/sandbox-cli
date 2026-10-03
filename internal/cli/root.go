// Package cli is cobra wiring and nothing else.
//
// It builds spec.Options from flags and hands them on. It never builds an argv
// and never decides what the sandbox may reach: isolation lives in spec and
// backend/oci, which is the one rule the old tree kept by convention and this
// one keeps by layout.
package cli

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

// NewRootCmd assembles the command tree.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "sandbox-cli",
		Short:         "Isolated microVM sandboxes: run any command on your Mac, a Linux machine you control, or the cloud",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(
		newVersionCmd(), newContextCmd(), newRunCmd(), newListCmd(), newAttachCmd(),
		newLogsCmd(), newKillCmd(), newBringBackCmd(), newRecoverCmd(), newMirrorCmd(), newEventsCmd(), newDoctorCmd(), newTunnelCmd(),
	)
	root.AddCommand(newSuspendCmds()...)
	root.AddCommand(newAgentGroupCmd(), newVolumeCmd(), newStudioCmd())
	return root
}

// Execute runs the root command and returns the process exit code.
func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM)
	defer stop()
	if err := NewRootCmd().ExecuteContext(ctx); err != nil {
		var ee exitError
		if errors.As(err, &ee) {
			return ee.code // the guest's exit status, mirrored
		}
		fmt.Fprintln(os.Stderr, "sandbox-cli: "+err.Error())
		// An error that is not a failure says so with its own code, after
		// its message: `agent wait` timing out is 2, not the 1 of a broken run.
		var coded interface{ ExitCode() int }
		if errors.As(err, &coded) {
			return coded.ExitCode()
		}
		return 1
	}
	return 0
}

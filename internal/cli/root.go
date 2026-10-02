// Package cli is cobra wiring and nothing else.
//
// It builds spec.Options from flags and hands them on. It never builds an argv
// and never decides what the sandbox may reach: isolation lives in spec and
// backend/oci, which is the one rule the old tree kept by convention and this
// one keeps by layout.
package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

// NewRootCmd assembles the command tree.
func NewRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:           "sandbox-cli",
		Short:         "Run AI coding agents inside a disposable, isolated sandbox",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(newVersionCmd())
	return root
}

// Execute runs the root command and returns the process exit code.
func Execute() int {
	if err := NewRootCmd().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "sandbox-cli: "+err.Error())
		return 1
	}
	return 0
}

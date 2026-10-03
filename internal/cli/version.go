package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/version"
)

// The base image tag (image.Ref, a hash of the embedded Dockerfile) comes back
// with the image package in M2; until then only the version is printed.
func newVersionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the sandbox-cli version",
		RunE: func(cmd *cobra.Command, args []string) error {
			fmt.Fprintf(cmd.OutOrStdout(), "sandbox-cli %s\n", version.Version)
			return nil
		},
	}
}

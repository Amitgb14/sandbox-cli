package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
)

// newResizeCmd changes a running sandbox's size. The server restarts it to
// do so, so the help says what is kept and what is not.
func newResizeCmd() *cobra.Command {
	var ctxFlag string
	var req api.ResizeSandboxRequest
	cmd := &cobra.Command{
		Use:   "resize SANDBOX",
		Short: "Give a running sandbox more or fewer CPUs and memory, or a larger disk",
		Long: "The sandbox restarts at its new size, in a few seconds: it keeps its id, its\n" +
			"network, its files and its volumes; its processes end, so it is refused while\n" +
			"any run. A size left out is kept. A disk only grows. Through a gateway what it\n" +
			"adds counts against your quota, and the sandbox's node must have the room.",
		Example: "  sandbox-cli resize my-box --cpus 4 --memory 8192\n  sandbox-cli resize my-box --disk 20480",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if req == (api.ResizeSandboxRequest{}) {
				return errors.New("name a new size: --cpus, --memory or --disk")
			}
			c, _, err := newClient(ctxFlag)
			if err != nil {
				return err
			}
			sb, err := c.Resize(cmd.Context(), args[0], req)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %v CPUs, %d MiB memory, %d MiB disk\n", termsafe.Clean(sb.ID), sb.CPUs, sb.MemoryMB, sb.DiskMB)
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&ctxFlag, "context", "", "which sandboxd to use")
	f.Float64Var(&req.CPUs, "cpus", 0, "vCPUs")
	f.IntVar(&req.MemoryMB, "memory", 0, "memory in MiB")
	f.IntVar(&req.DiskMB, "disk", 0, "writable disk in MiB (only grows)")
	return cmd
}

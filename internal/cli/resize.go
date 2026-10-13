package cli

import (
	"errors"
	"fmt"
	"io"

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
			out := cmd.OutOrStdout()
			before, err := c.Sandbox(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			after, rerr := c.Resize(cmd.Context(), before.ID, req)
			if rerr != nil {
				// A resize can fail part way — the disk not grown, the CPUs
				// and memory changed — so say what the sandbox is now.
				if now, err := c.Sandbox(cmd.Context(), before.ID); err == nil {
					fmt.Fprintf(out, "%s: not fully resized; it is now:\n", termsafe.Clean(now.ID))
					printSizes(out, before, now)
				}
				return rerr
			}
			if sizeOf(before) == sizeOf(after) {
				fmt.Fprintf(out, "%s: already that size; not restarted\n", termsafe.Clean(after.ID))
			} else {
				fmt.Fprintf(out, "%s: resized and restarted\n", termsafe.Clean(after.ID))
			}
			printSizes(out, before, after)
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

func sizeOf(sb api.Sandbox) api.NodeResources {
	return api.NodeResources{CPUs: sb.CPUs, MemoryMB: sb.MemoryMB, DiskMB: sb.DiskMB}
}

// printSizes shows each size as it was and as it is, so a user sees what
// changed and what did not.
func printSizes(w io.Writer, was, now api.Sandbox) {
	line := func(name, a, b string) {
		if a == b {
			fmt.Fprintf(w, "  %-7s %s (unchanged)\n", name, b)
		} else {
			fmt.Fprintf(w, "  %-7s %s -> %s\n", name, a, b)
		}
	}
	line("CPUs", fmt.Sprint(was.CPUs), fmt.Sprint(now.CPUs))
	line("memory", fmt.Sprintf("%d MiB", was.MemoryMB), fmt.Sprintf("%d MiB", now.MemoryMB))
	line("disk", fmt.Sprintf("%d MiB", was.DiskMB), fmt.Sprintf("%d MiB", now.DiskMB))
}

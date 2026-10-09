package cli

import (
	"fmt"
	"strconv"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/studio"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
)

// newTemplateCmd lists the sizes --template takes. Templates are made and
// changed in Studio's Templates screen; this side only reads them, so the
// file has one writer.
func newTemplateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "template",
		Short: "Sizes a sandbox can be launched at, by name (run --template)",
		Long: "A template is a size — vCPUs, memory and disk — by name: micro, small, medium,\n" +
			"large and xlarge are built in, and Studio's Templates screen saves more, in\n" +
			"~/.config/sandbox/studio.json. Launch at one with `sandbox-cli run --template\n" +
			"NAME` or `sandbox-cli agent <name> --template NAME`; --cpus, --memory and --disk\n" +
			"given beside it win for their own field. sandboxd's limits still apply.",
	}
	ls := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List the templates: built in, then those saved in Studio",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ts, err := studio.Templates()
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tVCPUS\tMEMORY\tDISK\tSOURCE\tDESCRIPTION")
			for _, t := range ts {
				disk, source, desc := "default", "saved", "-"
				if t.DiskMB > 0 {
					disk = mib(t.DiskMB)
				}
				if t.Builtin {
					source = "built in"
				}
				if t.Description != "" {
					// Typed into a browser: printed as text, never as escapes.
					desc = termsafe.Clean(t.Description)
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%s\n", termsafe.Clean(t.Name),
					strconv.FormatFloat(t.CPUs, 'f', -1, 64), mib(t.MemoryMB), disk, source, desc)
			}
			return tw.Flush()
		},
	}
	cmd.AddCommand(ls)
	return cmd
}

// mib is a size in MiB as people read it: 512 MiB, 2 GiB, 1.5 GiB.
func mib(n int) string {
	if n >= 1024 {
		return strconv.FormatFloat(float64(n)/1024, 'f', -1, 64) + " GiB"
	}
	return strconv.Itoa(n) + " MiB"
}

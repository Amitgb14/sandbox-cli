package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
)

func newVolumeCmd() *cobra.Command {
	var ctxFlag string
	cmd := &cobra.Command{
		Use:   "volume",
		Short: "Named volumes: filesystems that outlive the sandboxes they are mounted in",
		Long: "A volume keeps what a sandbox wrote for the next one that mounts it: a package\n" +
			"cache, a dataset, a model's weights. Mount one with\n" +
			"`sandbox-cli run --volume NAME:/path[:ro]`. A volume is mounted in one live\n" +
			"sandbox at a time, never in /workspace or a system directory.",
	}
	cmd.PersistentFlags().StringVar(&ctxFlag, "context", "", "which sandboxd to use")

	var size int
	create := &cobra.Command{
		Use:   "create NAME",
		Short: "Create an empty volume",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := newClient(ctxFlag)
			if err != nil {
				return err
			}
			v, err := c.CreateVolume(cmd.Context(), api.CreateVolumeRequest{Name: args[0], SizeMB: size})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s (%d MiB)\n", v.Name, v.SizeMB)
			return nil
		},
	}
	create.Flags().IntVar(&size, "size", 0, "size in MiB (default: the server's default disk size)")

	ls := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List volumes and where each is mounted",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := newClient(ctxFlag)
			if err != nil {
				return err
			}
			vols, err := c.Volumes(cmd.Context())
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tSIZE\tCREATED\tATTACHED TO")
			for _, v := range vols {
				at := v.AttachedTo
				if at == "" {
					at = "-"
				}
				fmt.Fprintf(tw, "%s\t%d MiB\t%s\t%s\n", termsafe.Clean(v.Name), v.SizeMB, v.CreatedAt.Local().Format(time.DateTime), at)
			}
			return tw.Flush()
		},
	}

	rm := &cobra.Command{
		Use:   "rm NAME...",
		Short: "Delete volumes and everything on them; refused while mounted",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := newClient(ctxFlag)
			if err != nil {
				return err
			}
			for _, name := range args {
				if err := c.DeleteVolume(cmd.Context(), name); err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
			}
			return nil
		},
	}
	cmd.AddCommand(create, ls, rm)
	return cmd
}

// parseVolumeFlag reads --volume NAME:/path[:ro]. The server checks the name
// and the path; this only splits them.
func parseVolumeFlag(s string) (api.VolumeMount, error) {
	f := strings.Split(s, ":")
	switch {
	case len(f) == 2:
		return api.VolumeMount{Name: f[0], Path: f[1]}, nil
	case len(f) == 3 && f[2] == "ro":
		return api.VolumeMount{Name: f[0], Path: f[1], ReadOnly: true}, nil
	case len(f) == 3 && f[2] == "rw":
		return api.VolumeMount{Name: f[0], Path: f[1]}, nil
	}
	return api.VolumeMount{}, fmt.Errorf("--volume %q: want NAME:/path or NAME:/path:ro", s)
}

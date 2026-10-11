package cli

import (
	"errors"
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
)

// newGatewayImagesCmd manages the images on a gateway's nodes: sandbox-cli
// image, once for the fleet, for its admin.
func newGatewayImagesCmd(ctxFlag *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "images",
		Short: "Images on the gateway's nodes: list, download ahead of use, remove (admin key)",
		Long: "The images the gateway's nodes start sandboxes from, once for the fleet. A pull\n" +
			"goes to every node that takes new sandboxes (healthy, not cordoned), each as\n" +
			"its own policy allows. A removal is refused, removing nothing, while any node\n" +
			"still needs the image (a sandbox starts from it, or it is the node's default\n" +
			"or pooled image), and then takes it from every node that has it.",
		Example: "  sandbox-cli gateway images pull ghcr.io/amitgb14/sandbox-desktop:edge\n" +
			"  sandbox-cli gateway images ls\n" +
			"  sandbox-cli gateway images rm python:3.13-slim",
	}

	ls := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List the nodes' images, and which nodes have each",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := requireGateway(cmd.Context(), *ctxFlag, "gateway images")
			if err != nil {
				return err
			}
			list, err := c.AdminImages(cmd.Context())
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "IMAGE\tSTATE\tSIZE\tIN USE\tNODES\tNOTE")
			for _, img := range list {
				size := "-"
				if img.Bytes > 0 {
					size = mib(int(img.Bytes >> 20))
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n", termsafe.Clean(img.Image), imageState(img), size, img.InUse, imageNodes(img), imageNote(img))
			}
			return tw.Flush()
		},
	}

	pull := &cobra.Command{
		Use:     "pull IMAGE",
		Aliases: []string{"install"},
		Short:   "Have every node that takes new sandboxes download and install an image",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := requireGateway(cmd.Context(), *ctxFlag, "gateway images")
			if err != nil {
				return err
			}
			img, err := c.AdminInstallImage(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			for _, n := range img.Nodes {
				if n.Error != "" {
					fmt.Fprintf(cmd.ErrOrStderr(), "sandbox-cli: %s: %s\n", termsafe.Clean(n.Node), termsafe.Clean(n.Error))
				}
			}
			fmt.Fprintf(cmd.OutOrStdout(), "installing %s on %s; sandbox-cli gateway images ls shows how far it has got\n",
				termsafe.Clean(args[0]), imageNodes(img))
			return nil
		},
	}

	rm := &cobra.Command{
		Use:   "rm IMAGE...",
		Short: "Remove images from every node, where no node still needs them",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := requireGateway(cmd.Context(), *ctxFlag, "gateway images")
			if err != nil {
				return err
			}
			var failed error
			for _, ref := range args {
				out, err := c.AdminRemoveImage(cmd.Context(), ref)
				if err != nil {
					fmt.Fprintf(cmd.ErrOrStderr(), "sandbox-cli: %s: %v\n", termsafe.Clean(ref), err)
					failed = errors.New("not every image was removed")
					continue
				}
				freed := ""
				if out.FreedBytes > 0 {
					freed = fmt.Sprintf(", freeing %s", mib(int(out.FreedBytes>>20)))
				}
				fmt.Fprintf(cmd.OutOrStdout(), "removed %s%s\n", termsafe.Clean(ref), freed)
			}
			return failed
		},
	}
	cmd.AddCommand(ls, pull, rm)
	return cmd
}

// imageNodes is where an image is, as a column reads it: the nodes that
// have it, each with its own state where that is not installed.
func imageNodes(img api.Image) string {
	var out []string
	for _, n := range img.Nodes {
		s := termsafe.Clean(n.Node)
		switch {
		case n.Error != "":
			s += " (refused)"
		case n.State != "" && n.State != api.ImageInstalled:
			s += " (" + n.State + ")"
		}
		out = append(out, s)
	}
	if len(out) == 0 {
		return "-"
	}
	return strings.Join(out, ",")
}

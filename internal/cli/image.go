package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
)

// newImageCmd manages the images a sandboxd starts sandboxes from: its
// operator's, so a gateway answers these as unsupported.
func newImageCmd() *cobra.Command {
	var ctxFlag string
	cmd := &cobra.Command{
		Use:   "image",
		Short: "Images a sandboxd starts sandboxes from: list, download ahead of use, remove",
		Long: "A sandboxd pulls an image the first time a sandbox asks for it. These let its\n" +
			"operator do that ahead of time, see what is installed and what uses it, and\n" +
			"remove what nothing does. On Linux an install also builds the image's root\n" +
			"disk, which is most of the first sandbox's wait. The policy's images list, where\n" +
			"there is one, limits what may be installed. Through a gateway they are not\n" +
			"offered: a node's images are its operator's.",
	}
	cmd.PersistentFlags().StringVar(&ctxFlag, "context", "", "which sandboxd to use")

	ls := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List installed images, those installing, and those that failed",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := newClient(ctxFlag)
			if err != nil {
				return err
			}
			list, err := c.Images(cmd.Context())
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "IMAGE\tSTATE\tSIZE\tIN USE\tINSTALLED\tNOTE")
			for _, img := range list {
				size, when := "-", "-"
				if img.Bytes > 0 {
					size = mib(int(img.Bytes >> 20))
				}
				if img.InstalledAt != nil {
					when = img.InstalledAt.Local().Format(time.DateTime)
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%s\t%s\n", termsafe.Clean(img.Image), imageState(img), size, img.InUse, when, imageNote(img))
			}
			return tw.Flush()
		},
	}

	var noWait bool
	pull := &cobra.Command{
		Use:   "pull IMAGE",
		Short: "Download and install an image ahead of the first sandbox that wants it",
		Example: "  sandbox-cli image pull ghcr.io/amitgb14/sandbox-desktop:edge\n" +
			"  sandbox-cli image pull --no-wait python:3.13-slim",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := newClient(ctxFlag)
			if err != nil {
				return err
			}
			ref := args[0]
			if _, err := c.InstallImage(cmd.Context(), ref); err != nil {
				return err
			}
			if noWait {
				fmt.Fprintf(cmd.OutOrStdout(), "installing %s; sandbox-cli image ls shows how far it has got\n", ref)
				return nil
			}
			// Progress redraws one line on a terminal, and is a line a change
			// anywhere else: a log is not a screen.
			errOut := cmd.ErrOrStderr()
			f, ok := errOut.(*os.File)
			tty := ok && isTerminal(f)
			last := ""
			for {
				list, err := c.Images(cmd.Context())
				if err != nil {
					return err
				}
				var img *api.Image
				for i := range list {
					if list[i].Image == ref {
						img = &list[i]
					}
				}
				switch {
				case img == nil:
					return fmt.Errorf("%s is no longer listed", ref)
				case img.State == api.ImageInstalled:
					if tty && last != "" {
						fmt.Fprint(errOut, "\r\033[K")
					}
					fmt.Fprintf(cmd.OutOrStdout(), "installed %s\n", ref)
					return nil
				case img.State == api.ImageFailed:
					return errors.New(termsafe.Clean(img.Error))
				}
				if s := imageState(*img); s != last {
					if tty {
						fmt.Fprintf(errOut, "\r\033[K%s: %s", ref, s)
					} else {
						fmt.Fprintf(errOut, "%s: %s\n", ref, s)
					}
					last = s
				}
				select {
				case <-cmd.Context().Done():
					return cmd.Context().Err()
				case <-time.After(time.Second):
				}
			}
		},
	}
	pull.Flags().BoolVar(&noWait, "no-wait", false, "start the install and return; image ls follows it")

	rm := &cobra.Command{
		Use:   "rm IMAGE...",
		Short: "Remove installed images nothing uses, and the layers no other image needs",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := newClient(ctxFlag)
			if err != nil {
				return err
			}
			var failed error
			for _, ref := range args {
				out, err := c.RemoveImage(cmd.Context(), ref)
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

// imageState is an image's state with its progress, as a column reads it.
func imageState(img api.Image) string {
	p := img.Progress
	if img.State != api.ImageInstalling || p == nil {
		return img.State
	}
	if p.Phase == "pulling" && p.Total > 0 {
		return fmt.Sprintf("pulling %d%%", p.Done*100/p.Total)
	}
	return p.Phase
}

func imageNote(img api.Image) string {
	var n []string
	if img.Default {
		n = append(n, "default")
	}
	if img.Pooled {
		n = append(n, "pooled")
	}
	if img.Error != "" {
		n = append(n, termsafe.Clean(img.Error))
	}
	if len(n) == 0 {
		return "-"
	}
	return strings.Join(n, "; ")
}

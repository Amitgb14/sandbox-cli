package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
)

// Secrets on a gateway: named values a tenant keeps there so a job can name
// them instead of carrying them. A value is read from stdin — never from an
// argument, which every process on this machine can read in the process
// table and which the shell writes to its history — and never shown again.

func newSecretCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "secret",
		Short: "Keep values on a gateway for jobs to name: an agent's API key, a registry token",
		Long: "A secret is a value your tenant keeps on the gateway, sealed at rest, that a\n" +
			"job sets in its runs' environment by name (secrets: [NAME], or agent-run\n" +
			"--secret NAME). The API never returns a value once it is set. Gateway only.",
	}
	var ctxFlag string
	set := &cobra.Command{
		Use:     "set NAME",
		Short:   "Set a secret, its value read from stdin",
		Example: "  sandbox-cli secret set ANTHROPIC_API_KEY < key.txt\n  printf %s \"$TOKEN\" | sandbox-cli secret set REGISTRY_TOKEN",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			value, err := readSecretValue(cmd)
			if err != nil {
				return err
			}
			c, err := requireGateway(cmd.Context(), ctxFlag, "secret")
			if err != nil {
				return err
			}
			return c.SetSecret(cmd.Context(), args[0], value)
		},
	}
	ls := &cobra.Command{
		Use:   "ls",
		Short: "List your tenant's secrets by name",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := requireGateway(cmd.Context(), ctxFlag, "secret")
			if err != nil {
				return err
			}
			list, err := c.Secrets(cmd.Context())
			if err != nil {
				return err
			}
			w := newTable(cmd.OutOrStdout())
			fmt.Fprintln(w, "NAME\tUPDATED")
			for _, s := range list {
				fmt.Fprintf(w, "%s\t%s\n", termsafe.Clean(s.Name), s.Updated.Local().Format("2006-01-02 15:04"))
			}
			return w.Flush()
		},
	}
	rm := &cobra.Command{
		Use:   "rm NAME",
		Short: "Remove a secret",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := requireGateway(cmd.Context(), ctxFlag, "secret")
			if err != nil {
				return err
			}
			return c.DeleteSecret(cmd.Context(), args[0])
		},
	}
	for _, sub := range []*cobra.Command{set, ls, rm} {
		sub.Flags().StringVar(&ctxFlag, "context", "", "which endpoint to use")
		cmd.AddCommand(sub)
	}
	return cmd
}

// readSecretValue reads the value: from a terminal without echoing it, one
// line; from a pipe or a file, all of it, less one trailing newline (what
// `echo` and editors add, and never part of a key).
func readSecretValue(cmd *cobra.Command) (string, error) {
	in := cmd.InOrStdin()
	if f, ok := in.(*os.File); ok && isTerminal(f) {
		fmt.Fprint(cmd.ErrOrStderr(), "value (not shown): ")
		defer fmt.Fprintln(cmd.ErrOrStderr())
		restore, err := makeRaw(f)
		if err != nil {
			return "", err
		}
		defer restore()
		var line []byte
		b := make([]byte, 1)
		for {
			if _, err := f.Read(b); err != nil {
				return "", err
			}
			switch b[0] {
			case '\r', '\n':
				if len(line) == 0 {
					return "", errors.New("no value given")
				}
				return string(line), nil
			case 3, 4: // Ctrl-C, Ctrl-D
				return "", errors.New("cancelled")
			case 127, 8:
				if len(line) > 0 {
					line = line[:len(line)-1]
				}
			default:
				line = append(line, b[0])
			}
		}
	}
	data, err := io.ReadAll(io.LimitReader(in, 64<<10+2))
	if err != nil {
		return "", err
	}
	data = bytes.TrimSuffix(data, []byte("\n"))
	data = bytes.TrimSuffix(data, []byte("\r"))
	if len(data) == 0 {
		return "", errors.New("no value on stdin (sandbox-cli secret set NAME < FILE)")
	}
	return string(data), nil
}

package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
)

// Registry logins on a gateway: an organization's user name and token for a
// registry, which the gateway adds to the organization's creates of an image
// there. The token is read as a secret's value is (readSecretValue): from
// stdin, never from an argument.

func newRegistryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "registry",
		Short: "Keep your organization's logins for private image registries on a gateway",
		Long: "A registry login is a user name and token your organization keeps on the\n" +
			"gateway, sealed at rest. Every sandbox, job and service of the organization\n" +
			"whose image is on that registry is pulled with it; no other organization's\n" +
			"is. The token is never returned once it is set. Gateway only.",
	}
	var ctxFlag, username string
	login := &cobra.Command{
		Use:   "login REGISTRY",
		Short: "Set the login for a registry, its token read from stdin",
		Example: "  sandbox-cli registry login ghcr.io --username alice < token.txt\n" +
			"  printf %s \"$GHCR_TOKEN\" | sandbox-cli registry login ghcr.io -u alice",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if username == "" {
				return errors.New("--username is required")
			}
			token, err := readSecretValue(cmd)
			if err != nil {
				return err
			}
			c, err := requireGateway(cmd.Context(), ctxFlag, "registry")
			if err != nil {
				return err
			}
			if err := c.SetRegistryLogin(cmd.Context(), args[0], username, token); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "images on %s are pulled as %s\n", termsafe.Clean(args[0]), termsafe.Clean(username))
			return nil
		},
	}
	login.Flags().StringVarP(&username, "username", "u", "", "the registry user name")
	ls := &cobra.Command{
		Use:   "ls",
		Short: "List your organization's registry logins",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := requireGateway(cmd.Context(), ctxFlag, "registry")
			if err != nil {
				return err
			}
			list, err := c.RegistryLogins(cmd.Context())
			if err != nil {
				return err
			}
			w := newTable(cmd.OutOrStdout())
			fmt.Fprintln(w, "REGISTRY\tUSERNAME\tUPDATED")
			for _, l := range list {
				fmt.Fprintf(w, "%s\t%s\t%s\n", termsafe.Clean(l.Registry), termsafe.Clean(l.Username), l.Updated.Local().Format("2006-01-02 15:04"))
			}
			return w.Flush()
		},
	}
	logout := &cobra.Command{
		Use:   "logout REGISTRY",
		Short: "Remove the login for a registry",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := requireGateway(cmd.Context(), ctxFlag, "registry")
			if err != nil {
				return err
			}
			return c.DeleteRegistryLogin(cmd.Context(), args[0])
		},
	}
	for _, sub := range []*cobra.Command{login, ls, logout} {
		sub.Flags().StringVar(&ctxFlag, "context", "", "which endpoint to use")
		cmd.AddCommand(sub)
	}
	return cmd
}

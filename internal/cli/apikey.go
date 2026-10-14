package cli

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
)

// newAPIKeyCmd is `sandbox-cli api-key`: the caller's personal API keys on a
// gateway (POST, GET and DELETE /v1/keys), what Studio's API keys screen
// does. The gateway holds each to less than its maker; the CLI only asks.
func newAPIKeyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "api-key",
		Short: "Make, list and revoke your own API keys on a gateway, for your programs to act as you",
		Long: "A personal API key acts as you, for a program: a CI job, a script, the CLI on\n" +
			"another machine. It has the scopes you give it, from your own (never admin),\n" +
			"and always expires: in an hour to 366 days, never after the key or account\n" +
			"you make it with. Your policy applies to it. Revoking, suspending or expiring\n" +
			"your account ends it. A personal key cannot make or revoke keys itself.\n" +
			"Gateway only.",
	}
	var ctxFlag, expires, out string
	var scopes []string
	create := &cobra.Command{
		Use:   "create NAME",
		Short: "Make a key; its secret is printed once, or written to --out",
		Example: "  sandbox-cli api-key create ci --scope sandbox:read --scope sandbox:create --expires 90d\n" +
			"  sandbox-cli api-key create laptop --scope sandbox:read --out ~/.config/sandbox/laptop.key",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if len(scopes) == 0 {
				return errors.New("give the key at least one --scope, from your own (sandbox-cli whoami lists them)")
			}
			secs, err := api.ParseKeyExpiry(expires)
			if err != nil {
				return fmt.Errorf("--expires %w", err)
			}
			if secs == 0 {
				return errors.New("--expires: a personal key always expires; give 1h to 366d")
			}
			// Made before the key, so a path that cannot be written mints
			// nothing; exclusive, so a key file already there is not replaced.
			var f *os.File
			if out != "" {
				if f, err = os.OpenFile(out, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600); err != nil {
					return fmt.Errorf("--out: %w", err)
				}
				defer f.Close()
			}
			c, err := requireGateway(cmd.Context(), ctxFlag, "api-key")
			if err != nil {
				if f != nil {
					f.Close()
					os.Remove(out)
				}
				return err
			}
			k, err := c.CreatePersonalKey(cmd.Context(), api.CreatePersonalKeyRequest{Name: args[0], Scopes: scopes, ExpiresInSecs: secs})
			if err != nil {
				if f != nil {
					f.Close()
					os.Remove(out)
				}
				return err
			}
			w := cmd.OutOrStdout()
			fmt.Fprintf(w, "id:      %s\nname:    %s\nscopes:  %s\nexpires: %s\n", termsafe.Clean(k.ID), termsafe.Clean(k.Name),
				termsafe.Clean(strings.Join(k.Scopes, ",")), expiryText(k.Expires))
			if k.Policy != "" {
				fmt.Fprintf(w, "policy:  %s\n", termsafe.Clean(k.Policy))
			}
			if f != nil {
				if _, err := fmt.Fprintln(f, k.Secret); err != nil {
					return fmt.Errorf("writing %s: %w; revoke %s (sandbox-cli api-key revoke %s)", out, err, k.ID, k.ID)
				}
				if err := f.Close(); err != nil {
					return fmt.Errorf("writing %s: %w; revoke %s (sandbox-cli api-key revoke %s)", out, err, k.ID, k.ID)
				}
				fmt.Fprintf(w, "secret:  in %s\n", termsafe.Clean(out))
				fmt.Fprintf(cmd.ErrOrStderr(), "Use it with: sandbox-cli context add NAME GATEWAY-URL --token-file %s\n", termsafe.Clean(out))
				return nil
			}
			fmt.Fprintf(w, "secret:  %s\n", k.Secret)
			fmt.Fprintln(cmd.ErrOrStderr(), "The secret is shown once and stored only as a hash; keep it now (--out FILE writes it to a file instead).")
			return nil
		},
	}
	cf := create.Flags()
	cf.StringArrayVar(&scopes, "scope", nil, "a scope you hold: sandbox:read, sandbox:create, sandbox:delete, sandbox:ssh, secrets:write, org:create (repeatable)")
	cf.StringVar(&expires, "expires", "30d", "how long the key works: 1h to 366d, e.g. 7d or 12h")
	cf.StringVar(&out, "out", "", "write the secret to this new file (mode 0600) rather than print it")

	list := &cobra.Command{
		Use:   "list",
		Short: "List your personal keys (never their secrets)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := requireGateway(cmd.Context(), ctxFlag, "api-key")
			if err != nil {
				return err
			}
			keys, err := c.PersonalKeys(cmd.Context())
			if err != nil {
				return err
			}
			t := newTable(cmd.OutOrStdout())
			fmt.Fprintln(t, "ID\tNAME\tSCOPES\tCREATED\tEXPIRES\tSTATE")
			for _, k := range keys {
				state := "active"
				switch {
				case k.Revoked:
					state = "revoked"
				case k.Expires != nil && !time.Now().Before(*k.Expires):
					state = "expired"
				}
				fmt.Fprintf(t, "%s\t%s\t%s\t%s\t%s\t%s\n", termsafe.Clean(k.ID), orDash(k.Name), termsafe.Clean(strings.Join(k.Scopes, ",")),
					k.Created.Local().Format("2006-01-02 15:04"), expiryText(k.Expires), state)
			}
			return t.Flush()
		},
	}
	revoke := &cobra.Command{
		Use:   "revoke ID",
		Short: "Revoke one of your personal keys; whatever uses it is refused at once",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := requireGateway(cmd.Context(), ctxFlag, "api-key")
			if err != nil {
				return err
			}
			if err := c.RevokePersonalKey(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "revoked %s\n", termsafe.Clean(args[0]))
			return nil
		},
	}
	for _, sub := range []*cobra.Command{create, list, revoke} {
		sub.Flags().StringVar(&ctxFlag, "context", "", "which endpoint to use")
		cmd.AddCommand(sub)
	}
	return cmd
}

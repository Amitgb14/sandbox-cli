package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
)

// The gateway's users, as an admin manages them while it serves: keys, and
// the policies their sandboxes are held to (docs/fleet.md#user-policies).
// `sandbox-gateway keys|policies` does the same on the state file, and so
// only while the gateway is stopped.

func newGatewayKeysCmd(ctxFlag *string) *cobra.Command {
	cmd := &cobra.Command{Use: "keys", Short: "Issue, list and revoke the gateway's API keys (admin key)"}

	var user, tenant, policy, invite, expires string
	var scopes []string
	create := &cobra.Command{
		Use:   "create",
		Short: "Issue an API key; its secret is printed once",
		Long: "Issues an API key: a user, in a tenant, with scopes. A demo user is one with a\n" +
			"policy (gateway policies) and an expiry; a long-time user has neither. With\n" +
			"--invite-url it also prints the link that signs the user in to hosted Studio.",
		Example: "  # a demo user: 30-minute sandboxes, for a week\n" +
			"  sandbox-cli gateway keys create --user bob --tenant bob \\\n" +
			"    --scope sandbox:read --scope sandbox:create --scope sandbox:delete \\\n" +
			"    --policy demo --expires 7d --invite-url https://studio.example.com\n" +
			"  # a long-time user\n" +
			"  sandbox-cli gateway keys create --user carol --tenant carol \\\n" +
			"    --scope sandbox:read --scope sandbox:create --scope sandbox:delete --scope sandbox:ssh",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if invite != "" {
				var err error
				if invite, err = api.InviteBase(invite); err != nil {
					return err
				}
			}
			secs, err := api.ParseKeyExpiry(expires)
			if err != nil {
				return fmt.Errorf("--expires %w", err)
			}
			c, err := requireGateway(cmd.Context(), *ctxFlag, "gateway keys")
			if err != nil {
				return err
			}
			k, err := c.CreateKey(cmd.Context(), api.CreateKeyRequest{User: user, Tenant: tenant, Scopes: scopes, Policy: policy, ExpiresInSecs: secs})
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "id:     %s\nuser:   %s\ntenant: %s\nscopes: %s\n", k.ID, termsafe.Clean(k.User), termsafe.Clean(k.Tenant), strings.Join(k.Scopes, ","))
			if k.Policy != "" {
				fmt.Fprintf(out, "policy: %s\n", termsafe.Clean(k.Policy))
			}
			fmt.Fprintf(out, "expires: %s\n", expiryText(k.Expires))
			fmt.Fprintf(out, "secret: %s\n", k.Secret)
			if invite != "" {
				fmt.Fprintf(out, "invite: %s/#key=%s\n", invite, k.Secret)
			}
			fmt.Fprintln(cmd.ErrOrStderr(), "The secret is shown once and stored only as a hash; keep it now.")
			if invite != "" {
				fmt.Fprintln(cmd.ErrOrStderr(), "The invite link is the key: send it as you would a password.")
				if slices.Contains(k.Scopes, "admin") {
					fmt.Fprintln(cmd.ErrOrStderr(), api.AdminInviteNote)
				}
			}
			return nil
		},
	}
	f := create.Flags()
	f.StringVar(&user, "user", "", "the user the key acts as; required")
	f.StringVar(&tenant, "tenant", "", "the user's tenant (quotas are per tenant)")
	f.StringArrayVar(&scopes, "scope", nil, "a scope: sandbox:read, sandbox:create, sandbox:delete, sandbox:ssh, secrets:write, org:create, admin (repeatable)")
	f.StringVar(&expires, "expires", "never", "how long the key works: 7d, 12h, or never")
	f.StringVar(&policy, "policy", "", "assign the user this policy (gateway policies set) as the key is issued")
	f.StringVar(&invite, "invite-url", "", "a hosted Studio's address (https://studio.example.com): also print the invite link that signs the user in")

	list := &cobra.Command{
		Use:   "list",
		Short: "List API keys (never their secrets)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := requireGateway(cmd.Context(), *ctxFlag, "gateway keys")
			if err != nil {
				return err
			}
			keys, err := c.Keys(cmd.Context())
			if err != nil {
				return err
			}
			t := newTable(cmd.OutOrStdout())
			fmt.Fprintln(t, "ID\tUSER\tTENANT\tSCOPES\tPOLICY\tCREATED\tEXPIRES\tSTATE\tEMAIL")
			for _, k := range keys {
				state := "active"
				switch {
				case k.Revoked:
					state = "revoked"
				case k.Expires != nil && !time.Now().Before(*k.Expires):
					state = "expired"
				}
				fmt.Fprintf(t, "%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\t%s\n", termsafe.Clean(k.ID), orDash(k.User), orDash(k.Tenant),
					strings.Join(k.Scopes, ","), orDash(k.Policy), k.Created.Local().Format(time.RFC3339), expiryText(k.Expires), state, orDash(k.Email))
			}
			return t.Flush()
		},
	}

	var renewFor string
	renew := &cobra.Command{
		Use:   "renew ID",
		Short: "Change when an API key stops working: --expires 30d, never, or now",
		Example: "  sandbox-cli gateway keys renew key_0123456789abcdef --expires 30d\n" +
			"  sandbox-cli gateway keys renew key_0123456789abcdef --expires now",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			secs := -1 // now
			if renewFor != "now" {
				var err error
				if secs, err = api.ParseKeyExpiry(renewFor); err != nil {
					return fmt.Errorf("--expires %w", err)
				}
			}
			c, err := requireGateway(cmd.Context(), *ctxFlag, "gateway keys")
			if err != nil {
				return err
			}
			k, err := c.SetKeyExpiry(cmd.Context(), args[0], secs)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s (%s): expires %s\n", termsafe.Clean(k.ID), orDash(k.User), expiryText(k.Expires))
			return nil
		},
	}
	renew.Flags().StringVar(&renewFor, "expires", "", "how long from now: 30d, 12h, never, or now (ends what it has open)")
	_ = renew.MarkFlagRequired("expires")

	revoke := &cobra.Command{
		Use:   "revoke ID",
		Short: "Revoke an API key; what it has open is closed at once",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := requireGateway(cmd.Context(), *ctxFlag, "gateway keys")
			if err != nil {
				return err
			}
			if err := c.RevokeKey(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "revoked %s\n", termsafe.Clean(args[0]))
			return nil
		},
	}
	var resetURL string
	var resetSend bool
	reset := &cobra.Command{
		Use:   "reset ID",
		Short: "Make a link that sets a new password for an account",
		Long: "Makes a one-time link, valid two days, with which the account's user sets a new\n" +
			"password; their sign-ins end when it is used. The old password works until then.",
		Example: "  sandbox-cli gateway keys reset key_0123456789abcdef --invite-url https://studio.example.com",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			base, err := inviteURLBase(resetURL)
			if err != nil {
				return err
			}
			c, err := requireGateway(cmd.Context(), *ctxFlag, "gateway keys")
			if err != nil {
				return err
			}
			iv, err := c.ResetPassword(cmd.Context(), args[0], resetSend)
			if err != nil {
				return err
			}
			printInvite(cmd, iv, base)
			return nil
		},
	}
	reset.Flags().StringVar(&resetURL, "invite-url", "", "the hosted Studio's address (https://studio.example.com), to print the whole link")
	reset.Flags().BoolVar(&resetSend, "send", false, "have the gateway email the link to the account's address (a gateway started with --smtp-host)")

	cmd.AddCommand(create, list, renew, revoke, reset)
	return cmd
}

// newGatewayInvitesCmd is `gateway invites`: sign-up links for people, who
// then sign in to hosted Studio with an email and password.
func newGatewayInvitesCmd(ctxFlag *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "invites",
		Short: "Invite people by email to sign in to hosted Studio with a password (admin key)",
		Long: "An invite is a one-time link for an email address. Opening it in hosted Studio,\n" +
			"the person chooses a password and becomes an account: a user with the tenant,\n" +
			"scopes, policy and lifetime the invite names, as a key issued with them would\n" +
			"have. They sign in with the address and password from then on; the account is\n" +
			"listed, renewed and revoked with gateway keys.",
	}

	var email, user, tenant, policy, expires, inviteURL string
	var scopes []string
	var send bool
	create := &cobra.Command{
		Use:   "create",
		Short: "Make an invite; its link is printed once",
		Example: "  # a demo user: 30-minute sandboxes, an account for a week, the link emailed\n" +
			"  sandbox-cli gateway invites create --email bob@example.com --user bob --tenant bob \\\n" +
			"    --scope sandbox:read --scope sandbox:create --scope sandbox:delete \\\n" +
			"    --policy demo --expires 7d --send --invite-url https://studio.example.com",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			base, err := inviteURLBase(inviteURL)
			if err != nil {
				return err
			}
			secs, err := api.ParseKeyExpiry(expires)
			if err != nil {
				return fmt.Errorf("--expires %w", err)
			}
			c, err := requireGateway(cmd.Context(), *ctxFlag, "gateway invites")
			if err != nil {
				return err
			}
			iv, err := c.CreateInvite(cmd.Context(), api.CreateInviteRequest{Email: email, User: user, Tenant: tenant,
				Scopes: scopes, Policy: policy, ExpiresInSecs: secs, SendEmail: send})
			if err != nil {
				return err
			}
			printInvite(cmd, iv, base)
			return nil
		},
	}
	f := create.Flags()
	f.StringVar(&email, "email", "", "the address the invite is for; required")
	f.StringVar(&user, "user", "", "the user the account acts as; required")
	f.StringVar(&tenant, "tenant", "", "the user's tenant (quotas are per tenant)")
	f.StringArrayVar(&scopes, "scope", nil, "a scope, as for gateway keys create (repeatable)")
	f.StringVar(&policy, "policy", "", "the policy the user holds (gateway policies set)")
	f.StringVar(&expires, "expires", "never", "how long the account lasts once made: 7d, 12h, or never")
	f.StringVar(&inviteURL, "invite-url", "", "the hosted Studio's address (https://studio.example.com), to print the whole link")
	f.BoolVar(&send, "send", false, "have the gateway email the link to the address (a gateway started with --smtp-host)")
	_ = create.MarkFlagRequired("email")
	_ = create.MarkFlagRequired("user")

	list := &cobra.Command{
		Use:   "list",
		Short: "List invites not yet used",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := requireGateway(cmd.Context(), *ctxFlag, "gateway invites")
			if err != nil {
				return err
			}
			list, err := c.Invites(cmd.Context())
			if err != nil {
				return err
			}
			ivs := list.Invites
			t := newTable(cmd.OutOrStdout())
			fmt.Fprintln(t, "ID\tEMAIL\tUSER\tTENANT\tFOR\tLINK WORKS UNTIL")
			for _, iv := range ivs {
				what := "a new account"
				if iv.Reset {
					what = "a new password"
				}
				fmt.Fprintf(t, "%s\t%s\t%s\t%s\t%s\t%s\n", termsafe.Clean(iv.ID), termsafe.Clean(iv.Email), orDash(iv.User), orDash(iv.Tenant),
					what, iv.Expires.Local().Format(time.RFC3339))
			}
			return t.Flush()
		},
	}

	revoke := &cobra.Command{
		Use:   "revoke ID",
		Short: "Withdraw an invite; its link stops working",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := requireGateway(cmd.Context(), *ctxFlag, "gateway invites")
			if err != nil {
				return err
			}
			if err := c.RevokeInvite(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "withdrew %s\n", termsafe.Clean(args[0]))
			return nil
		},
	}
	var resendURL string
	var resendSend bool
	resend := &cobra.Command{
		Use:   "resend ID",
		Short: "Make a pending invite's link again, valid two days; the old link stops working",
		Example: "  sandbox-cli gateway invites resend inv_0123456789abcdef --send\n" +
			"  sandbox-cli gateway invites resend inv_0123456789abcdef --invite-url https://studio.example.com",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			base, err := inviteURLBase(resendURL)
			if err != nil {
				return err
			}
			c, err := requireGateway(cmd.Context(), *ctxFlag, "gateway invites")
			if err != nil {
				return err
			}
			iv, err := c.ResendInvite(cmd.Context(), args[0], resendSend)
			if err != nil {
				return err
			}
			printInvite(cmd, iv, base)
			return nil
		},
	}
	resend.Flags().StringVar(&resendURL, "invite-url", "", "the hosted Studio's address (https://studio.example.com), to print the whole link")
	resend.Flags().BoolVar(&resendSend, "send", false, "have the gateway email the link to the address (a gateway started with --smtp-host)")

	cmd.AddCommand(create, list, resend, revoke)
	return cmd
}

// inviteURLBase checks an --invite-url, "" for none.
func inviteURLBase(raw string) (string, error) {
	if raw == "" {
		return "", nil
	}
	return api.InviteBase(raw)
}

// printInvite prints an invite just made, with its link when base is set.
func printInvite(cmd *cobra.Command, iv api.CreatedInvite, base string) {
	out := cmd.OutOrStdout()
	fmt.Fprintf(out, "id:      %s\nemail:   %s\nuser:    %s\n", iv.ID, termsafe.Clean(iv.Email), termsafe.Clean(iv.User))
	if iv.Reset {
		fmt.Fprintln(out, "for:     a new password")
	}
	fmt.Fprintf(out, "until:   %s\n", iv.Expires.Local().Format(time.RFC3339))
	if base != "" {
		fmt.Fprintf(out, "link:    %s/#invite=%s\n", base, iv.Token)
	} else {
		fmt.Fprintf(out, "token:   %s\n", iv.Token)
	}
	switch {
	case iv.Emailed:
		fmt.Fprintf(out, "emailed: yes\n")
	case iv.EmailError != "":
		fmt.Fprintf(out, "emailed: no: %s\n", termsafe.Clean(iv.EmailError))
	}
	w := cmd.ErrOrStderr()
	if base == "" {
		fmt.Fprintln(w, "The link is the hosted Studio's address followed by /#invite= and the token.")
	}
	fmt.Fprintln(w, "It is shown once and works once: send it to the address now.")
	if slices.Contains(iv.Scopes, "admin") {
		fmt.Fprintln(w, api.AdminInviteNote)
	}
}

func newGatewayPoliciesCmd(ctxFlag *string) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "policies",
		Short: "Set, list, assign and remove user policies (admin key)",
		Long: "A user policy holds a user's sandboxes to limits on top of the nodes' own; for\n" +
			"now, how long each may exist. A sandbox asking for no lifetime gets the\n" +
			"policy's, and one asking for more is refused. A change to a policy holds for\n" +
			"sandboxes created after it; move a running one's end with\n" +
			"sandbox-cli update SANDBOX --lifetime +15m. Admin keys are held to none.",
		Example: "  sandbox-cli gateway policies set demo --max-lifetime 30m\n" +
			"  sandbox-cli gateway policies assign bob demo --tenant bob\n" +
			"  sandbox-cli gateway policies assign bob --tenant bob --none",
	}

	var maxLife time.Duration
	set := &cobra.Command{
		Use:   "set NAME",
		Short: "Create or change a policy; a change holds for sandboxes created after it",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if maxLife < 0 || maxLife%time.Second != 0 {
				return fmt.Errorf("--max-lifetime %v: whole seconds, not negative", maxLife)
			}
			c, err := requireGateway(cmd.Context(), *ctxFlag, "gateway policies")
			if err != nil {
				return err
			}
			p, err := c.PutPolicy(cmd.Context(), api.UserPolicy{Name: args[0], MaxLifetimeSecs: int(maxLife / time.Second)})
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "policy %s: max lifetime %s\n", termsafe.Clean(p.Name), lifetimeText(p.MaxLifetimeSecs))
			return nil
		},
	}
	set.Flags().DurationVar(&maxLife, "max-lifetime", 0, "how long each of a user's sandboxes may exist, busy or not, e.g. 30m (0: no bound of the policy's own)")

	list := &cobra.Command{
		Use:   "list",
		Short: "List policies and the users holding each",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := requireGateway(cmd.Context(), *ctxFlag, "gateway policies")
			if err != nil {
				return err
			}
			pols, err := c.Policies(cmd.Context())
			if err != nil {
				return err
			}
			keys, err := c.Keys(cmd.Context())
			if err != nil {
				return err
			}
			holders, seen := map[string][]string{}, map[string]bool{}
			for _, k := range keys {
				who := k.User
				if k.Tenant != "" {
					who += "@" + k.Tenant
				}
				if k.Policy != "" && !k.Revoked && !seen[who] {
					seen[who] = true
					holders[k.Policy] = append(holders[k.Policy], who)
				}
			}
			t := newTable(cmd.OutOrStdout())
			fmt.Fprintln(t, "NAME\tMAX LIFETIME\tUSERS")
			for _, p := range pols {
				fmt.Fprintf(t, "%s\t%s\t%s\n", termsafe.Clean(p.Name), lifetimeText(p.MaxLifetimeSecs), orDash(strings.Join(holders[p.Name], ",")))
			}
			return t.Flush()
		},
	}

	var tenant string
	var none bool
	assign := &cobra.Command{
		Use:   "assign USER [POLICY]",
		Short: "Give a user a policy, or none with --none",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			name := ""
			switch {
			case none && len(args) == 2:
				return errors.New("give a policy or --none, not both")
			case !none && len(args) == 1:
				return errors.New("give a policy, or --none to clear the user's")
			case !none:
				name = args[1]
			}
			c, err := requireGateway(cmd.Context(), *ctxFlag, "gateway policies")
			if err != nil {
				return err
			}
			if err := c.AssignPolicy(cmd.Context(), api.AssignPolicyRequest{User: args[0], Tenant: tenant, Policy: name}); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: policy %s\n", termsafe.Clean(args[0]), orDash(name))
			return nil
		},
	}
	assign.Flags().StringVar(&tenant, "tenant", "", "the tenant of the user's keys")
	assign.Flags().BoolVar(&none, "none", false, "clear the user's policy")

	remove := &cobra.Command{
		Use:   "remove NAME",
		Short: "Remove a policy no user holds",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := requireGateway(cmd.Context(), *ctxFlag, "gateway policies")
			if err != nil {
				return err
			}
			if err := c.DeletePolicy(cmd.Context(), args[0]); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed %s\n", termsafe.Clean(args[0]))
			return nil
		},
	}
	cmd.AddCommand(set, list, assign, remove)
	return cmd
}

// expiryText is when a key stops working, or "never".
func expiryText(at *time.Time) string {
	if at == nil {
		return "never"
	}
	return at.Local().Format(time.RFC3339)
}

// lifetimeText is a lifetime in seconds as a person reads it.
func lifetimeText(secs int) string {
	if secs == 0 {
		return "none"
	}
	return (time.Duration(secs) * time.Second).String()
}

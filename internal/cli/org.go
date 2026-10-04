package cli

import (
	"context"
	"errors"
	"fmt"
	"regexp"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
)

// Organisations on a gateway: tenants users create and share. Every command
// acts in one — --org, then SANDBOX_ORG, then the context's (org use), then
// the key's own tenant — and the gateway checks each request against the
// key's memberships; the CLI only chooses.

// orgArgRE is what may be sent as an organisation: an organisation's name,
// or a tenant's (which may hold capitals, dots and @), never a header's
// worth of anything else.
var orgArgRE = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._@+-]{0,63}$`)

func validOrgArg(s string) bool { return orgArgRE.MatchString(s) }

// orgErr explains a refused organisation: the gateway answers 404 for one
// the key's user is not in, as for one that does not exist.
func orgErr(c *api.Client, err error) error {
	var ae *api.Error
	if c.Org() != "" && errors.As(err, &ae) && ae.Code == api.CodeNotFound && ae.Message == "no such organization" {
		return fmt.Errorf("organization %s: no such organization among yours (sandbox-cli org ls)", termsafe.Clean(c.Org()))
	}
	return err
}

// gatewayOrgs connects and refuses unless the endpoint is a gateway, and
// unless the organisation it would act in is one the key may select.
func gatewayOrgs(ctx context.Context, ctxFlag string) (*api.Client, string, api.Whoami, error) {
	c, name, err := newClient(ctxFlag)
	if err != nil {
		return nil, "", api.Whoami{}, err
	}
	if c.Org() != "" && !validOrgArg(c.Org()) {
		return nil, "", api.Whoami{}, fmt.Errorf("%q is not an organization name", termsafe.Clean(c.Org()))
	}
	gw, err := c.IsGateway(ctx)
	if err != nil {
		return nil, "", api.Whoami{}, err
	}
	if !gw {
		return nil, "", api.Whoami{}, fmt.Errorf("context %s is a plain sandboxd, which has no users and no organizations: org needs a gateway (sandbox-cli context add NAME https://gateway --token-file KEYFILE)",
			termsafe.Clean(name))
	}
	w, err := c.Whoami(ctx)
	if err != nil {
		return nil, "", api.Whoami{}, orgErr(c, err)
	}
	return c, name, w, nil
}

func newOrgCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "org",
		Short: "Organizations on a gateway: create one, switch between yours, manage its members",
		Long: "An organization is a tenant users create and share on a gateway. What is made\n" +
			"in one — sandboxes, volumes, secrets, jobs, services — is its own, with its own\n" +
			"quota, and nobody outside it can see it. A command acts in the organization\n" +
			"--org names, else SANDBOX_ORG, else the context's (org use), else your key's\n" +
			"own tenant, called \"default\" when it has no name. Gateway only.",
		Example: "  sandbox-cli org create acme\n" +
			"  sandbox-cli org use acme\n" +
			"  sandbox-cli org members add bob\n" +
			"  sandbox-cli --org acme ls",
	}
	var ctxFlag string
	ls := &cobra.Command{
		Use:   "ls",
		Short: "List the organizations you may act in; * marks the current one",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, _, err := gatewayOrgs(cmd.Context(), ctxFlag)
			if err != nil {
				return err
			}
			orgs, err := c.Orgs(cmd.Context())
			if err != nil {
				return err
			}
			t := newTable(cmd.OutOrStdout())
			fmt.Fprintln(t, "\tNAME\tROLE\tCREATED")
			for _, o := range orgs {
				mark, created := "", "-"
				if o.Current {
					mark = "*"
				}
				if !o.Created.IsZero() {
					created = o.Created.Local().Format("2006-01-02 15:04")
				}
				fmt.Fprintf(t, "%s\t%s\t%s\t%s\n", mark, termsafe.Clean(o.Name), termsafe.Clean(o.Role), created)
			}
			return t.Flush()
		},
	}
	create := &cobra.Command{
		Use:   "create NAME",
		Short: "Create an organization, with you as its owner (needs the org:create scope)",
		Long: "NAME is 1 to 30 lowercase letters, digits and dashes, starting with a letter,\n" +
			"with no \"--\"; \"default\" and \"admin\" are reserved, and a name already used by\n" +
			"a tenant is taken.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, _, err := gatewayOrgs(cmd.Context(), ctxFlag)
			if err != nil {
				return err
			}
			o, err := c.CreateOrg(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "created organization %s; act in it with --org %s, or make it this context's with: sandbox-cli org use %s\n",
				termsafe.Clean(o.Name), termsafe.Clean(o.Name), termsafe.Clean(o.Name))
			return nil
		},
	}
	use := &cobra.Command{
		Use:   "use NAME",
		Short: "Make NAME the organization this context acts in",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if !validOrgArg(args[0]) {
				return fmt.Errorf("%q is not an organization name", termsafe.Clean(args[0]))
			}
			// Checked first, with the organisation selected: a context
			// left pointing at one the key may not select would fail every
			// command after it.
			prev := orgFlag
			orgFlag = args[0]
			_, name, w, err := gatewayOrgs(cmd.Context(), ctxFlag)
			orgFlag = prev
			if err != nil {
				return err
			}
			cf, err := loadContexts()
			if err != nil {
				return err
			}
			ec, ok := cf.Contexts[name]
			if !ok {
				return fmt.Errorf("context %s is built in and keeps no settings; add the gateway as a context of its own (sandbox-cli context add)", termsafe.Clean(name))
			}
			ec.Org = args[0]
			own := w.Tenant
			if own == "" {
				own = api.DefaultOrg
			}
			if args[0] == own {
				ec.Org = "" // the key's own tenant needs no header
			}
			cf.Contexts[name] = ec
			if err := cf.save(); err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "context %s now acts in organization %s\n", termsafe.Clean(name), termsafe.Clean(args[0]))
			return nil
		},
	}
	members := &cobra.Command{
		Use:   "members",
		Short: "List the current organization's members",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, w, err := gatewayOrgs(cmd.Context(), ctxFlag)
			if err != nil {
				return err
			}
			ms, err := c.OrgMembers(cmd.Context(), w.Org)
			if err != nil {
				return notAnOrg(w, err)
			}
			t := newTable(cmd.OutOrStdout())
			fmt.Fprintln(t, "USER\tTENANT\tROLE\tADDED")
			for _, m := range ms {
				tenant, added := m.Tenant, "-"
				if tenant == "" {
					tenant = api.DefaultOrg
				}
				if !m.Added.IsZero() {
					added = m.Added.Local().Format("2006-01-02 15:04")
				}
				fmt.Fprintf(t, "%s\t%s\t%s\t%s\n", termsafe.Clean(m.User), termsafe.Clean(tenant), termsafe.Clean(m.Role), added)
			}
			return t.Flush()
		},
	}
	var role, memberTenant string
	add := &cobra.Command{
		Use:   "add USER",
		Short: "Add a member to the current organization, or change one's role (owners only)",
		Long: "USER is named as their API key names them. A user name is unique only within\n" +
			"a tenant, so --tenant says which tenant their keys are in; it defaults to\n" +
			"yours (\"default\" for keys with none).",
		Example: "  sandbox-cli org members add bob\n  sandbox-cli --org acme org members add carol --role owner --tenant team-c",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, w, err := gatewayOrgs(cmd.Context(), ctxFlag)
			if err != nil {
				return err
			}
			m, err := c.SetOrgMember(cmd.Context(), w.Org, api.OrgMemberRequest{User: args[0], Tenant: memberTenant, Role: role})
			if err != nil {
				return notAnOrg(w, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s is a%s %s of %s\n", termsafe.Clean(m.User), article(m.Role), termsafe.Clean(m.Role), termsafe.Clean(w.Org))
			return nil
		},
	}
	add.Flags().StringVar(&role, "role", api.RoleMember, "owner or member")
	add.Flags().StringVar(&memberTenant, "tenant", "", "the tenant of the user's own keys (default: yours)")
	rm := &cobra.Command{
		Use:   "rm USER",
		Short: "Remove a member from the current organization (owners only); what they had open there ends at once",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, w, err := gatewayOrgs(cmd.Context(), ctxFlag)
			if err != nil {
				return err
			}
			if err := c.RemoveOrgMember(cmd.Context(), w.Org, args[0], memberTenant); err != nil {
				return notAnOrg(w, err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "removed %s from %s\n", termsafe.Clean(args[0]), termsafe.Clean(w.Org))
			return nil
		},
	}
	rm.Flags().StringVar(&memberTenant, "tenant", "", "the tenant of the user's own keys (default: yours)")
	members.AddCommand(add, rm)
	for _, sub := range []*cobra.Command{ls, create, use, members, add, rm} {
		sub.Flags().StringVar(&ctxFlag, "context", "", "which endpoint to use")
	}
	cmd.AddCommand(ls, create, use, members)
	return cmd
}

func article(role string) string {
	if role == api.RoleOwner {
		return "n"
	}
	return ""
}

// notAnOrg explains a members call on a tenant that is not an organisation:
// a key's own tenant that nobody created has no member list.
func notAnOrg(w api.Whoami, err error) error {
	var ae *api.Error
	if errors.As(err, &ae) && ae.Code == api.CodeNotFound && ae.Message == "no such organization" {
		return fmt.Errorf("%s is your key's own tenant, not an organization made with org create: it has no members to manage (select one with --org or org use)", termsafe.Clean(w.Org))
	}
	return err
}

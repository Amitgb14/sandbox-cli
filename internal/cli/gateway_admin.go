package cli

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
)

// `sandbox-cli gateway …` is the operator's side of a running gateway: its
// admin API, with the current context's key, which must hold the admin
// scope. `sandbox-gateway keys|nodes` works on the state file and so only
// while the gateway is stopped; draining and upgrading a node happen while
// it serves, which is why these are API calls and live here.

func newGatewayCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "gateway",
		Short: "Operate a running gateway's nodes: list, cordon, drain, lost sandboxes, the audit log (admin key)",
		Long: "Calls a gateway's admin API with the current context's key, which needs the\n" +
			"admin scope. Upgrading a node is: drain NODE (or cordon it and wait), upgrade\n" +
			"and restart its sandboxd, uncordon NODE. The gateway keeps a node it cordoned\n" +
			"cordoned across the node's restart, until it is uncordoned here.",
	}
	var ctxFlag string
	cmd.PersistentFlags().StringVar(&ctxFlag, "context", "", "which endpoint to use")

	nodes := &cobra.Command{
		Use:   "nodes",
		Short: "List the gateway's nodes: health, cordon, sandboxes running, sandboxd version",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := requireGateway(cmd.Context(), ctxFlag, "gateway")
			if err != nil {
				return err
			}
			list, err := c.GatewayNodes(cmd.Context())
			if err != nil {
				return err
			}
			t := newTable(cmd.OutOrStdout())
			fmt.Fprintln(t, "NAME\tHEALTHY\tCORDONED\tRUNNING\tVERSION\tLAST SEEN")
			for _, n := range list {
				cordoned, running, ver := "-", "-", "-"
				if n.Status != nil {
					cordoned, running, ver = strconv.FormatBool(n.Status.Cordoned), strconv.Itoa(n.Status.Running), n.Status.Version
				}
				seen := "never"
				if !n.LastSeen.IsZero() {
					seen = n.LastSeen.Local().Format(time.RFC3339)
				}
				fmt.Fprintf(t, "%s\t%v\t%s\t%s\t%s\t%s\n", termsafe.Clean(n.Name), n.Healthy, cordoned, running, termsafe.Clean(ver), seen)
			}
			return t.Flush()
		},
	}

	cordon := func(use, short string, cordoned bool) *cobra.Command {
		return &cobra.Command{
			Use:   use + " NODE",
			Short: short,
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				c, err := requireGateway(cmd.Context(), ctxFlag, "gateway")
				if err != nil {
					return err
				}
				if _, err := c.CordonNode(cmd.Context(), args[0], cordoned); err != nil {
					return err
				}
				fmt.Fprintf(cmd.OutOrStdout(), "node %s %sed\n", termsafe.Clean(args[0]), use)
				return nil
			},
		}
	}

	var terminate bool
	drain := &cobra.Command{
		Use:   "drain NODE",
		Short: "Cordon a node and report its sandboxes; with --terminate, end them all",
		Long: "Cordons NODE, so it takes no new sandboxes, and says how many it still runs.\n" +
			"Without --terminate they carry on until they end; run it again to see the\n" +
			"count fall. With --terminate every sandbox on the node is terminated now.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, err := requireGateway(cmd.Context(), ctxFlag, "gateway")
			if err != nil {
				return err
			}
			res, err := c.DrainNode(cmd.Context(), args[0], terminate)
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			name := termsafe.Clean(res.Node)
			if terminate {
				fmt.Fprintf(out, "node %s cordoned; %d sandboxes terminated\n", name, len(res.Terminated))
				for _, f := range res.Failed {
					fmt.Fprintf(out, "  %s: %s\n", termsafe.Clean(f.ID), termsafe.Clean(f.Error))
				}
			} else {
				fmt.Fprintf(out, "node %s cordoned; %d sandboxes still running there\n", name, res.Remaining)
			}
			if res.Remaining > 0 && terminate {
				return fmt.Errorf("%d sandboxes on %s could not be terminated", res.Remaining, name)
			}
			return nil
		},
	}
	drain.Flags().BoolVar(&terminate, "terminate", false, "terminate every sandbox on the node now")

	lost := &cobra.Command{
		Use:   "lost",
		Short: "List sandboxes on nodes that have not answered for longer than the gateway's grace period",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := requireGateway(cmd.Context(), ctxFlag, "gateway")
			if err != nil {
				return err
			}
			list, err := c.LostSandboxes(cmd.Context())
			if err != nil {
				return err
			}
			t := newTable(cmd.OutOrStdout())
			fmt.Fprintln(t, "ID\tUSER\tTENANT\tNODE\tNODE DOWN SINCE")
			for _, l := range list {
				since := "(node removed)"
				if !l.NodeDownSince.IsZero() {
					since = l.NodeDownSince.Local().Format(time.RFC3339)
				}
				fmt.Fprintf(t, "%s\t%s\t%s\t%s\t%s\n", termsafe.Clean(l.ID), termsafe.Clean(l.User), termsafe.Clean(l.Tenant), termsafe.Clean(l.Node), since)
			}
			return t.Flush()
		},
	}

	var since durationFlag
	var limit int
	auditCmd := &cobra.Command{
		Use:   "audit",
		Short: "Show the gateway's audit log: who did what, newest last",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, err := requireGateway(cmd.Context(), ctxFlag, "gateway")
			if err != nil {
				return err
			}
			var from time.Time
			if since.secs > 0 {
				from = time.Now().Add(-time.Duration(since.secs) * time.Second)
			}
			list, err := c.Audit(cmd.Context(), from, limit)
			if err != nil {
				return err
			}
			t := newTable(cmd.OutOrStdout())
			fmt.Fprintln(t, "TIME\tUSER\tKEY\tACTION\tSANDBOX\tNODE\tRESULT")
			for _, e := range list.Entries {
				result := e.Result
				if e.Status != 0 {
					result = strconv.Itoa(e.Status)
				}
				action := e.Action
				if e.Session != "" {
					action += " " + e.Session
				}
				key := e.KeyID
				if e.Fingerprint != "" && e.KeyID == "" {
					key = e.Fingerprint
				}
				fmt.Fprintf(t, "%s\t%s\t%s\t%s\t%s\t%s\t%s\n", e.Time.Local().Format(time.RFC3339),
					orDash(e.User), orDash(key), orDash(action), orDash(e.Sandbox), orDash(e.Node), orDash(result))
			}
			if list.Truncated {
				fmt.Fprintln(cmd.ErrOrStderr(), "sandbox-cli: older entries left out; raise --limit or narrow --since")
			}
			return t.Flush()
		},
	}
	auditCmd.Flags().Var(&since, "since", "only entries this recent, e.g. 1h")
	auditCmd.Flags().IntVar(&limit, "limit", 0, "at most this many of the newest entries (default: the gateway's, 100)")

	cmd.AddCommand(nodes,
		cordon("cordon", "Stop placing new sandboxes on a node; what runs there carries on", true),
		cordon("uncordon", "Place new sandboxes on a node again", false),
		drain, lost, auditCmd)
	return cmd
}

// orDash is s made safe to print, or "-" for an empty column.
func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return termsafe.Clean(strings.TrimSpace(s))
}

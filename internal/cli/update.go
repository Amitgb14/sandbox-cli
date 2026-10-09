package cli

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/policy"
)

// newUpdateCmd changes a live sandbox: its name, labels and idle timeout,
// and — on a running one, where the endpoint can — its network. Only what a
// flag names changes.
func newUpdateCmd() *cobra.Command {
	var ctxFlag, name, network string
	var labels, unlabel, allow, deny []string
	var idle time.Duration
	var noBaseline bool
	cmd := &cobra.Command{
		Use:   "update SANDBOX",
		Short: "Rename, relabel, retime or change the network of a live sandbox",
		Long: "Changes a live sandbox in place; only what a flag names changes.\n\n" +
			"--label adds or changes a label and --label KEY- (or --unlabel KEY) removes one;\n" +
			"the others are kept. --idle sets how long it may sit idle before it is\n" +
			"terminated (0: never, where the server allows that). --network changes the\n" +
			"network of a running sandbox, as run's flags would set it: --allow adds to the\n" +
			"built-in hosts unless --no-baseline, and an agent's sandbox keeps its agent's\n" +
			"API. Not every endpoint can change a running sandbox's network.\n\n" +
			"vCPUs and memory are fixed while a VM runs.",
		Example: "  sandbox-cli update web --name api\n" +
			"  sandbox-cli update api --label team=infra --label ticket-\n" +
			"  sandbox-cli update api --idle 2h\n" +
			"  sandbox-cli update api --network allowlist --allow proxy.golang.org --no-baseline",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			f := cmd.Flags()
			var req api.UpdateSandboxRequest
			if f.Changed("name") {
				req.Name = &name
			}
			if f.Changed("idle") {
				secs := int(idle / time.Second)
				if idle%time.Second != 0 || idle < 0 {
					return fmt.Errorf("--idle %v: whole seconds, not negative", idle)
				}
				req.IdleTimeoutSecs = &secs
			}
			if (len(allow) > 0 || noBaseline) && network != api.NetworkAllowlist {
				return errors.New("--allow and --no-baseline go with --network allowlist")
			}
			if len(deny) > 0 && network == "" {
				return errors.New("--deny goes with --network")
			}
			c, _, err := newClient(ctxFlag)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			needCurrent := len(labels) > 0 || len(unlabel) > 0 || network != ""
			var cur api.Sandbox
			if needCurrent {
				if cur, err = c.Sandbox(ctx, args[0]); err != nil {
					return err
				}
			}
			if len(labels) > 0 || len(unlabel) > 0 {
				next, err := editLabels(cur.Labels, labels, unlabel)
				if err != nil {
					return err
				}
				req.Labels = &next
			}
			if network != "" {
				caps, err := c.Capabilities(ctx)
				if err != nil {
					return err
				}
				n, err := updatedNetwork(network, allow, deny, noBaseline, caps, cur.Labels["agent"])
				if err != nil {
					return err
				}
				req.Network = n
			}
			if req.Name == nil && req.Labels == nil && req.IdleTimeoutSecs == nil && req.Network == nil {
				return errors.New("nothing to change: name a flag (--name, --label, --unlabel, --idle, --network)")
			}
			sb, err := c.UpdateSandbox(ctx, args[0], req)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s  name %s  network %s  idle %s  labels %s\n", sb.ID,
				orDash(sb.Name), sb.Network.Mode, idleText(sb.IdleTimeoutSecs), formatLabels(sb.Labels))
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&ctxFlag, "context", "", "which sandboxd to use")
	f.StringVar(&name, "name", "", `rename it ("" removes the name)`)
	f.StringArrayVar(&labels, "label", nil, "set a label, key=value, or remove one, key- (repeatable)")
	f.StringArrayVar(&unlabel, "unlabel", nil, "remove a label by key (repeatable)")
	f.DurationVar(&idle, "idle", 0, "terminate it after this long idle, e.g. 2h (0: never, where allowed)")
	f.StringVar(&network, "network", "", "none, allowlist or open, applied to the running sandbox")
	f.StringArrayVar(&allow, "allow", nil, "with --network allowlist: a host it may reach (repeatable)")
	f.StringArrayVar(&deny, "deny", nil, "with --network: a host it may not reach (repeatable)")
	f.BoolVar(&noBaseline, "no-baseline", false, "with --network allowlist: only the hosts named, without the built-in ones")
	return cmd
}

// editLabels is cur with each key=value set and each key- or --unlabel key
// removed. A gateway's own labels are sent back as they were, which it
// accepts; it refuses any other change to them.
func editLabels(cur map[string]string, set, unset []string) (map[string]string, error) {
	out := map[string]string{}
	for k, v := range cur {
		out[k] = v
	}
	for _, l := range set {
		if k, ok := strings.CutSuffix(l, "-"); ok && !strings.Contains(l, "=") {
			delete(out, k)
			continue
		}
		k, v, ok := strings.Cut(l, "=")
		if !ok || k == "" {
			return nil, fmt.Errorf("--label %q: want key=value, or key- to remove it", l)
		}
		out[k] = v
	}
	for _, k := range unset {
		delete(out, k)
	}
	return out, nil
}

// updatedNetwork is the whole policy an update sends, built as run builds
// one: an allowlist starts from the server's default list (or the built-in
// baseline, where the default is not an allowlist) unless noBaseline, and
// an agent's sandbox keeps its agent's API, without which it stops.
func updatedNetwork(mode string, allow, deny []string, noBaseline bool, caps api.Capabilities, agent string) (*api.NetworkPolicy, error) {
	n := &api.NetworkPolicy{Mode: mode}
	switch mode {
	case api.NetworkNone:
		return n, nil
	case api.NetworkOpen:
	case api.NetworkAllowlist:
		var hosts []string
		if !noBaseline {
			if caps.Network.Default.Mode == api.NetworkAllowlist {
				hosts = append(hosts, caps.Network.Default.Allow...)
			} else {
				hosts = append(hosts, policy.BaselineEgress()...)
			}
		}
		hosts = append(hosts, allow...)
		if d, ok := agents.LookupInteractive(agent); ok && d.ProviderHost != "" && !slices.Contains(hosts, d.ProviderHost) {
			hosts = append(hosts, d.ProviderHost)
		}
		n.Allow = policy.DedupeDomains(hosts)
		if len(n.Allow) == 0 {
			return nil, errors.New("an allowlist of nothing is refused; use --network none to reach nothing")
		}
	default:
		return nil, fmt.Errorf("--network %q: none, allowlist or open", mode)
	}
	n.Deny = policy.DedupeDomains(deny)
	return n, nil
}

func idleText(secs int) string {
	if secs == 0 {
		return "never"
	}
	return (time.Duration(secs) * time.Second).String()
}

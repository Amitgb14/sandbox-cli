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

// newUpdateCmd changes a live sandbox: its name, labels, idle timeout and lifetime,
// and — on a running one, where the endpoint can — its network. Only what a
// flag names changes.
func newUpdateCmd() *cobra.Command {
	var ctxFlag, name, network, lifetime, onIdle string
	var labels, unlabel, allow, deny, groups []string
	var idle time.Duration
	var noBaseline bool
	var export exportFlags
	cmd := &cobra.Command{
		Use:   "update SANDBOX",
		Short: "Rename, relabel, retime or change the network of a live sandbox",
		Long: "Changes a live sandbox in place; only what a flag names changes.\n\n" +
			"--label adds or changes a label and --label KEY- (or --unlabel KEY) removes one;\n" +
			"the others are kept. --idle sets how long it may sit idle before it is\n" +
			"terminated (0: never, where the server allows that), and --on-idle suspend has\n" +
			"it suspended instead, to be resumed by the next command, file or shell that\n" +
			"uses it. --lifetime sets when it\n" +
			"ends, busy or not, counted from its creation: 45m sets it, +15m pushes its end\n" +
			"out and -10m brings it nearer. Through a gateway only an admin can push it out.\n\n" +
			"--network changes the network of a running sandbox, as run's flags would set\n" +
			"it: --allow and --allow-group add to the built-in hosts unless --no-baseline,\n" +
			"and an agent's sandbox keeps its agent's API. Not every endpoint can change a\n" +
			"running sandbox's network.\n\n" +
			"--metrics-export sets where the sandbox's usage is pushed, replacing what was\n" +
			"set, headers included; --metrics-export none stops it.\n\n" +
			"vCPUs and memory are fixed while a VM runs.",
		Example: "  sandbox-cli update web --name api\n" +
			"  sandbox-cli update api --label team=infra --label ticket-\n" +
			"  sandbox-cli update api --idle 2h\n" +
			"  sandbox-cli update api --idle 30m --on-idle suspend\n" +
			"  sandbox-cli update api --lifetime +15m\n" +
			"  OTEL_AUTH='Bearer …' sandbox-cli update api --metrics-export https://otlp.example.com --metrics-export-header Authorization=OTEL_AUTH\n" +
			"  sandbox-cli update api --network allowlist --allow proxy.golang.org --no-baseline\n" +
			"  sandbox-cli update api --network allowlist --allow-group go   # through a gateway",
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
			if f.Changed("on-idle") {
				req.IdleAction = &onIdle
			}
			me, err := export.export()
			if err != nil {
				return err
			}
			req.MetricsExport = me
			if (len(allow) > 0 || len(groups) > 0 || noBaseline) && network != api.NetworkAllowlist {
				return errors.New("--allow, --allow-group and --no-baseline go with --network allowlist")
			}
			if len(deny) > 0 && network == "" {
				return errors.New("--deny goes with --network")
			}
			c, _, err := newClient(ctxFlag)
			if err != nil {
				return err
			}
			ctx := cmd.Context()
			rel := strings.HasPrefix(lifetime, "+") || strings.HasPrefix(lifetime, "-")
			needCurrent := len(labels) > 0 || len(unlabel) > 0 || network != "" || rel
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
			if f.Changed("lifetime") {
				secs, err := lifetimeChange(lifetime, cur.LifetimeSecs)
				if err != nil {
					return err
				}
				req.LifetimeSecs = &secs
			}
			if network != "" {
				caps, err := c.Capabilities(ctx)
				if err != nil {
					return err
				}
				n, err := updatedNetwork(network, allow, deny, groups, noBaseline, caps, cur.Labels["agent"])
				if err != nil {
					return err
				}
				req.Network = n
			}
			if req.Name == nil && req.Labels == nil && req.IdleTimeoutSecs == nil && req.IdleAction == nil && req.Network == nil && req.LifetimeSecs == nil && req.MetricsExport == nil {
				return errors.New("nothing to change: name a flag (--name, --label, --unlabel, --idle, --on-idle, --lifetime, --network, --metrics-export)")
			}
			sb, err := c.UpdateSandbox(ctx, args[0], req)
			if err != nil {
				return err
			}
			ends := "never"
			if sb.ExpiresAt != nil {
				ends = sb.ExpiresAt.Local().Format(time.RFC3339)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s  name %s  network %s  idle %s  ends %s  labels %s\n", sb.ID,
				orDash(sb.Name), sb.Network.Mode, idleWithAction(sb), ends, formatLabels(sb.Labels))
			if req.MetricsExport != nil {
				fmt.Fprintf(cmd.OutOrStdout(), "metrics export %s\n", exportSummary(sb.MetricsExport))
			}
			return nil
		},
	}
	f := cmd.Flags()
	f.StringVar(&ctxFlag, "context", "", "which sandboxd to use")
	f.StringVar(&name, "name", "", `rename it ("" removes the name)`)
	f.StringArrayVar(&labels, "label", nil, "set a label, key=value, or remove one, key- (repeatable)")
	f.StringArrayVar(&unlabel, "unlabel", nil, "remove a label by key (repeatable)")
	f.DurationVar(&idle, "idle", 0, "terminate it after this long idle, e.g. 2h (0: never, where allowed)")
	f.StringVar(&onIdle, "on-idle", "", "what the idle timeout does: terminate, or suspend until something uses it")
	f.StringVar(&lifetime, "lifetime", "", "when it ends, from its creation: 45m sets it, +15m or -10m moves its end")
	f.StringVar(&network, "network", "", "none, allowlist or open, applied to the running sandbox")
	f.StringArrayVar(&allow, "allow", nil, "with --network allowlist: a host it may reach (repeatable)")
	f.StringArrayVar(&groups, "allow-group", nil, "with --network allowlist: a gateway allowlist group whose hosts it may reach (repeatable)")
	f.StringArrayVar(&deny, "deny", nil, "with --network: a host it may not reach (repeatable)")
	f.BoolVar(&noBaseline, "no-baseline", false, "with --network allowlist: only the hosts named, without the built-in ones")
	export.register(f, true)
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
// an agent's sandbox keeps its agent's API, without which it stops. groups
// go to the gateway by name, which adds their hosts.
func updatedNetwork(mode string, allow, deny, groups []string, noBaseline bool, caps api.Capabilities, agent string) (*api.NetworkPolicy, error) {
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
		if len(groups) > 0 {
			// Sent even empty: absent, it would be the server's default
			// list in the groups' place.
			if n.Allow == nil {
				n.Allow = []string{}
			}
			n.Groups = slices.Clone(groups)
		} else if len(n.Allow) == 0 {
			return nil, errors.New("an allowlist of nothing is refused; use --network none to reach nothing")
		}
	default:
		return nil, fmt.Errorf("--network %q: none, allowlist or open", mode)
	}
	n.Deny = policy.DedupeDomains(deny)
	return n, nil
}

// lifetimeChange is the lifetime --lifetime asks for, in seconds from the
// sandbox's creation: v itself, or cur moved by v when v has a sign.
func lifetimeChange(v string, cur int) (int, error) {
	d, err := time.ParseDuration(v)
	if err != nil || d%time.Second != 0 {
		return 0, fmt.Errorf("--lifetime %q: a duration in whole seconds, e.g. 45m, +15m or -10m", v)
	}
	secs := int(d / time.Second)
	if strings.HasPrefix(v, "+") || strings.HasPrefix(v, "-") {
		if cur == 0 {
			return 0, fmt.Errorf("--lifetime %s: it has no lifetime to move; give one, e.g. --lifetime 45m", v)
		}
		secs += cur
	}
	if secs <= 0 {
		return 0, fmt.Errorf("--lifetime %s: that ends it before it began; terminate it instead", v)
	}
	return secs, nil
}

// idleWithAction is idleText, followed by "(suspend)" when the idle timeout
// suspends the sandbox rather than terminating it.
func idleWithAction(sb api.Sandbox) string {
	if sb.IdleAction == api.IdleSuspend && sb.IdleTimeoutSecs > 0 {
		return idleText(sb.IdleTimeoutSecs) + " (suspend)"
	}
	return idleText(sb.IdleTimeoutSecs)
}

func idleText(secs int) string {
	if secs == 0 {
		return "never"
	}
	return (time.Duration(secs) * time.Second).String()
}

package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
)

func newEventsCmd() *cobra.Command {
	var ctxFlag string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "events SANDBOX",
		Short: "Show a sandbox's audit events: what it was asked to do and how it ended",
		Long: "Prints the events sandboxd recorded for a sandbox: creation with its policy and\n" +
			"labels, every process with its argv and exit code, files read and written,\n" +
			"network changes, and how it ended. Environment variables appear by name only.\n" +
			"A terminated sandbox is still answered by id after the server has forgotten it.",
		Args: cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := newClient(ctxFlag)
			if err != nil {
				return err
			}
			list, err := c.Events(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			if asJSON {
				enc := json.NewEncoder(cmd.OutOrStdout())
				for _, ev := range list.Events {
					if err := enc.Encode(ev); err != nil {
						return err
					}
				}
				return nil
			}
			return printEvents(cmd.OutOrStdout(), list)
		},
	}
	cmd.Flags().StringVar(&ctxFlag, "context", "", "which sandboxd to use")
	cmd.Flags().BoolVar(&asJSON, "json", false, "one JSON event per line")
	return cmd
}

func printEvents(out io.Writer, list api.EventList) error {
	if list.Truncated {
		fmt.Fprintln(out, "(older events omitted)")
	}
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, ev := range list.Events {
		fmt.Fprintf(tw, "%s\t%s\t%s\n", ev.Time.Local().Format(time.DateTime), ev.Type, eventDetail(ev))
	}
	return tw.Flush()
}

// eventDetail is the part of an event worth a column. Everything here came
// through a client — an argv, a path, a label — so all of it is printed safe.
func eventDetail(ev api.Event) string {
	var parts []string
	add := func(format string, a ...any) { parts = append(parts, fmt.Sprintf(format, a...)) }
	switch ev.Type {
	case api.EventSandboxCreated:
		add("image %s", termsafe.Clean(ev.Image))
		if ev.Name != "" {
			add("name %s", termsafe.Clean(ev.Name))
		}
		if ev.Network != nil {
			add("network %s", networkSummary(*ev.Network))
		}
		if len(ev.EnvNames) > 0 {
			add("env %s", termsafe.Clean(strings.Join(ev.EnvNames, ",")))
		}
		if ev.Bind != "" {
			add("bind %s", termsafe.Clean(ev.Bind))
		}
		if ev.Snapshot != "" {
			add("from %s", termsafe.Clean(ev.Snapshot))
		}
		if len(ev.Labels) > 0 {
			add("labels %s", formatLabels(ev.Labels))
		}
	case api.EventNetworkUpdated:
		if ev.Network != nil {
			add("network %s", networkSummary(*ev.Network))
		}
	case api.EventProcessStarted:
		add("pid %d", ev.PID)
		add("%s", termsafe.Clean(ev.Program))
		if ev.ArgCount > 0 {
			add("%d args sha256 %.12s", ev.ArgCount, ev.ArgsSHA256)
		}
	case api.EventProcessExited:
		if ev.ExitCode != nil {
			add("pid %d exit %d after %s", ev.PID, *ev.ExitCode, (time.Duration(ev.DurationMS) * time.Millisecond).Round(time.Millisecond))
		}
	case api.EventFileRead, api.EventFileWritten, api.EventFileRemoved:
		add("%s", termsafe.Clean(ev.Path))
		if ev.Bytes > 0 {
			add("%d bytes", ev.Bytes)
		}
	case api.EventTunnelOpened:
		add("port %d", ev.Port)
	case api.EventSnapshotCreated:
		add("%s, %d bytes", termsafe.Clean(ev.Snapshot), ev.Bytes)
	}
	if ev.Reason != "" {
		add("%s", termsafe.Clean(ev.Reason))
	}
	return strings.Join(parts, " · ")
}

func networkSummary(p api.NetworkPolicy) string {
	s := p.Mode
	if len(p.Allow) > 0 {
		s += " allow " + termsafe.Clean(strings.Join(p.Allow, ","))
	}
	if len(p.Deny) > 0 {
		s += " deny " + termsafe.Clean(strings.Join(p.Deny, ","))
	}
	return s
}

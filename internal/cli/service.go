package cli

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"
	"gopkg.in/yaml.v3"

	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
)

func newServiceCmd() *cobra.Command {
	var ctxFlag string
	cmd := &cobra.Command{
		Use:   "service",
		Short: "Services on a gateway: a sandbox spec and a count the gateway keeps running",
		Long: "A service is a sandbox spec and a number of replicas a sandbox-gateway keeps\n" +
			"true: it replaces a replica that fails its health check or is lost with its\n" +
			"node, spreads replicas across nodes, rolls a change out one replica at a\n" +
			"time, and routes HTTP to the healthy ones when the service is public.\n" +
			"Services need a gateway; a single sandboxd has none. See docs/fleet.md.",
	}
	cmd.PersistentFlags().StringVar(&ctxFlag, "context", "", "which gateway to use")

	var file string
	deploy := &cobra.Command{
		Use:   "deploy -f service.yaml",
		Short: "Create a service, or update it (a rolling update) if it exists",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			sp, err := readServiceFile(file)
			if err != nil {
				return err
			}
			c, _, err := newClient(ctxFlag)
			if err != nil {
				return err
			}
			s, err := c.DeployService(cmd.Context(), sp)
			verb := "created"
			if api.IsCode(err, api.CodeConflict) {
				s, err = c.UpdateService(cmd.Context(), sp)
				verb = "updated"
			}
			if err != nil {
				return err
			}
			out := cmd.OutOrStdout()
			fmt.Fprintf(out, "%s %s, revision %d\n", verb, termsafe.Clean(s.Spec.Name), s.Revision)
			if s.Rollout != nil && s.Rollout.State == api.RolloutInProgress && s.Rollout.To == s.Revision {
				fmt.Fprintf(out, "rolling out revision %d over revision %d; follow it with: sandbox-cli service get %s\n",
					s.Rollout.To, s.Rollout.From, termsafe.Clean(s.Spec.Name))
			}
			if s.URL != "" {
				fmt.Fprintf(out, "url: %s\n", termsafe.Clean(s.URL))
			}
			return nil
		},
	}
	deploy.Flags().StringVarP(&file, "file", "f", "", "the service's YAML spec (- for stdin); required")
	_ = deploy.MarkFlagRequired("file")

	ls := &cobra.Command{
		Use:     "ls",
		Aliases: []string{"list"},
		Short:   "List services",
		Args:    cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			c, _, err := newClient(ctxFlag)
			if err != nil {
				return err
			}
			list, err := c.Services(cmd.Context())
			if err != nil {
				return err
			}
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "NAME\tREADY\tREVISION\tROLLOUT\tURL")
			for _, s := range list {
				fmt.Fprintf(tw, "%s\t%d/%d\t%d\t%s\t%s\n", termsafe.Clean(s.Spec.Name), s.Ready, s.Desired,
					s.Revision, rolloutWord(s.Rollout), serviceDash(termsafe.Clean(s.URL)))
			}
			return tw.Flush()
		},
	}

	get := &cobra.Command{
		Use:   "get NAME",
		Short: "Show a service: its spec, its rollout, and each replica's health",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := newClient(ctxFlag)
			if err != nil {
				return err
			}
			s, err := c.Service(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			printService(cmd.OutOrStdout(), s)
			return nil
		},
	}

	scale := &cobra.Command{
		Use:   "scale NAME REPLICAS",
		Short: "Set how many replicas a service keeps",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := strconv.Atoi(args[1])
			if err != nil || n < 0 {
				return fmt.Errorf("replicas %q: a number, 0 or more", args[1])
			}
			c, _, err := newClient(ctxFlag)
			if err != nil {
				return err
			}
			s, err := c.ScaleService(cmd.Context(), args[0], n)
			if err != nil {
				return err
			}
			fmt.Fprintf(cmd.OutOrStdout(), "%s: %d replicas wanted, %d ready\n", termsafe.Clean(s.Spec.Name), s.Desired, s.Ready)
			return nil
		},
	}

	rm := &cobra.Command{
		Use:   "rm NAME...",
		Short: "Delete services and terminate their replicas",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c, _, err := newClient(ctxFlag)
			if err != nil {
				return err
			}
			for _, name := range args {
				if err := c.DeleteService(cmd.Context(), name); err != nil {
					return fmt.Errorf("%s: %w", name, err)
				}
			}
			return nil
		},
	}
	cmd.AddCommand(deploy, ls, get, scale, rm)
	return cmd
}

// readServiceFile reads a spec. Unknown keys are refused: a misspelt
// `public` or `health` would otherwise be a service deployed without what
// it was meant to have.
func readServiceFile(path string) (api.ServiceSpec, error) {
	var data []byte
	var err error
	if path == "-" {
		data, err = io.ReadAll(io.LimitReader(os.Stdin, 1<<20))
	} else {
		data, err = os.ReadFile(path)
	}
	if err != nil {
		return api.ServiceSpec{}, err
	}
	var sp api.ServiceSpec
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&sp); err != nil {
		if errors.Is(err, io.EOF) {
			return api.ServiceSpec{}, fmt.Errorf("%s: empty", path)
		}
		return api.ServiceSpec{}, fmt.Errorf("%s: %w", path, err)
	}
	if sp.Name == "" {
		return api.ServiceSpec{}, fmt.Errorf("%s: name is required", path)
	}
	return sp, nil
}

func rolloutWord(r *api.ServiceRollout) string {
	if r == nil {
		return "-"
	}
	return r.State
}

func serviceDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func printService(out io.Writer, s api.Service) {
	c := termsafe.Clean
	fmt.Fprintf(out, "name:      %s\n", c(s.Spec.Name))
	fmt.Fprintf(out, "owner:     %s", c(s.Owner))
	if s.Tenant != "" {
		fmt.Fprintf(out, " (tenant %s)", c(s.Tenant))
	}
	fmt.Fprintln(out)
	fmt.Fprintf(out, "image:     %s\n", serviceDash(c(s.Spec.Image)))
	if len(s.Spec.Command) > 0 {
		fmt.Fprintf(out, "command:   %s\n", c(strings.Join(s.Spec.Command, " ")))
	}
	fmt.Fprintf(out, "replicas:  %d ready of %d\n", s.Ready, s.Desired)
	fmt.Fprintf(out, "revision:  %d (serving %d)\n", s.Revision, s.Serving)
	if r := s.Rollout; r != nil {
		fmt.Fprintf(out, "rollout:   %s, %d -> %d", r.State, r.From, r.To)
		if r.Reason != "" {
			fmt.Fprintf(out, ": %s", c(r.Reason))
		}
		fmt.Fprintln(out)
	}
	if s.Restarts > 0 {
		fmt.Fprintf(out, "restarts:  %d\n", s.Restarts)
	}
	if s.Error != "" {
		fmt.Fprintf(out, "error:     %s\n", c(s.Error))
	}
	if s.URL != "" {
		fmt.Fprintf(out, "url:       %s\n", c(s.URL))
	} else if s.Spec.Public {
		fmt.Fprintln(out, "url:       - (public, but the gateway runs no router)")
	}
	if len(s.EnvNames) > 0 {
		fmt.Fprintf(out, "env:       %s\n", c(strings.Join(s.EnvNames, ", ")))
	}
	fmt.Fprintln(out)
	tw := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, "SANDBOX\tNODE\tREV\tSTATE\tRESTARTS\tLAST CHECK\tERROR")
	for _, r := range s.Replicas {
		last := "-"
		if r.LastCheck != nil {
			last = r.LastCheck.Local().Format(time.TimeOnly)
		}
		fmt.Fprintf(tw, "%s\t%s\t%d\t%s\t%d\t%s\t%s\n", c(r.Sandbox), c(r.Node), r.Revision, c(r.State), r.Restarts, last, serviceDash(c(r.LastError)))
	}
	_ = tw.Flush()
}

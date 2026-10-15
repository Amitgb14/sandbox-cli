package cli

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/agenthome"
	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/api"
	"github.com/Amitgb14/sandbox-cli/internal/termsafe"
)

// Agents are a layer on top of the sandbox, not the sandbox itself, so they
// live under one command: everything at the top level works for any command,
// and `sandbox-cli agent` is where the conveniences for coding agents are — a
// login kept between runs, the agent's own environment variables, headless
// runs and fallbacks. Each of those is built from the same API calls
// `run` makes; none of them can do anything `run` cannot.
func newAgentGroupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Run a coding agent in a sandbox, keeping its login between runs",
		Long: "Runs a coding agent in a new sandbox, in the sandbox user's home, like `run`.\n" +
			"On top of `run`, the agent's login is restored into the sandbox and saved again\n" +
			"when the run ends, and the agent's own environment variables are forwarded when\n" +
			"set — or, where one is not, the API key saved for it in Studio's Agents screen\n" +
			"(~/.config/sandbox/agent-keys.json). The sandbox needs no repository: ask the\n" +
			"agent to clone one.\n\n" +
			"Leading sandbox flags are consumed; everything after them, or after --, goes\n" +
			"to the agent.",
		Example: "  sandbox-cli agent claude\n" +
			"  sandbox-cli agent claude --network none -- --resume\n" +
			"  sandbox-cli agent codex --fallback claude -- exec \"clone github.com/you/app and fix its failing test\"",
	}
	cmd.AddCommand(newAgentListCmd(), newAgentStateCmd(), newAgentWaitCmd())
	for _, name := range agents.InteractiveNames() {
		d, _ := agents.LookupInteractive(name)
		cmd.AddCommand(newAgentCmd(d))
	}
	return cmd
}

func newAgentListCmd() *cobra.Command {
	var ctxFlag string
	cmd := &cobra.Command{
		Use:   "ls",
		Short: "List the agents, whether each can run unattended, and whether a login is saved",
		Long: "List the agents, whether each can run unattended, and whether a login is saved.\n\n" +
			"On a gateway that keeps agent logins, an agent run uses the login the gateway\n" +
			"keeps for you, and GATEWAY says whether there is one; THIS MACHINE is the login\n" +
			"saved here, which a first run gives a gateway that keeps none yet. Elsewhere\n" +
			"the login is this machine's alone.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			kept, onGateway := gatewayLogins(cmd.Context(), ctxFlag)
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			if onGateway {
				fmt.Fprintln(tw, "AGENT\tUNATTENDED\tGATEWAY\tTHIS MACHINE")
			} else {
				fmt.Fprintln(tw, "AGENT\tUNATTENDED\tLOGIN")
			}
			for _, name := range agents.InteractiveNames() {
				d, _ := agents.LookupInteractive(name)
				// Unattended means a verified headless mode: only those agents may
				// be fallen through to or run from Studio, because one that stops
				// to ask with nobody there does not fail, it hangs.
				unattended := "-"
				if _, ok := agents.Lookup(name); ok {
					unattended = "yes"
				}
				if !onGateway {
					fmt.Fprintf(tw, "%s\t%s\t%s\n", name, unattended, loginState(d))
					continue
				}
				gw := "-"
				switch {
				case len(d.AuthPaths) == 0:
					gw = "not kept"
				case !kept[name].IsZero():
					gw = "saved " + kept[name].Local().Format("2006-01-02 15:04")
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", name, unattended, gw, loginState(d))
			}
			return tw.Flush()
		},
	}
	cmd.Flags().StringVar(&ctxFlag, "context", "", "which endpoint to ask for the logins it keeps")
	return cmd
}

// gatewayLogins is when each agent's login kept on the context's gateway
// was last saved, and whether the context is a gateway that keeps logins at
// all. Anything else — a plain sandboxd, a gateway without a secrets key
// or older than agent logins, an endpoint that does not answer — is false,
// and ls shows this machine's logins, as a run there uses them. Only an
// endpoint named with --context that does not answer is worth a word: ls
// runs offline as often as not.
func gatewayLogins(ctx context.Context, ctxFlag string) (map[string]time.Time, bool) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	c, name, err := newClient(ctxFlag)
	if err != nil {
		if ctxFlag != "" {
			fmt.Fprintf(os.Stderr, "sandbox-cli: %v; showing this machine's logins\n", err)
		}
		return nil, false
	}
	gw, err := c.IsGateway(ctx)
	if err != nil || !gw {
		if err != nil && ctxFlag != "" {
			fmt.Fprintf(os.Stderr, "sandbox-cli: context %s: %v; showing this machine's logins\n", termsafe.Clean(name), err)
		}
		return nil, false
	}
	list, err := c.AgentLogins(ctx)
	if err != nil {
		var ae *api.Error
		if !errors.As(err, &ae) || (ae.Status != http.StatusNotImplemented && ae.Status != http.StatusNotFound) {
			fmt.Fprintf(os.Stderr, "sandbox-cli: context %s: %v; showing this machine's logins\n", termsafe.Clean(name), err)
		}
		return nil, false
	}
	// Keyed by name and read only for the agents this CLI knows: nothing
	// the gateway sends is printed as it came.
	out := map[string]time.Time{}
	for _, l := range list {
		out[l.Agent] = l.Updated
	}
	return out, true
}

// loginState says whether any of an agent's login files has been saved. It
// reports presence only: whether the login inside is still valid is a
// question for the agent's provider.
func loginState(d agents.Descriptor) string {
	if len(d.AuthPaths) == 0 {
		return "not kept"
	}
	dir := agenthome.LoginDir(d)
	for _, rel := range d.AuthPaths {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err == nil {
			return "saved"
		}
	}
	return "-"
}

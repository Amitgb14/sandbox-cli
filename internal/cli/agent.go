package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/agenthome"
	"github.com/Amitgb14/sandbox-cli/internal/agents"
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
	return &cobra.Command{
		Use:   "ls",
		Short: "List the agents, whether each can run unattended, and whether a login is saved",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			tw := tabwriter.NewWriter(cmd.OutOrStdout(), 0, 0, 2, ' ', 0)
			fmt.Fprintln(tw, "AGENT\tUNATTENDED\tLOGIN")
			for _, name := range agents.InteractiveNames() {
				d, _ := agents.LookupInteractive(name)
				// Unattended means a verified headless mode: only those agents may
				// be fallen through to or run from Studio, because one that stops
				// to ask with nobody there does not fail, it hangs.
				unattended := "-"
				if _, ok := agents.Lookup(name); ok {
					unattended = "yes"
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\n", name, unattended, loginState(d))
			}
			return tw.Flush()
		},
	}
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

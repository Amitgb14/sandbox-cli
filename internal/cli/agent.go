package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/agents"
	"github.com/Amitgb14/sandbox-cli/internal/workspace"
)

// Agents are a layer on top of the sandbox, not the sandbox itself, so they
// live under one command: everything at the top level works for any command,
// and `sandbox-cli agent` is where the conveniences for coding agents are — a
// login kept between runs, the agent's own environment variables, headless
// runs, fleets and fallbacks. Each of those is built from the same API calls
// `run` makes; none of them can do anything `run` cannot.
func newAgentGroupCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "agent",
		Short: "Run a coding agent in a sandbox, keeping its login between runs",
		Long: "Runs a coding agent in a new sandbox on a clone of this repository, and brings\n" +
			"its commits back to refs/sandbox/<name>, like `run`. On top of `run`, the agent's\n" +
			"login is restored into the sandbox and saved again when the run ends, and the\n" +
			"agent's own environment variables are forwarded when set.\n\n" +
			"Leading sandbox flags are consumed; everything after them, or after --, goes\n" +
			"to the agent.",
		Example: "  sandbox-cli agent claude\n" +
			"  sandbox-cli agent claude --network none -- --resume\n" +
			"  sandbox-cli agent codex --fallback claude -- exec \"fix the failing test\"\n" +
			"  sandbox-cli agent fleet run -f fleet.yaml",
	}
	cmd.AddCommand(newAgentListCmd(), newAgentStateCmd(), newAgentWaitCmd(), newFleetCmd())
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
				// run in a fleet or be fallen through to, because one that stops to
				// ask with nobody there does not fail, it hangs.
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
	dir := workspace.LoginDir(d)
	for _, rel := range d.AuthPaths {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(rel))); err == nil {
			return "saved"
		}
	}
	return "-"
}

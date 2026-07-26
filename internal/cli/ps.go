package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"
)

// Finding a sandbox again after the CLI is gone.
//
// Two things leave containers behind, and neither had a command:
//
//   - A `kill -9` on sandbox-cli. The daemon owns the container, not the `docker
//     run` client, so killing the client leaves the agent running and still
//     writing to /workspace. --rm is daemon-side, so it is reaped when it
//     eventually exits — but nothing stops it, and nothing tells the user it is
//     there.
//   - `--detach`, which sets Remove=false on purpose: the exit code and the logs
//     are the whole supervision story and --rm would discard both at the moment
//     they become interesting. Reaping was always meant to be "a later, explicit
//     step"; this is that step.
//
// Every container carries sandbox.repo/branch/agent labels, so both are findable.
// The labels are what makes this possible at all — docker is the state store, and
// a fact not stamped there is one no later command can recover.

// sandboxLabel is stamped on every container sandbox-cli starts, so `ps` finds
// exactly ours and nothing else on the machine.
//
// Deliberately sandbox.cli and not sandbox.repo: the repo/branch/agent labels
// describe the work and are omitted when there is nothing true to say, so a run
// started outside a git repository carried none of them and was invisible here.
const sandboxLabel = "sandbox.cli"

// psRow is one sandbox container as `docker ps` reports it.
type psRow struct {
	Name   string
	Status string
	Repo   string
	Branch string
	Agent  string
	Age    string
}

func newPsCmd() *cobra.Command {
	var all bool
	cmd := &cobra.Command{
		Use:   "ps",
		Short: "List sandbox containers, including ones left behind",
		Long: "List containers started by sandbox-cli.\n\n" +
			"Running ones include any orphaned by a killed sandbox-cli — the container\n" +
			"outlives the client, so an agent can still be working in your project with\n" +
			"nothing attached to it. --all also shows exited detached runs, which are\n" +
			"kept on purpose so their exit code and logs survive; `sandbox-cli clean`\n" +
			"removes those.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rows, err := sandboxContainers(all)
			if err != nil {
				return err
			}
			if len(rows) == 0 {
				if all {
					fmt.Println("no sandbox containers")
				} else {
					fmt.Println("no running sandbox containers (--all includes exited ones)")
				}
				return nil
			}
			w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
			fmt.Fprintln(w, "NAME\tAGENT\tBRANCH\tSTATUS")
			for _, r := range rows {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\n", r.Name, dash(r.Agent), dash(r.Branch), r.Status)
			}
			return w.Flush()
		},
	}
	cmd.Flags().BoolVarP(&all, "all", "a", false, "include exited containers (detached runs are kept after they finish)")
	return cmd
}

func newCleanCmd() *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "clean",
		Short: "Remove exited sandbox containers",
		Long: "Remove sandbox containers that have exited.\n\n" +
			"Detached runs are deliberately kept after they finish so their exit code and\n" +
			"`docker logs` survive; this is how you reap them once you have read what you\n" +
			"needed. Running containers are never touched without --force, because one of\n" +
			"them may be an agent still working in your project.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			rows, err := sandboxContainers(true)
			if err != nil {
				return err
			}
			var targets []psRow
			for _, r := range rows {
				running := strings.HasPrefix(r.Status, "Up")
				if running && !force {
					continue
				}
				targets = append(targets, r)
			}
			if len(targets) == 0 {
				fmt.Println("nothing to clean")
				return nil
			}
			for _, r := range targets {
				rm := exec.Command("docker", "rm", "-f", r.Name)
				if out, err := rm.CombinedOutput(); err != nil {
					fmt.Fprintf(os.Stderr, "sandbox-cli: removing %s: %s\n", r.Name, strings.TrimSpace(string(out)))
					continue
				}
				fmt.Printf("removed %s (%s)\n", r.Name, r.Status)
			}
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "also remove RUNNING containers — one may be an agent still working")
	return cmd
}

// sandboxContainers asks docker for our containers. The label filter is what
// keeps this to sandbox-cli's own containers rather than everything on the
// machine, and the format string is chosen so a value can never contain the
// separator (names, labels and status have no tabs).
func sandboxContainers(all bool) ([]psRow, error) {
	args := []string{"ps", "--filter", "label=" + sandboxLabel,
		"--format", "{{.Names}}\t{{.Status}}\t{{.Label \"sandbox.repo\"}}\t{{.Label \"sandbox.branch\"}}\t{{.Label \"sandbox.agent\"}}"}
	if all {
		args = append(args, "--all")
	}
	out, err := exec.Command("docker", args...).Output()
	if err != nil {
		return nil, fmt.Errorf("listing sandbox containers (is the docker daemon running?): %w", err)
	}
	return parsePsRows(string(out)), nil
}

// parsePsRows splits docker's tab-separated output.
//
// Trimming per line and only of line endings, never with TrimSpace on the whole
// output: the trailing fields are the optional labels, so a container with no
// repo/branch/agent — every run started outside a git repository — ends its line
// in empty fields, and TrimSpace ate them. The row then had two fields instead of
// five, failed a length check, and was silently dropped. Short rows are padded
// rather than skipped for the same reason: a missing label is a blank column, not
// a reason to hide the container.
func parsePsRows(out string) []psRow {
	var rows []psRow
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		for len(f) < 5 {
			f = append(f, "")
		}
		rows = append(rows, psRow{Name: f[0], Status: f[1], Repo: f[2], Branch: f[3], Agent: f[4]})
	}
	return rows
}

func dash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "-"
	}
	return s
}

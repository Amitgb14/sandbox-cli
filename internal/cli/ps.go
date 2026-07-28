package cli

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/config"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
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
	var engineFlag, cfgPath string
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
			bin := engineBin(cfgPath, engineFlag)
			rows, err := sandboxContainers(bin, all)
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
	cmd.Flags().StringVar(&engineFlag, "engine", "", "container engine: docker (default) or podman")
	cmd.Flags().StringVarP(&cfgPath, "config", "c", "", "explicit config file path")

	return cmd
}

func newCleanCmd() *cobra.Command {
	var force bool
	var engineFlag, cfgPath string
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
			bin := engineBin(cfgPath, engineFlag)
			rows, err := sandboxContainers(bin, true)
			if err != nil {
				return err
			}
			var targets []psRow
			for _, r := range rows {
				// Anything not plainly exited is treated as live: "Up", "Restarting",
				// "Removing", and "?" (status could not be matched) all mean do not
				// touch this without --force.
				running := !strings.HasPrefix(r.Status, "Exited") && !strings.HasPrefix(r.Status, "Created")
				if running && !force {
					continue
				}
				targets = append(targets, r)
			}
			if len(targets) == 0 {
				fmt.Println("nothing to clean")
				// Still try the network: "nothing to remove" is precisely the state in
				// which a leftover network is worth collecting, and returning early
				// meant it never was.
				removeSandboxNetworkIfUnused(bin)
				return nil
			}
			for _, r := range targets {
				rm := exec.Command(bin, "rm", "-f", r.Name)
				if out, err := rm.CombinedOutput(); err != nil {
					fmt.Fprintf(os.Stderr, "sandbox-cli: removing %s: %s\n", r.Name, strings.TrimSpace(string(out)))
					continue
				}
				fmt.Printf("removed %s (%s)\n", r.Name, r.Status)
			}
			removeSandboxNetworkIfUnused(bin)
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "also remove RUNNING containers — one may be an agent still working")
	cmd.Flags().StringVar(&engineFlag, "engine", "", "container engine: docker (default) or podman")
	cmd.Flags().StringVarP(&cfgPath, "config", "c", "", "explicit config file path")

	return cmd
}

// sandboxContainers asks docker for our containers. The label filter is what
// keeps this to sandbox-cli's own containers rather than everything on the
// machine, and the format string is chosen so a value can never contain the
// separator (names, labels and status have no tabs).
func sandboxContainers(bin string, all bool) ([]psRow, error) {
	base := []string{"ps", "--filter", "label=" + sandboxLabel}
	if all {
		base = append(base, "--all")
	}

	// The authoritative set of names comes from a format carrying NOTHING
	// attacker-controlled. Label values do: they are branch names and repo paths,
	// and a label containing a newline forged an entire extra row — whose Status
	// then parsed as empty, which is not "Up", so `clean` force-removed a
	// container sandbox-cli never created. Names are constrained by docker itself,
	// so a name list cannot be made to grow.
	nameOut, err := exec.Command(bin, append(append([]string{}, base...), "--format", "{{.Names}}")...).Output()
	if err != nil {
		return nil, fmt.Errorf("listing sandbox containers (is the docker daemon running?): %w", err)
	}
	real := map[string]bool{}
	var order []string
	for _, n := range strings.Split(string(nameOut), "\n") {
		if n = strings.TrimSpace(n); n != "" && !real[n] {
			real[n] = true
			order = append(order, n)
		}
	}
	if len(order) == 0 {
		return nil, nil
	}

	// Labels and status are for display only, and are matched back by name. A row
	// naming anything not in the set above is discarded rather than shown.
	detailOut, _ := exec.Command(bin, append(append([]string{}, base...),
		"--format", "{{.Names}}\t{{.Status}}\t{{.Label \"sandbox.repo\"}}\t{{.Label \"sandbox.branch\"}}\t{{.Label \"sandbox.agent\"}}")...).Output()
	byName := map[string]psRow{}
	for _, r := range parsePsRows(string(detailOut)) {
		if real[r.Name] {
			byName[r.Name] = r
		}
	}

	rows := make([]psRow, 0, len(order))
	for _, n := range order {
		if r, ok := byName[n]; ok {
			rows = append(rows, r)
			continue
		}
		rows = append(rows, psRow{Name: n, Status: "?"})
	}
	return rows, nil
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

// removeSandboxNetworkIfUnused reaps the shared network once nothing is on it.
//
// It is one long-lived object rather than one per run, so this is tidiness rather
// than the orphan problem a per-run network would have created — and it is
// deliberately conditional: removing a network another sandbox is still attached
// to would take that sandbox's networking away mid-run. docker refuses that
// anyway, but relying on the refusal would mean printing an error for a normal
// situation.
// engineBin is the container binary the supporting commands should speak to.
//
// ps, clean and stats used to hardcode docker, which made a detached podman run
// invisible to all three: `podman ps` showed it, `sandbox-cli ps` showed a
// docker container instead, and `clean` reported nothing to do while both sat
// there. A supervision command that cannot see the thing it supervises is worse
// than absent, because it answers.
func engineBin(cfgPath, engineFlag string) string {
	if engineFlag != "" {
		return engineFlag
	}
	wd, err := os.Getwd()
	if err != nil {
		wd = "."
	}
	cfg, err := config.LoadProfile(wd, cfgPath, "")
	if err != nil || cfg.Engine == "" {
		return "docker"
	}
	return cfg.Engine
}

func removeSandboxNetworkIfUnused(bin string) {
	// The reaper runs first, unconditionally. It used to sit behind the
	// "no containers left" guard, which meant it never ran on a podman-only
	// machine (sandboxContainers failed against a missing docker) and was
	// short-circuited on a mixed one by any docker container. Since a detached
	// run deliberately leaves its network with machine lifetime, that was a leak
	// with no collector.
	removeStaleSandboxNetworks(bin)

	rows, err := sandboxContainers(bin, true)
	if err != nil || len(rows) > 0 {
		return
	}
	if err := exec.Command(bin, "network", "inspect", runtime.SandboxNetwork).Run(); err != nil {
		return // not there; nothing to do
	}
	if out, err := exec.Command(bin, "network", "rm", runtime.SandboxNetwork).CombinedOutput(); err != nil {
		// Something outside sandbox-cli may be attached. Not an error worth
		// failing the command over.
		_ = out
		return
	}
	fmt.Printf("removed network %s\n", runtime.SandboxNetwork)
}

// removeStaleSandboxNetworks removes the per-run networks podman leaves behind.
//
// Scoped to the engine actually in use rather than sweeping both: deleting
// sandbox-cli-* networks on an engine this run was never configured for is a
// broader licence than the docker path takes.
//
// Best-effort and quiet on failure: a leaked network is untidy, and a `clean`
// that fails because of one is worse. Anything carrying the per-run prefix is
// unattached by the time it is reachable — a running container holds its network
// open, so `network rm` simply refuses and this moves on.
func removeStaleSandboxNetworks(bin string) {
	if _, err := exec.LookPath(bin); err != nil {
		return
	}
	out, err := exec.Command(bin, "network", "ls", "--format", "{{.Name}}").Output()
	if err != nil {
		return
	}
	for _, name := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		name = strings.TrimSpace(name)
		if !strings.HasPrefix(name, runtime.SandboxNetwork+"-") {
			continue
		}
		if err := exec.Command(bin, "network", "rm", name).Run(); err == nil {
			fmt.Printf("removed network %s\n", name)
		}
	}
}

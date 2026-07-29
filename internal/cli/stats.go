package cli

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

// statRow is one container's live resource usage.
type statRow struct {
	ID   string
	Name string
	Mem  string
	CPU  string
	PIDs string
}

func newStatsCmd() *cobra.Command {
	var (
		interval   time.Duration
		once       bool
		engineFlag string
		cfgPath    string
	)
	cmd := &cobra.Command{
		Use:   "stats",
		Short: "Live memory/CPU of running sandbox sessions",
		Long: "Shows a live, refreshing table of memory and CPU for every running sandbox\n" +
			"session. Run it in a second terminal alongside an interactive agent (where\n" +
			"the inline gauge can't be drawn). Press Ctrl-C to exit.\n\n" +
			"The ID column is the same one `sandbox-cli list` shows, so a row here can be\n" +
			"handed straight to `attach`, `logs` or `kill`.",
		RunE: func(cmd *cobra.Command, args []string) error {
			return runStats(engineBin(cfgPath, engineFlag), interval, once)
		},
	}
	cmd.Flags().DurationVar(&interval, "interval", 2*time.Second, "refresh interval")
	cmd.Flags().BoolVar(&once, "once", false, "print a single snapshot and exit")
	cmd.Flags().StringVar(&engineFlag, "engine", "", "container engine: docker (default) or podman")
	cmd.Flags().StringVarP(&cfgPath, "config", "c", "", "explicit config file path")
	return cmd
}

func runStats(dockerBin string, interval time.Duration, once bool) error {
	// Named after the engine actually configured, not after docker: on a
	// podman-only machine the old message told the user to install something they
	// had deliberately not installed.
	if _, err := exec.LookPath(dockerBin); err != nil {
		return fmt.Errorf("%s is not on your PATH, so there is nothing to sample\n"+
			"  install it, or select the engine you do have with --engine podman", dockerBin)
	}

	clear := isTerminalStats(os.Stdout) && !once

	if once {
		return renderStats(dockerBin, os.Stdout, false)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt)
	defer signal.Stop(sigCh)

	if clear {
		fmt.Print("\033[?25l")       // hide cursor
		defer fmt.Print("\033[?25h") // restore cursor on exit
	}

	for {
		if err := renderStats(dockerBin, os.Stdout, clear); err != nil {
			return err
		}
		select {
		case <-sigCh:
			fmt.Println()
			return nil
		case <-time.After(interval):
		}
	}
}

// renderStats draws one frame. When clear is set, it homes the cursor and clears
// the screen first (live mode on a terminal).
func renderStats(dockerBin string, out *os.File, clear bool) error {
	rows, err := collectSandboxStats(dockerBin)
	if err != nil {
		return err
	}
	if clear {
		fmt.Fprint(out, "\033[H\033[2J")
	}
	fmt.Fprintf(out, "\033[1msandbox-cli — live stats\033[0m  %s  (Ctrl-C to exit)\n\n",
		time.Now().Format("15:04:05"))
	if len(rows) == 0 {
		fmt.Fprintln(out, "  no sandbox containers running…")
		return nil
	}
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	fmt.Fprintln(tw, "ID\tCONTAINER\tMEM\tCPU\tPIDS")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n", r.ID, r.Name, r.Mem, r.CPU, r.PIDs)
	}
	return tw.Flush()
}

// collectSandboxStats returns live usage for running containers named sandbox-*.
// collectSandboxStats samples every running sandbox container.
//
// It retries once, because the two steps below cannot be made atomic: a sandbox
// that exits between the listing and the sampling makes `docker stats` fail for
// the whole batch, taking the other containers' readings with it. Watching
// containers that come and go is the entire job of this command, so that race is
// the normal case rather than an exotic one, and one fresh attempt settles it.
func collectSandboxStats(dockerBin string) ([]statRow, error) {
	rows, err := sampleSandboxStats(dockerBin)
	if err != nil {
		rows, err = sampleSandboxStats(dockerBin)
	}
	return rows, err
}

func sampleSandboxStats(dockerBin string) ([]statRow, error) {
	// Filtered by label, not by a `sandbox-` name prefix. The label is stamped on
	// every container this tool starts and nothing else; the name is a convention,
	// so the prefix both missed nothing today and would quietly start reporting
	// somebody else's `sandbox-postgres` tomorrow. It is also the filter `list`
	// uses, which is what makes the two commands describe the same set.
	ids, err := exec.Command(dockerBin, "ps",
		"--filter", "label="+sandbox.LabelCLI+"=1", "--format", "{{.ID}}").Output()
	if err != nil {
		return nil, fmt.Errorf("listing sandbox sessions: %w", err)
	}
	idList := strings.Fields(strings.TrimSpace(string(ids)))
	if len(idList) == 0 {
		return nil, nil
	}

	args := append([]string{"stats", "--no-stream",
		"--format", "{{.ID}}|{{.Name}}|{{.MemUsage}}|{{.CPUPerc}}|{{.PIDs}}"}, idList...)
	out, err := exec.Command(dockerBin, args...).Output()
	if err != nil {
		return nil, fmt.Errorf("reading %s stats: %w", dockerBin, err)
	}

	var rows []statRow
	for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if line == "" {
			continue
		}
		f := strings.SplitN(line, "|", 5)
		if len(f) != 5 {
			continue
		}
		rows = append(rows, statRow{ID: f[0], Name: f[1], Mem: f[2], CPU: f[3], PIDs: f[4]})
	}
	return rows, nil
}

func isTerminalStats(f *os.File) bool {
	fi, err := f.Stat()
	if err != nil {
		return false
	}
	return fi.Mode()&os.ModeCharDevice != 0
}

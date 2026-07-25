package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/agentusage"
)

type usageOpts struct {
	json bool
}

func newUsageCmd() *cobra.Command {
	o := usageOpts{}
	cmd := &cobra.Command{
		Use:   "usage",
		Short: "Show how much of the subscription window is left, and when it resets",
		Long: "Shows the two usage windows Claude Code reports — the 5-hour session window\n" +
			"and the weekly one — as a percentage spent and the time each resets.\n\n" +
			"Inside a `sandbox-cli claude` session the same numbers ride along on the\n" +
			"sandbox status line. This is for everywhere else: a second terminal, a run\n" +
			"that has already finished, or an agent whose UI has nowhere to put them.\n\n" +
			"These are cached numbers. There is no way to ask for a live reading without\n" +
			"an interactive session, so this reads the cache Claude Code keeps for its own\n" +
			"/usage display and always prints how old that reading is.\n\n" +
			"Only claude's windows are read. Codex records the same kind of figure, but\n" +
			"only under a ChatGPT plan and in a shape no sample here has confirmed; gemini,\n" +
			"opencode and goose record nothing of the kind. An agent whose numbers have not\n" +
			"been seen is reported as not recording them rather than guessed at.",
		Example: "  sandbox-cli usage\n" +
			"  sandbox-cli usage --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return runUsage(o) },
	}
	cmd.Flags().BoolVar(&o.json, "json", false, "emit the windows as JSON")
	return cmd
}

func runUsage(o usageOpts) error {
	paths := agentusage.ClaudePaths()
	snap, err := agentusage.Find(paths...)
	if err != nil {
		return err
	}
	if o.json {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(snap)
	}
	if snap.Empty() {
		printNoUsage(paths)
		return nil
	}
	printUsage(snap, time.Now())
	return nil
}

// printNoUsage is the "why is this empty?" answer given where the question
// arises, the same bargain `context list` makes: say where we looked, and name
// the two ordinary reasons before the user starts hunting for a bug.
func printNoUsage(paths []string) {
	fmt.Println("no usage recorded for claude yet")
	for _, p := range paths {
		fmt.Printf("  looked in %s\n", shortenHome(p))
	}
	fmt.Println("  claude writes these numbers once it has run signed in to a Claude.ai plan;")
	fmt.Println("  API-key auth has no subscription window to report")
}

func printUsage(s agentusage.Snapshot, now time.Time) {
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	for _, w := range s.Windows {
		left, when := resetCells(w.ResetsAt, now)
		row := fmt.Sprintf("%s\t%.0f%%\t%s", windowLabel(w.Kind, w.Scope), w.Percent, left)
		if when != "" {
			row += "\t" + when
		}
		fmt.Fprintln(tw, row)
	}
	tw.Flush()

	// Always say how stale the reading is. A percentage with no age on it is the
	// one way these numbers actively mislead: they are refreshed when the agent
	// talks to the server, so an idle machine can hold a figure from hours ago.
	line := s.Agent
	if !s.FetchedAt.IsZero() {
		line += ", as of " + humanAge(s.FetchedAt)
	} else {
		line += ", age unknown"
	}
	if s.Path != "" {
		line += " — " + shortenHome(s.Path)
	}
	fmt.Printf("\n%s\n", line)
}

// windowLabel names a window: the period, plus the model in parentheses when the
// window covers one model rather than the whole account. The scope has to be on
// the label — an unlabelled second weekly row reads as a second weekly
// allowance.
func windowLabel(kind, scope string) string {
	label := kind
	switch kind {
	case agentusage.KindFiveHour:
		label = "5h"
	case agentusage.KindSevenDay:
		label = "week"
	}
	if scope != "" {
		label += " (" + scope + ")"
	}
	return label
}

// resetCells renders when a window starts over as two columns: how long away,
// and the wall-clock time to plan around. A window with no reported reset time
// says so rather than showing a zero time that reads as "now".
func resetCells(at, now time.Time) (left, when string) {
	if at.IsZero() {
		return "reset time not reported", ""
	}
	at = at.Local()
	when = "(" + at.Format("15:04") + ")"
	if at.YearDay() != now.YearDay() || at.Year() != now.Year() {
		when = "(" + at.Format("Mon 15:04") + ")"
	}
	d := at.Sub(now)
	if d <= 0 {
		// Past its reset and not yet refreshed: the percentage beside it is
		// stale rather than wrong, and saying which is more use than a negative
		// countdown.
		return "due", when
	}
	return "resets in " + humanLeft(d), when
}

// humanLeft is the countdown, coarsened to the unit that matters at that
// distance: minutes within the hour, hours and minutes within the day, days
// beyond it.
func humanLeft(d time.Duration) string {
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return strings.TrimSuffix(fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60), "00m")
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

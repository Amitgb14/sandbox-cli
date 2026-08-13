package cli

import (
	"context"
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
	json    bool
	refresh bool
}

// refreshTimeout bounds the throwaway request --refresh makes. Generous enough
// for a cold start on a slow link, short enough that a hung agent does not hold
// a terminal open indefinitely.
const refreshTimeout = 2 * time.Minute

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
			"These are cached numbers: this reads the cache Claude Code keeps for its own\n" +
			"/usage display, and always prints how old that reading is. The cache only\n" +
			"refreshes when the agent talks to the server, so on an idle machine it can be\n" +
			"hours old — old enough that a window has since started over, in which case the\n" +
			"figure shown is the last one from the window before it. Those rows say `rolled\n" +
			"over` and show no percentage rather than a number about the wrong period.\n\n" +
			"--refresh asks claude for one throwaway turn first, which is the only way to\n" +
			"make the numbers current: it costs a request against the subscription being\n" +
			"measured, which is why it is opt-in. Claude Code still decides when to refetch,\n" +
			"so this makes a reading minutes old rather than hours — never stamped now.\n\n" +
			"Only claude's windows are read. Codex records the same kind of figure, but\n" +
			"only under a ChatGPT plan and in a shape no sample here has confirmed; gemini,\n" +
			"opencode and goose record nothing of the kind. An agent whose numbers have not\n" +
			"been seen is reported as not recording them rather than guessed at.",
		Example: "  sandbox-cli usage\n" +
			"  sandbox-cli usage --refresh\n" +
			"  sandbox-cli usage --json",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return runUsage(o) },
	}
	cmd.Flags().BoolVar(&o.json, "json", false, "emit the windows as JSON")
	cmd.Flags().BoolVar(&o.refresh, "refresh", false,
		"ask claude for one throwaway turn first, so the reading is current (costs a request)")
	return cmd
}

func runUsage(o usageOpts) error {
	paths := agentusage.ClaudePaths()
	snap, err := agentusage.Find(paths...)
	if err != nil {
		return err
	}
	if o.refresh {
		snap = refreshUsage(snap, paths)
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
	printUsage(snap, time.Now(), !o.refresh)
	return nil
}

// refreshUsage asks the agent for a current reading and re-reads the cache. A
// refresh that fails is reported and then stepped over rather than aborting the
// command: the cached numbers are still worth printing, and since every printing
// carries its own age, falling back to them cannot pass stale figures off as
// fresh.
func refreshUsage(snap agentusage.Snapshot, paths []string) agentusage.Snapshot {
	if !snap.NeedsRefresh(time.Now()) {
		// Already inside the interval Claude Code refetches on. A request here
		// would spend from the window it is trying to measure and change nothing.
		return snap
	}
	ctx, cancel := context.WithTimeout(context.Background(), refreshTimeout)
	defer cancel()
	if err := agentusage.Refresh(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		return snap
	}
	fresh, err := agentusage.Find(paths...)
	if err != nil || fresh.Empty() {
		return snap
	}
	return fresh
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

func printUsage(s agentusage.Snapshot, now time.Time, offerRefresh bool) {
	stale := false
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)
	for _, w := range s.Windows {
		stale = stale || w.RolledOver(now)
		left, when := resetCells(w.ResetsAt, now)
		row := fmt.Sprintf("%s\t%s\t%s", windowLabel(w.Kind, w.Scope), percentCell(w, now), left)
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

	// An abandoned cache is not a stale one, and offering --refresh for it sells
	// a request that cannot buy anything: the file is being written, the reading
	// in it is not, so driving the agent changes nothing. Said here, once,
	// instead of leaving someone to discover it by spending.
	if s.Abandoned() {
		fmt.Println("that file is still being written, but this reading is not — the agent has")
		fmt.Println("stopped recording usage there, so --refresh cannot make it current")
		return
	}

	// Say where a current reading comes from, but only when something on screen
	// is actually out of date. Advertising a flag that costs a request under
	// numbers that are already good would be selling the spend for nothing.
	if stale && offerRefresh {
		fmt.Println("run `sandbox-cli usage --refresh` for a current reading")
	}
}

// percentCell renders how much of a window is spent, or a dash once that window
// has started over. The cached percentage then describes the period *before* the
// reset — accurate when it was written, about the wrong week by the time it is
// read. A dash cannot be misread the way a stale 25% can; the `rolled over` in
// the next column says why it is there.
func percentCell(w agentusage.Window, now time.Time) string {
	if w.RolledOver(now) {
		return "—"
	}
	return fmt.Sprintf("%.0f%%", w.Percent)
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
		// Past its reset and not refreshed since: the window has started over
		// and whatever was measured of it belongs to the period before. Naming
		// that is more use than a negative countdown, and it is what the dash in
		// the percent column is answering for.
		return "rolled over", when
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

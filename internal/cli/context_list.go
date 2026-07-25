package cli

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/Amitgb14/sandbox-cli/internal/agentctx"
	"github.com/Amitgb14/sandbox-cli/internal/config"
)

// defaultContextListLimit keeps a listing to a screenful. Anything cut is
// announced — a listing that silently stops reads as "that is all there is".
const defaultContextListLimit = 20

type contextListOpts struct {
	scope   contextScope
	project string
	all     bool
	limit   int
	json    bool
	verbose bool
	full    bool
}

func newContextListCmd(sc contextScope) *cobra.Command {
	o := contextListOpts{scope: sc}
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List the conversations an agent has had here",
		Long: "Lists the conversations in each agent's store, newest first, with the id you\n" +
			"resume by.\n\n" +
			"Scoped to the project you are in, when the agent's store can be scoped that\n" +
			"way — Claude Code keeps a directory per project, so its listing is exact.\n" +
			"Stores that shard by date instead (Codex) cannot be narrowed that way and are\n" +
			"listed whole, which the output says. `--all` lists every project.\n\n" +
			"When there is nothing to list, this says where it looked and why it came up\n" +
			"empty. `--verbose` shows the same detail alongside a listing that did work.\n\n" +
			"Sessions from a store sandbox-cli can locate but not yet read are listed with\n" +
			"their id and date, and \"?\" where the title and turn count would be — the file\n" +
			"is there, the format is not understood yet.\n\n" +
			"Ids are abbreviated; sandbox-cli expands one back to the full id before the\n" +
			"agent sees it. Use --full when you need the whole id — running the agent\n" +
			"directly, outside sandbox-cli, requires it (`claude --resume <full-id>`).",
		Example: "  sandbox-cli context list\n" +
			"  sandbox-cli claude context list\n" +
			"  sandbox-cli claude context list --full\n" +
			"  sandbox-cli claude context list --all --limit 0",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error { return runContextList(o) },
	}
	cmd.Flags().StringVar(&o.project, "project", "", "list sessions for this project (default: the current directory)")
	cmd.Flags().BoolVar(&o.all, "all", false, "list sessions for every project in the store")
	cmd.Flags().IntVar(&o.limit, "limit", defaultContextListLimit, "show at most this many sessions (0 for all)")
	cmd.Flags().BoolVar(&o.json, "json", false, "emit the sessions as JSON")
	cmd.Flags().BoolVarP(&o.verbose, "verbose", "v", false, "also show where each agent's sessions are stored")
	cmd.Flags().BoolVarP(&o.full, "full", "f", false, "print whole session ids (needed to resume outside sandbox-cli)")
	return cmd
}

// listedAgent pairs an agent's sessions with how they were found, so the printer
// can be honest about a listing that could not be narrowed to this project.
type listedAgent struct {
	finding  agentctx.Finding
	sessions []agentctx.Session
	scoped   bool
	hidden   int // trimmed by --limit, reported rather than dropped silently
}

func runContextList(o contextListOpts) error {
	project := o.project
	if project == "" {
		project = o.scope.project // --project given to the agent wrapper
	}
	if project == "" {
		wd, err := os.Getwd()
		if err != nil {
			return err
		}
		project = wd
	}
	abs, err := filepath.Abs(config.ExpandTilde(project))
	if err != nil {
		return err
	}

	agents, err := contextListAgents(o.scope.agent)
	if err != nil {
		return err
	}
	if len(agents) == 0 {
		return explainNoStore(o.scope.agent)
	}

	var listed []listedAgent
	total := 0
	for _, f := range agents {
		sessions, scoped, lerr := agentctx.List(f, agentctx.ListOpts{Project: abs, All: o.all})
		if lerr != nil {
			// One unreadable store must not hide the others.
			fmt.Fprintf(os.Stderr, "sandbox-cli: reading %s sessions: %v\n", f.Agent, lerr)
		}
		if len(sessions) == 0 {
			continue
		}
		total += len(sessions)
		// Applied here rather than in the printer, so `--json` and the table show
		// the same sessions. A limit that only trims what you can see would make
		// the two disagree about what "the list" is.
		la := listedAgent{finding: f, sessions: sessions, scoped: scoped}
		if o.limit > 0 && len(sessions) > o.limit {
			la.sessions, la.hidden = sessions[:o.limit], len(sessions)-o.limit
		}
		listed = append(listed, la)
	}

	if o.json {
		return emitSessionsJSON(listed)
	}
	if total == 0 {
		printNoSessions(agents, abs, o.all)
		return nil
	}
	return printSessions(listed, o, abs)
}

// contextListAgents returns the stores worth listing: the one asked for, or every
// store that has been verified.
func contextListAgents(agent string) ([]agentctx.Finding, error) {
	reg := agentctx.LoadRegistry()
	roots, now := agentctx.DefaultRoots(), time.Now()
	if agent != "" {
		f, ok := agentctx.Resolve(agent, roots, now)
		if !ok {
			return nil, nil
		}
		return []agentctx.Finding{f}, nil
	}
	var out []agentctx.Finding
	for _, f := range reg.Findings() {
		if f.State == agentctx.StateVerified {
			out = append(out, f)
		}
	}
	if len(out) == 0 {
		// Nothing recorded yet: probe once, so a first-time `context list` works
		// without the user having to ask for a probe they should not have to know about.
		fresh, rerr := agentctx.Refresh(roots, now)
		if rerr == nil {
			for _, f := range fresh.Findings() {
				if f.State == agentctx.StateVerified {
					out = append(out, f)
				}
			}
		}
	}
	return out, nil
}

// explainNoStore is the "why is this empty?" answer, given at the moment the
// question arises. Where an agent keeps its sessions is real information, but it
// is not worth a command of its own — nobody wants to know until a listing comes
// up empty, and then they want it immediately, without being sent somewhere else.
func explainNoStore(agent string) error {
	if agent == "" {
		return fmt.Errorf("no agent sessions found on this machine\n" +
			"  no agent has run in a sandbox yet, or their session stores are somewhere unexpected\n" +
			"  run an agent once (e.g. sandbox-cli claude) and try again")
	}
	if _, known := agentctx.Lookup(agent); !known {
		return fmt.Errorf("sandbox-cli does not know where %s keeps its sessions yet\n"+
			"  %s can still run in a sandbox; only this listing is missing\n"+
			"  see docs/proposals/shared-context.md for what it takes to add a store", agent, agent)
	}
	var b strings.Builder
	fmt.Fprintf(&b, "no %s sessions found on this machine — has %s run in a sandbox yet?", agent, agent)
	for _, dir := range agentctx.CandidateDirs(agent, agentctx.DefaultRoots()) {
		fmt.Fprintf(&b, "\n  looked in %s", shortenHome(dir))
	}
	if !hasPersistedHome(agent) {
		// No persisted home at all. That is either an agent that has never run, or
		// one always run with --no-persist-auth; both look identical from here, so
		// the note states the rule rather than accusing the user of using the flag.
		fmt.Fprintf(&b, "\n  note: %s has no persisted home here — with --no-persist-auth it is\n"+
			"        thrown away with the container, sessions included", agent)
	}
	return fmt.Errorf("%s", b.String())
}

// hasPersistedHome reports whether the agent has a sandbox-owned home at all. A
// user who always passes --no-persist-auth has no sessions by construction, and
// saying so is kinder than letting them hunt for a directory that was never kept.
func hasPersistedHome(agent string) bool {
	dir := config.AgentStateDir(agent)
	if dir == "" {
		return false
	}
	fi, err := os.Stat(dir)
	return err == nil && fi.IsDir()
}

// printNoSessions covers the other empty case: the store is there, this project
// just has nothing in it.
func printNoSessions(agents []agentctx.Finding, project string, all bool) {
	if all {
		fmt.Println("no sessions in any store")
		return
	}
	names := make([]string, 0, len(agents))
	for _, f := range agents {
		names = append(names, f.Agent)
	}
	sort.Strings(names)
	fmt.Printf("no %s sessions for %s\n", strings.Join(names, "/"), project)
	for _, f := range agents {
		fmt.Printf("  %s\n", storeLine(f))
	}
	fmt.Println("try --all to list the sessions in other projects")
}

// storeLine describes one agent's store in a sentence: what is in it, where it
// is, and how sure we are. Shown when a listing is empty, and under --verbose
// when it is not.
func storeLine(f agentctx.Finding) string {
	s := fmt.Sprintf("%s: %d session(s) in %s", f.Agent, f.Sessions, shortenHome(f.Dir))
	if len(f.Locations) > 1 {
		s += fmt.Sprintf(" (and %d other location)", len(f.Locations)-1)
	}
	if f.Stale {
		// The path was confirmed before but is out of sight now, so the count is
		// the last one seen rather than the one on disk this second.
		s += fmt.Sprintf(", not visible right now — last seen %s", humanAge(f.VerifiedAt))
	}
	return s
}

func printSessions(listed []listedAgent, o contextListOpts, project string) error {
	multi := len(listed) > 1
	tw := tabwriter.NewWriter(os.Stdout, 0, 2, 2, ' ', 0)

	header := "ID\tWHEN\tTURNS\tTITLE"
	if multi {
		header = "AGENT\t" + header
	}
	if o.all {
		header = strings.TrimSuffix(header, "\tTITLE") + "\tPROJECT\tTITLE"
	}
	fmt.Fprintln(tw, header)

	hidden := 0
	for _, la := range listed {
		hidden += la.hidden
		for _, s := range la.sessions {
			var row []string
			if multi {
				row = append(row, la.finding.Agent)
			}
			row = append(row, sessionIDCell(s.ID, o.full), humanAge(s.Modified), turnsCell(s))
			if o.all {
				row = append(row, projectCell(s))
			}
			row = append(row, titleCell(s))
			fmt.Fprintln(tw, strings.Join(row, "\t"))
		}
	}
	if err := tw.Flush(); err != nil {
		return err
	}

	if hidden > 0 {
		fmt.Printf("\n… %d more not shown (--limit 0 for all)\n", hidden)
	} else {
		fmt.Println()
	}
	// A listing that could not be narrowed must say so, or an agent's whole
	// history reads as this project's history.
	for _, la := range listed {
		if !la.scoped && !o.all {
			fmt.Printf("%s: every project (its store is not organised by project)\n", la.finding.Agent)
		}
	}
	if o.verbose {
		for _, la := range listed {
			fmt.Printf("%s\n", storeLine(la.finding))
		}
	}
	printResumeHint(listed, o.full)
	return nil
}

// printResumeHint turns the listing into something actionable, using the resume
// argv from the verified descriptor rather than a hardcoded flag.
//
// When more than one agent is listed it also says that the ids are not
// interchangeable. A single table of every agent's conversations reads as one
// pool of resumable things, and the obvious next move — taking an id off any row
// and handing it to whichever agent you like — fails with a message from the
// agent that explains nothing ("No saved session found with ID ..."). The table
// created that impression, so the table corrects it.
func printResumeHint(listed []listedAgent, full bool) {
	for _, la := range listed {
		if len(la.finding.Resume) == 0 || len(la.sessions) == 0 {
			continue
		}
		fmt.Printf("resume: sandbox-cli %s %s %s\n",
			la.finding.Agent, strings.Join(la.finding.Resume, " "), sessionIDCell(la.sessions[0].ID, full))
	}
	if len(listed) > 1 {
		fmt.Println("\nnote: an id belongs to the agent that created it — a claude session cannot be")
		fmt.Println("      resumed by codex, and vice versa. Carrying a conversation across agents is")
		fmt.Println("      a handoff, not a resume (docs/proposals/shared-context.md).")
	}
}

// sessionIDCell renders an id for display: abbreviated by default, whole under
// --full.
//
// Abbreviated is right for the common case, where the id is read on screen and
// handed straight back to sandbox-cli, which expands it. It is wrong for the
// other case: prefix expansion is sandbox-cli's own glue, so an abbreviated id
// fails when the agent is run directly (`claude --resume 37888763` is refused as
// not-a-UUID). A claude session recorded in a sandbox lives in the user's real
// ~/.claude history, so reaching for it outside the sandbox is a normal thing to
// want — hence a flag rather than a choice made for them.
func sessionIDCell(id string, full bool) string {
	if full {
		return id
	}
	return shortSessionID(id)
}

// shortSessionID is the first block of a UUID: long enough to be unambiguous in
// practice and short enough to read.
//
// The agents do *not* accept it — Claude Code refuses anything that is not a
// full UUID. sandbox-cli expands it on the way through (expandResumeID), which
// is what lets this listing stay narrow without printing an id you cannot use.
func shortSessionID(id string) string {
	if i := strings.IndexByte(id, '-'); i == 8 {
		return id[:i]
	}
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func turnsCell(s agentctx.Session) string {
	if s.Partial {
		return "?"
	}
	return fmt.Sprint(s.Turns)
}

func titleCell(s agentctx.Session) string {
	const max = 64
	t := s.Title
	if t == "" {
		if s.Partial {
			return "?"
		}
		return "(no prompts yet)"
	}
	if len(t) > max {
		return t[:max-1] + "…"
	}
	return t
}

func projectCell(s agentctx.Session) string {
	if s.Project == "" {
		return "?"
	}
	return shortenHome(s.Project)
}

func emitSessionsJSON(listed []listedAgent) error {
	out := []agentctx.Session{}
	for _, la := range listed {
		out = append(out, la.sessions...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Modified.After(out[j].Modified) })
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(out)
}

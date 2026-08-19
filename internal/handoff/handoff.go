// Package handoff carries one agent's conversation to another.
//
// It exists because routing changes the agent mid-task, and an agent that
// inherits nothing starts the work again from the prompt — re-reading the same
// files, re-deciding the same things, and sometimes undoing what the first one
// established.
//
// **It is a briefing, not a resume, and that is a limit rather than a shortcut.**
// A session id is a primary key into one vendor's private store: Claude writes
// `~/.claude/projects/<bucket>/<uuid>.jsonl`, Codex writes `rollout-*.jsonl`
// under a date-sharded tree, and neither will ever look up the other's id. The
// schemas differ entirely, and so do the semantics — a Claude transcript is full
// of tool_use blocks naming Edit, Bash and TodoWrite, tools the target does not
// have. docs/proposals/shared-context.md weighs transcribing one format into the
// other and rejects it: the target ends up believing a fabricated history,
// confidently, with file-writing tools, and the first upstream schema change
// corrupts it silently.
//
// So what crosses is what survives translation:
//
//	HANDOFF.md        what was being done, what was decided, what is still open
//	transcript.jsonl  the conversation, normalized to {role, text, at}
//	files.md          which files the previous agent actually changed
//
// `files.md` carries the most signal per byte and is the part that cannot be
// wrong in an interesting way: it is a diffstat, derived from the workspace
// rather than from anything the agent said about itself. "These nine files
// changed" survives a translation between vendors perfectly; a summary of intent
// does not.
//
// Everything here is **deterministic** — no network, no API key, no token cost —
// which is what makes it testable in `make test` and what makes it safe to run
// in the middle of a failover, when the provider that would have written a
// nicer summary is the one that just went down.
package handoff

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/agentctx"
	"github.com/Amitgb14/sandbox-cli/internal/worktree"
)

// GuestDir is where an export is mounted inside the container. Read-only: the
// briefing is evidence about a run that has finished, and an agent that could
// rewrite it could rewrite its own history.
const GuestDir = "/sandbox/context"

// maxBriefTurns bounds what the brief quotes. A long session is thousands of
// turns and the useful part is what was asked and what was concluded, not every
// exchange in between — and a brief that fills the target's context window with
// the previous agent's chatter has spent the budget it was supposed to save.
const maxBriefTurns = 40

// Export is one prepared handoff.
type Export struct {
	// Dir is the host directory to mount at GuestDir.
	Dir string
	// From is the agent whose conversation this is.
	From string
	// Turns is how many prompts the source session held, for the line that says
	// what is being handed over.
	Turns int
	// Files is how many files it changed, from the workspace rather than from
	// the transcript.
	Files int
}

// Write produces an export for the conversation in sessionPath, describing work
// done in workspace, under dir.
//
// A missing or unreadable transcript is **not** an error: the point of a
// handoff is that the first agent failed, and an agent that died before writing
// anything is exactly the case routing fires on. The export is still written,
// with what is known — usually the file ledger alone — because "here is what
// changed on disk, there was no conversation" is a true and useful briefing.
func Write(dir, from, sessionPath, workspace string, base string) (*Export, error) {
	// 0750/0640 rather than 0700/0600, and this is the difference between a
	// briefing and a directory the agent cannot open.
	//
	// The export is bind-mounted into the container, which on native Linux runs
	// as 1001:<the host user's primary gid> (sandbox/hostgroup.go). A directory
	// created by os.MkdirTemp is 0700 and owned by the host uid, so the guest
	// gets EACCES on every file — silently, since the run has already printed
	// that the briefing was carried, and the agent is told by its own prompt to
	// read a path it cannot. macOS hides this entirely: Docker Desktop
	// virtualizes bind-mount ownership, so it is a bug that only appears where
	// most unattended runs happen.
	//
	// Group bits are enough; no chown is needed, because the container's gid is
	// the host user's own. Not world-readable: /tmp is shared, and the brief
	// quotes a conversation.
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	// MkdirTemp made it 0700 before this was called, and MkdirAll leaves an
	// existing directory's mode alone.
	if err := os.Chmod(dir, 0o750); err != nil {
		return nil, err
	}
	ex := &Export{Dir: dir, From: from}

	var msgs []agentctx.Message
	if sessionPath != "" {
		// `from` names the agent, so its recorded format decides the reader — a
		// briefing built from a sniff that missed would be an empty transcript
		// handed to the next agent as though the conversation had crossed.
		format := ""
		if store, ok := agentctx.Lookup(from); ok {
			format = store.Format
		}
		if m, err := agentctx.TranscriptOf(format, sessionPath, maxBriefTurns); err == nil {
			msgs = m
		}
	}
	for _, m := range msgs {
		if m.Role == "user" {
			ex.Turns++
		}
	}

	stats := changedFiles(workspace, base)
	ex.Files = len(stats)

	if err := writeJSONL(filepath.Join(dir, "transcript.jsonl"), msgs); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "files.md"), []byte(fileLedger(stats)), 0o640); err != nil {
		return nil, err
	}
	if err := os.WriteFile(filepath.Join(dir, "HANDOFF.md"), []byte(brief(from, msgs, stats)), 0o640); err != nil {
		return nil, err
	}
	return ex, nil
}

// Prompt is the sentence prepended to the fallback agent's prompt.
//
// It says three things, and the third is the one that keeps this honest: where
// the briefing is, what it contains, and that it is a briefing — the target must
// not be told it is resuming a conversation it never had, or it will answer as
// though it remembers things it does not.
func (e *Export) Prompt(original string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "A previous agent (%s) was working on this task and stopped before finishing. "+
		"Its briefing is mounted read-only at %s: HANDOFF.md (what was being done and decided), "+
		"transcript.jsonl (%d prompts of that conversation), and files.md (%d files it changed). "+
		"Read them first. This is a briefing, not a resumed conversation — you did not have it, "+
		"so treat it as evidence about work in progress rather than as your own memory.\n\n",
		e.From, GuestDir, e.Turns, e.Files)
	b.WriteString("The original task follows.\n\n")
	b.WriteString(original)
	return b.String()
}

// changedFiles is what the previous agent did to the workspace, asked of git
// rather than of the agent. base is the branch the work is measured against, and
// may be empty — then only uncommitted work is reported, which is where an
// interrupted agent's output usually still is.
func changedFiles(workspace, base string) []worktree.FileStat {
	if workspace == "" {
		return nil
	}
	seen := map[string]worktree.FileStat{}
	order := []string{}
	add := func(st worktree.FileStat) {
		if _, ok := seen[st.Path]; !ok {
			order = append(order, st.Path)
		}
		cur := seen[st.Path]
		cur.Path = st.Path
		if cur.Status == "" {
			cur.Status = st.Status
		}
		cur.Insertions += st.Insertions
		cur.Deletions += st.Deletions
		cur.Binary = cur.Binary || st.Binary
		seen[st.Path] = cur
	}
	for _, st := range worktree.WorkingStatIn(workspace) {
		add(st)
	}
	if base != "" {
		if branch := worktree.HeadBranch(workspace); branch != "" && branch != base {
			for _, st := range worktree.DiffStat(workspace, branch, base) {
				add(st)
			}
		}
	}
	out := make([]worktree.FileStat, 0, len(order))
	for _, p := range order {
		out = append(out, seen[p])
	}
	return out
}

func fileLedger(stats []worktree.FileStat) string {
	var b strings.Builder
	b.WriteString("# Files the previous agent changed\n\n")
	if len(stats) == 0 {
		b.WriteString("None. The workspace is as it was, so nothing here is half-finished —\n")
		b.WriteString("start from the task itself.\n")
		return b.String()
	}
	b.WriteString("Derived from the workspace with git, not from anything the agent said about\n")
	b.WriteString("itself. This is the part of the briefing that cannot be wrong in an\n")
	b.WriteString("interesting way.\n\n")
	for _, st := range stats {
		status := st.Status
		if status == "" {
			status = "modified"
		}
		if st.Binary {
			fmt.Fprintf(&b, "- `%s` — %s, binary\n", st.Path, status)
			continue
		}
		fmt.Fprintf(&b, "- `%s` — %s, +%d/-%d\n", st.Path, status, st.Insertions, st.Deletions)
	}
	return b.String()
}

// brief is the deterministic HANDOFF.md.
//
// User turns are quoted verbatim, because a prompt is what somebody actually
// asked and paraphrasing it loses the only unambiguous thing in the transcript.
// Assistant turns are reduced to their first line — in practice the heading or
// the conclusion — because the body is reasoning the target cannot verify and
// should not inherit as fact.
func brief(from string, msgs []agentctx.Message, stats []worktree.FileStat) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# Handoff from %s\n\n", from)
	fmt.Fprintf(&b, "Written %s by sandbox-cli, deterministically, from the previous run's\n",
		time.Now().UTC().Format(time.RFC3339))
	b.WriteString("transcript and workspace. No model wrote this summary, so it does not\n")
	b.WriteString("interpret — it quotes and it counts.\n\n")

	b.WriteString("## This is a briefing, not a resume\n\n")
	fmt.Fprintf(&b, "You are not %s and you did not have this conversation. Session ids do not\n", from)
	b.WriteString("cross between agents and neither do transcripts: what follows is evidence\n")
	b.WriteString("about work in progress, to be checked against the files rather than trusted\n")
	b.WriteString("as memory.\n\n")

	b.WriteString("## What was asked\n\n")
	var prompts []string
	for _, m := range msgs {
		if m.Role == "user" && strings.TrimSpace(m.Text) != "" {
			prompts = append(prompts, m.Text)
		}
	}
	if len(prompts) == 0 {
		b.WriteString("_Nothing recorded — the previous agent stopped before writing a transcript._\n\n")
	} else {
		for _, p := range prompts {
			fmt.Fprintf(&b, "> %s\n\n", strings.ReplaceAll(strings.TrimSpace(p), "\n", "\n> "))
		}
	}

	b.WriteString("## What it said it was doing\n\n")
	said := 0
	for _, m := range msgs {
		if m.Role != "assistant" {
			continue
		}
		if line := firstLine(m.Text); line != "" {
			fmt.Fprintf(&b, "- %s\n", line)
			said++
		}
	}
	if said == 0 {
		b.WriteString("_Nothing recorded._\n")
	}
	b.WriteString("\n")

	b.WriteString("## Where the work stands\n\n")
	if len(stats) == 0 {
		b.WriteString("No files changed. Nothing is half-finished.\n")
	} else {
		fmt.Fprintf(&b, "%d file(s) changed — see files.md for the ledger. Read them before\n", len(stats))
		b.WriteString("editing: they are the previous agent's work in progress, not a finished\n")
		b.WriteString("state, and they may contradict what it said above.\n")
	}
	return b.String()
}

// firstLine is the heading-or-conclusion reduction: the first non-empty line,
// bounded, with markdown heading marks stripped so a list of them reads as a list.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(line), "#"))
		if line == "" {
			continue
		}
		// By runes, not bytes: this lands in HANDOFF.md, which is handed to
		// another agent, and slicing a UTF-8 string at a byte offset writes an
		// invalid sequence whenever a multibyte character straddles it.
		if r := []rune(line); len(r) > 200 {
			line = string(r[:200]) + "…"
		}
		return line
	}
	return ""
}

// normalized is the neutral message shape — the only fields that mean the same
// thing to every agent. Tool calls are deliberately absent: their names and ids
// are vendor-specific, and a target that read them would be reading about tools
// it does not have.
type normalized struct {
	Role string `json:"role"`
	Text string `json:"text"`
	At   string `json:"at,omitempty"`
}

func writeJSONL(path string, msgs []agentctx.Message) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o640)
	if err != nil {
		return err
	}
	defer f.Close()
	enc := json.NewEncoder(f)
	for _, m := range msgs {
		at := ""
		if !m.At.IsZero() {
			at = m.At.UTC().Format(time.RFC3339)
		}
		if err := enc.Encode(normalized{Role: m.Role, Text: m.Text, At: at}); err != nil {
			return err
		}
	}
	return nil
}

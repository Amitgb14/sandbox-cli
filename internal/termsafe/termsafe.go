// Package termsafe strips the characters a terminal reads as commands from
// strings that came from somewhere untrusted.
//
// sandbox-cli prints a lot of text the agent chose: session titles and project
// paths out of transcripts, filenames out of the workspace, model names out of a
// file the agent writes. All of it goes to the user's terminal, where an ESC is
// not a character but an instruction — screen clears, window titles, and on
// permissive terminals a clipboard write via OSC 52. A listing of what an agent
// did should not be able to act on the machine reading it.
//
// This is a package rather than a helper in each caller because it was a helper
// in each caller: three near-identical copies existed, and an audit then found
// two more display paths that had none — `git status -z` filenames and the
// `context list` project column. One name to reach for is the difference between
// remembering and forgetting.
//
// Sanitising is deliberately at the point where the value enters something the
// user will read, not at the point of print: a value that has been through Clean
// stays safe wherever it is later composed.
package termsafe

import "strings"

// Clean replaces every control character with a space and collapses the runs, so
// the result is a single printable line.
//
// The ranges are C0 (which includes ESC), DEL, and C1 — everything a terminal
// may interpret. Printable non-ASCII is kept, because a filename or a title in
// any language is ordinary text and mangling it would be its own bug.
func Clean(s string) string {
	return strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
			return ' '
		}
		return r
	}, s)), " ")
}

// FirstLine is Clean for values where only the first line is wanted — a title,
// where the rest of a multi-line prompt is not a summary of it. The truncation
// happens before the control characters are replaced, so an embedded newline
// still ends the line rather than becoming a space.
func FirstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, "\r\n"); i >= 0 {
		s = strings.TrimSpace(s[:i])
	}
	return Clean(s)
}

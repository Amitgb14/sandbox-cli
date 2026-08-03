package studioapi

import (
	"strconv"
	"strings"
)

// parseUnifiedDiff turns git's unified diff into the hunks a viewer renders.
//
// Line numbers are tracked on both sides as the hunk is walked, because that is
// the one thing a reader needs that the text does not carry: a `-` line has an
// old number and no new one, an added line the reverse, and context has both.
// Getting that wrong is not a cosmetic bug — it is a diff that points at the
// wrong line of somebody's file.
//
// Everything before the first @@ is dropped: the `diff --git`, index and
// ---/+++ headers say which file this is, which the caller already knows,
// having asked for it.
func parseUnifiedDiff(text string) []DiffHunk {
	hunks := []DiffHunk{}
	if text == "" {
		return hunks
	}

	var cur *DiffHunk
	var oldNo, newNo int

	for _, line := range strings.Split(text, "\n") {
		if strings.HasPrefix(line, "@@") {
			if cur != nil {
				hunks = append(hunks, *cur)
			}
			o, n := parseHunkHeader(line)
			oldNo, newNo = o, n
			cur = &DiffHunk{Header: line, Lines: []DiffLine{}}
			continue
		}
		if cur == nil {
			continue // preamble
		}

		switch {
		case strings.HasPrefix(line, "+"):
			no := newNo
			cur.Lines = append(cur.Lines, DiffLine{Kind: "add", NewNo: &no, Content: line[1:]})
			newNo++
		case strings.HasPrefix(line, "-"):
			no := oldNo
			cur.Lines = append(cur.Lines, DiffLine{Kind: "del", OldNo: &no, Content: line[1:]})
			oldNo++
		case strings.HasPrefix(line, " "):
			o, n := oldNo, newNo
			cur.Lines = append(cur.Lines, DiffLine{Kind: "ctx", OldNo: &o, NewNo: &n, Content: line[1:]})
			oldNo++
			newNo++
		case strings.HasPrefix(line, "\\"):
			// "\ No newline at end of file" — a fact about the file, not a line of
			// it, so it advances neither counter.
			cur.Lines = append(cur.Lines, DiffLine{Kind: "meta", Content: strings.TrimSpace(line)})
		}
	}
	if cur != nil {
		hunks = append(hunks, *cur)
	}
	return hunks
}

// parseHunkHeader reads the starting line numbers out of `@@ -a,b +c,d @@`.
// An unreadable header yields 1,1 rather than dropping the hunk: the content is
// still worth showing, and a wrong starting number is a smaller lie than a
// change that appears not to exist.
func parseHunkHeader(header string) (oldNo, newNo int) {
	oldNo, newNo = 1, 1
	fields := strings.Fields(header)
	for _, f := range fields {
		switch {
		case strings.HasPrefix(f, "-"):
			oldNo = firstNumber(f[1:])
		case strings.HasPrefix(f, "+"):
			newNo = firstNumber(f[1:])
		}
	}
	return oldNo, newNo
}

func firstNumber(s string) int {
	if i := strings.IndexByte(s, ','); i >= 0 {
		s = s[:i]
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 1
	}
	return n
}

// addedFileHunk presents a whole file as one addition, for something git has
// nothing to compare against — an untracked file, which for an agent that
// scaffolds a project is most of what it produced.
func addedFileHunk(content string) []DiffHunk {
	if content == "" {
		return []DiffHunk{}
	}
	lines := strings.Split(strings.TrimSuffix(content, "\n"), "\n")
	h := DiffHunk{
		Header: "@@ -0,0 +1," + strconv.Itoa(len(lines)) + " @@",
		Lines:  make([]DiffLine, 0, len(lines)),
	}
	for i, l := range lines {
		no := i + 1
		h.Lines = append(h.Lines, DiffLine{Kind: "add", NewNo: &no, Content: l})
	}
	return []DiffHunk{h}
}

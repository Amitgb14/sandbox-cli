package termsafe

import "testing"

// TestCleanRemovesEverythingATerminalInterprets is the property that matters:
// whatever goes in, nothing capable of instructing a terminal comes out. The
// specific sequences below are the ones an audit demonstrated reaching the user's
// terminal — window-title spoofing via OSC, screen clears, and OSC 52 clipboard
// writes on terminals that permit them.
func TestCleanRemovesEverythingATerminalInterprets(t *testing.T) {
	inputs := []string{
		"\x1b[2J\x1b[H",              // clear screen, home cursor
		"\x1b]0;PWNED\x07",           // set window title
		"\x1b]52;c;cGF5bG9hZA==\x07", // clipboard write
		"a\x1bb", "x\x9bc", "\x00\x07", "\x7f",
		"file\nname", "tab\tsep",
	}
	for _, in := range inputs {
		got := Clean(in)
		for _, r := range got {
			if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) {
				t.Errorf("Clean(%q) = %q still carries control character %q", in, got, r)
			}
		}
	}
	// Ordinary text, including non-ASCII, must survive intact — mangling a
	// filename or a title in any language would be its own bug.
	for in, want := range map[string]string{
		"plain title":           "plain title",
		"rapport-financiér.bin": "rapport-financiér.bin",
		"日本語のファイル":              "日本語のファイル",
		"  spaced   out  ":      "spaced out",
		"":                      "",
	} {
		if got := Clean(in); got != want {
			t.Errorf("Clean(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestFirstLineTruncatesBeforeSanitising pins the ordering. A newline has to end
// the line rather than become a space, or a multi-line prompt would be joined
// into one long title instead of truncated.
func TestFirstLineTruncatesBeforeSanitising(t *testing.T) {
	if got := FirstLine("first line\nsecond line"); got != "first line" {
		t.Errorf("FirstLine = %q, want %q", got, "first line")
	}
	if got := FirstLine("\x1b[31mtitle\x1b[0m\nrest"); got != "[31mtitle [0m" {
		t.Errorf("FirstLine = %q", got)
	}
}

package sandbox

import (
	"os"
	"strings"
)

// What a container knows about the terminal it is drawing on, which by default
// is almost nothing.
//
// `docker run -t` sets TERM=xterm and stops there, so an agent's TUI inside the
// sandbox draws for an eight-colour terminal while the same agent on the host
// has 256 and truecolor. goose's start-up banner is the visible case — present
// in a terminal, absent through sandbox-cli — and every colourised diff and
// progress bar is the quiet one. Measured: `tput colors` reports 8 inside and
// 256 on the host that started the run.
//
// Forwarded as **names**, the bargain timezone.go makes for TZ: a name is a
// short string the host publishes to everything it starts, not a path and not a
// capability.
//
// The catch, and the reason this is not simply `-e TERM=$TERM`: a name is only
// useful if the container can resolve it. The image ships ncurses-base, which
// carries xterm, screen, tmux, rxvt, vt100 and friends — and *not* the names
// modern terminals report for themselves. Forwarding `xterm-ghostty` verbatim
// leaves `tput` answering "unknown terminal", and `less` — git's default pager —
// printing "WARNING: terminal is not fully functional / Press RETURN" and then
// **waiting for a keystroke**. That is worse than the eight colours this exists
// to fix, so an unresolvable name is translated rather than passed on.
//
// hostTerminal is a var for the reason hostTimezone is: BuildSpec must produce
// the same spec on every machine, and this is one of the two inputs that
// genuinely differ per machine.
var hostTerminal = resolveHostTerminal

// consoleTerm describes the terminal that will attach to a console run rather
// than the one that started it: a Studio console container is created by a
// daemon, and what attaches later is xterm.js, which emulates a 256-colour
// xterm and speaks 24-bit colour.
const (
	consoleTerm      = "xterm-256color"
	consoleColorterm = "truecolor"
)

// knownTerminfo is what the base image can actually resolve — `ls /lib/terminfo`
// on it, which is ncurses-base and nothing more.
//
// A list rather than a probe because BuildSpec is pure: it renders an argv and
// does not start containers to ask them questions. It is deliberately the *small*
// set: a name that is missing costs a downgrade to xterm-256color, while a name
// wrongly assumed present costs a pager that hangs.
//
// A user-supplied `image:` may carry less than this, but that was equally true of
// the `xterm` docker set before any of this — so the floor is unchanged.
var knownTerminfo = map[string]bool{
	"ansi": true, "dumb": true, "linux": true, "pcansi": true, "sun": true,
	"vt100": true, "vt102": true, "vt220": true, "vt52": true,
	"rxvt": true, "rxvt-basic": true, "rxvt-unicode": true, "rxvt-unicode-256color": true,
	"screen": true, "screen-256color": true, "screen-bce": true, "screen.xterm-256color": true,
	"tmux": true, "tmux-256color": true,
	"xterm": true, "xterm-256color": true, "xterm-color": true, "xterm-debian": true,
	"xterm-mono": true, "xterm-vt220": true, "xterm-xfree86": true,
	"Eterm": true, "Eterm-color": true, "cygwin": true, "hurd": true,
}

// resolveHostTerminal reports the names to forward, empty when the host does not
// say. Empty is an answer: a wrong TERM is worse than a plain one, because it
// makes a program draw for a terminal that is not there.
func resolveHostTerminal() (term, colorterm string) {
	return sanitizeTermName(os.Getenv("TERM")), sanitizeTermName(os.Getenv("COLORTERM"))
}

// sanitizeTermName keeps a value that looks like a terminal name and drops
// anything else. These are read from the host environment and rendered into a
// `docker run -e` argument, so they are checked at the point of use — the rule
// validZoneName keeps for a zone read off the filesystem.
func sanitizeTermName(s string) string {
	if s == "" || len(s) > 64 {
		return ""
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-', r == '_', r == '.', r == '+':
		default:
			return ""
		}
	}
	return s
}

// containerTerm translates the host's terminal into one the container can look
// up, preserving what the translation is for: colour.
//
// A name the image knows is passed through — a tmux or screen user keeps the
// entry describing their actual terminal. Anything else becomes xterm-256color
// when the host looks 256-capable, which is the whole point of the exercise, and
// otherwise "" — leaving docker's own `xterm`, which is exactly where this
// started and therefore not a regression for anybody.
func containerTerm(term, colorterm string) string {
	if knownTerminfo[term] {
		return term
	}
	if term == "" {
		return ""
	}
	if strings.Contains(term, "256color") || colorterm != "" {
		return consoleTerm
	}
	return ""
}

// applyTerminal fills in TERM and COLORTERM for a run that has a pty, leaving
// anything the user said alone.
//
// Only with a pty. Without one there is no terminal to describe, and a TERM in a
// pipe invites escape codes into a log somebody will read as text — which is why
// docker itself only sets it with -t.
//
// `seen` carries names already forwarded by EnvNames, resolved on the host at
// exec time: a run forwarding TERM by name has answered this question, and
// answering it again would render the name twice and let the two disagree.
func applyTerminal(env map[string]string, seen map[string]bool, tty, console bool) {
	// One condition, not two. Console implies a pty today — BuildSpec sets
	// `tty = opts.Console` for a detached run — and a second source of truth for
	// that is how a later caller ends up with TERM on a container started without
	// -t, which is the outcome this whole file is careful to avoid.
	if !tty {
		return
	}

	term, colorterm := hostTerminal()
	if console {
		// Unconditionally, not only when the daemon has no terminal of its own.
		// studio.sh starts the daemon with `nohup … &` from an interactive shell,
		// so it inherits that shell's TERM — and a console container told
		// `xterm-ghostty` because a developer happened to launch Studio from
		// Ghostty is describing a terminal that will never be attached to it.
		term, colorterm = consoleTerm, consoleColorterm
	} else {
		term = containerTerm(term, colorterm)
	}

	set := func(name, value string) {
		if value == "" {
			return
		}
		if _, already := env[name]; already || seen[name] {
			return
		}
		env[name] = value
	}
	set("TERM", term)
	set("COLORTERM", colorterm)
}

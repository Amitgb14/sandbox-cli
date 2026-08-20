package sandbox

import "os"

// What a container knows about the terminal it is drawing on, which by default
// is almost nothing.
//
// `docker run -t` sets TERM=xterm and stops there, so an agent's TUI inside the
// sandbox is drawing for an eight-colour terminal while the same agent on the
// host has xterm-256color and COLORTERM=truecolor. Programs that ask before they
// draw then take their plain path. goose's start-up banner is the visible case —
// present in a terminal, absent through sandbox-cli, with nothing in the output
// to say why — and the quieter cost is every colourised diff, progress bar and
// highlight looking worse inside the sandbox than outside it. Measured: `tput
// colors` reports 8 in the container and 256 on the host that started it.
//
// Forwarded as **names**, which is the bargain timezone.go already makes for TZ:
// a name is a short string the host publishes to every program it starts, not a
// path and not a capability. The container's own terminfo interprets it, and a
// name it does not recognise degrades to something drawable rather than to
// nothing.
//
// Neither name is privileged. Both are read long after the privilege drop by the
// agent itself, so they are settings rather than instructions and do not belong
// on config.IsReservedEnv — unlike SANDBOX_UMASK, which is reserved for reach
// even though it too is read after the drop.
//
// hostTerminal is a var for the reason hostTimezone is: BuildSpec must produce
// the same spec on every machine, and this is one of the two inputs that
// genuinely differ per machine.
var hostTerminal = resolveHostTerminal

// consoleTerm describes the terminal that will attach to a console run, rather
// than the one that started it.
//
// A Studio console container is created by a daemon with no terminal of its own,
// so there is nothing on the host to copy — and what eventually attaches is
// xterm.js in a browser, which emulates a 256-colour xterm. Naming what will be
// there beats forwarding what happens to be here.
const consoleTerm = "xterm-256color"

// resolveHostTerminal reports the names to forward, empty when the host does not
// say. Empty is an answer: a wrong TERM is worse than a plain one, because it
// makes a program draw for a terminal that is not there.
func resolveHostTerminal() (term, colorterm string) {
	return sanitizeTermName(os.Getenv("TERM")), sanitizeTermName(os.Getenv("COLORTERM"))
}

// sanitizeTermName keeps a value that looks like a terminal name and drops
// anything else. These are read from the host environment and rendered into a
// `docker run -e` argument, so they are checked at the point of use — the rule
// validZoneName keeps for a zone name read off the filesystem.
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

// applyTerminal fills in TERM and COLORTERM for a run that has a pty, leaving
// anything the user said alone.
//
// Only with a pty. Without one there is no terminal to describe, and a TERM in a
// pipe invites escape codes into a log somebody will read as text — which is why
// docker itself only sets it with -t.
//
// `seen` carries the names already being forwarded by EnvNames. Those are read
// from the host environment at exec time, so a run forwarding TERM by name has
// answered this question and must not be answered again here.
func applyTerminal(env map[string]string, seen map[string]bool, tty, console bool) {
	if !tty && !console {
		return
	}
	term, colorterm := hostTerminal()
	if console && term == "" {
		term = consoleTerm
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

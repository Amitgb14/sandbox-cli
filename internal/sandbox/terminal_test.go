package sandbox

import "testing"

// pinTerminal makes the developer's own terminal irrelevant to a test, the way
// pinTimezone does for their zone.
func pinTerminal(t *testing.T, term, colorterm string) {
	t.Helper()
	prev := hostTerminal
	hostTerminal = func() (string, string) { return term, colorterm }
	t.Cleanup(func() { hostTerminal = prev })
}

func yes(b bool) *bool { return &b }

// The feature: an agent's TUI inside the sandbox should know it has the same
// terminal the agent outside it has. Without this docker says `xterm` and the
// program draws for eight colours.
func TestBuildSpecForwardsTheTerminalWhenThereIsOne(t *testing.T) {
	pinTerminal(t, "xterm-256color", "truecolor")
	spec, err := BuildSpec(baseCfg(), Options{
		Project: t.TempDir(),
		Command: []string{"sh"},
		TTY:     yes(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Env["TERM"] != "xterm-256color" {
		t.Errorf("Env[TERM] = %q, want xterm-256color", spec.Env["TERM"])
	}
	if spec.Env["COLORTERM"] != "truecolor" {
		t.Errorf("Env[COLORTERM] = %q, want truecolor", spec.Env["COLORTERM"])
	}
}

// No pty, no terminal to describe. A TERM in a pipe invites escape codes into a
// log somebody will read as text, which is why docker itself only sets it with
// -t — and why a captured `sandbox-cli run` must look exactly as it did before.
func TestBuildSpecOmitsTheTerminalWithoutAPty(t *testing.T) {
	pinTerminal(t, "xterm-256color", "truecolor")
	spec, err := BuildSpec(baseCfg(), Options{
		Project: t.TempDir(),
		Command: []string{"sh"},
		TTY:     yes(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"TERM", "COLORTERM"} {
		if v, ok := spec.Env[name]; ok {
			t.Errorf("Env[%s] = %q on a run with no terminal, want it absent", name, v)
		}
	}
}

// A default, and every default here yields to the user saying otherwise.
func TestBuildSpecTerminalYieldsToTheUser(t *testing.T) {
	pinTerminal(t, "xterm-256color", "truecolor")
	spec, err := BuildSpec(baseCfg(), Options{
		Project: t.TempDir(),
		Command: []string{"sh"},
		TTY:     yes(true),
		Env:     []string{"TERM=dumb"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Env["TERM"] != "dumb" {
		t.Errorf("Env[TERM] = %q, want the explicit dumb", spec.Env["TERM"])
	}
}

// Forwarding by name means "use whatever the host has at exec time", which is
// the same answer arrived at by a different route. Setting it here as well would
// render the name twice and let the two disagree.
func TestBuildSpecTerminalYieldsToForwardingByName(t *testing.T) {
	pinTerminal(t, "xterm-256color", "truecolor")
	cfg := baseCfg()
	cfg.EnvAllow = []string{"TERM"}
	t.Setenv("TERM", "screen-256color")
	spec, err := BuildSpec(cfg, Options{
		Project: t.TempDir(),
		Command: []string{"sh"},
		TTY:     yes(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := spec.Env["TERM"]; ok {
		t.Errorf("Env[TERM] = %q while TERM is also forwarded by name; one of them is wrong", v)
	}
	found := false
	for _, n := range spec.EnvNames {
		if n == "TERM" {
			found = true
		}
	}
	if !found {
		t.Error("TERM was neither set nor forwarded by name")
	}
}

// A console container is told about the terminal that will attach to it, not the
// one that started it.
//
// Pinned with a host terminal *present*, because that is the case that occurs:
// studio.sh starts the daemon with `nohup … &` from an interactive shell, so it
// inherits that shell's TERM. The earlier version of this test pinned the host to
// nothing and so only exercised the branch that never happens in production.
func TestBuildSpecNamesTheTerminalAConsoleWillGet(t *testing.T) {
	pinTerminal(t, "xterm-ghostty", "truecolor")
	spec, err := BuildSpec(baseCfg(), Options{
		Project: t.TempDir(),
		Command: []string{"sh"},
		Detach:  true,
		Console: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if spec.Env["TERM"] != consoleTerm {
		t.Errorf("Env[TERM] = %q on a console run, want %q", spec.Env["TERM"], consoleTerm)
	}
	// Both names or neither: xterm.js speaks 24-bit colour, and a console told
	// about its 256-colour terminfo and nothing about truecolor is half an answer.
	if spec.Env["COLORTERM"] != consoleColorterm {
		t.Errorf("Env[COLORTERM] = %q on a console run, want %q", spec.Env["COLORTERM"], consoleColorterm)
	}
}

// A detached run with no console has nobody to draw for.
func TestBuildSpecOmitsTheTerminalOnADetachedRun(t *testing.T) {
	pinTerminal(t, "xterm-256color", "truecolor")
	spec, err := BuildSpec(baseCfg(), Options{
		Project: t.TempDir(),
		Command: []string{"sh"},
		Detach:  true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := spec.Env["TERM"]; ok {
		t.Errorf("Env[TERM] = %q on a detached run with no console, want it absent", v)
	}
}

// These are read from the host environment and rendered into a `docker run -e`
// argument, so they are checked at the point of use rather than trusted for
// being local — the rule validZoneName keeps for a zone read off the filesystem.
func TestTerminalNamesAreCheckedBeforeTheyReachTheArgv(t *testing.T) {
	for _, bad := range []string{
		"xterm; rm -rf /",
		"xterm 256color",
		"xterm\n-e",
		"$(whoami)",
		"`id`",
		string(make([]byte, 65)),
	} {
		if got := sanitizeTermName(bad); got != "" {
			t.Errorf("sanitizeTermName(%q) = %q, want it dropped", bad, got)
		}
	}
	for _, good := range []string{"xterm", "xterm-256color", "screen.linux", "rxvt-unicode-256color", "truecolor", "24bit"} {
		if got := sanitizeTermName(good); got != good {
			t.Errorf("sanitizeTermName(%q) = %q, want it kept", good, got)
		}
	}
}

// The finding that made this whole feature a regression rather than an
// improvement, for anybody on a modern terminal.
//
// The image ships ncurses-base, which knows xterm and screen and tmux and not
// `xterm-ghostty`, `xterm-kitty` or `alacritty`. Forwarded verbatim, `tput`
// answers "unknown terminal" and `less` — git's default pager — prints
// "WARNING: terminal is not fully functional / Press RETURN" and waits for a
// keystroke. Measured against the real image: a `git log` inside the sandbox
// blocks where before it paged.
//
// So an unresolvable name is translated to one that is present and keeps the
// colour, which is what the forwarding was for.
func TestBuildSpecTranslatesATerminalTheImageCannotResolve(t *testing.T) {
	for _, tc := range []struct {
		name      string
		term      string
		colorterm string
		want      string
	}{
		{"ghostty", "xterm-ghostty", "truecolor", "xterm-256color"},
		{"kitty", "xterm-kitty", "truecolor", "xterm-256color"},
		{"alacritty", "alacritty", "", ""},
		{"alacritty with colorterm", "alacritty", "truecolor", "xterm-256color"},
		{"foot", "foot-extra", "24bit", "xterm-256color"},
		// Names the image really has are passed through: a tmux user keeps the
		// entry that describes their actual terminal.
		{"tmux", "tmux-256color", "truecolor", "tmux-256color"},
		{"screen", "screen-256color", "", "screen-256color"},
		{"plain xterm", "xterm", "", "xterm"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			pinTerminal(t, tc.term, tc.colorterm)
			spec, err := BuildSpec(baseCfg(), Options{
				Project: t.TempDir(),
				Command: []string{"sh"},
				TTY:     yes(true),
			})
			if err != nil {
				t.Fatal(err)
			}
			got := spec.Env["TERM"]
			if got != tc.want {
				t.Errorf("TERM=%q on the host became %q in the container, want %q", tc.term, got, tc.want)
			}
			if got != "" && !knownTerminfo[got] {
				t.Errorf("forwarded %q, which the image cannot look up — tput fails and less blocks", got)
			}
		})
	}
}

// An unresolvable name with nothing to say about colour leaves docker's own
// `xterm` in place. That is where this started, so it cannot be a regression for
// anybody — the improvement is for terminals that can prove they deserve it.
func TestBuildSpecLeavesDockersDefaultWhenItCannotDoBetter(t *testing.T) {
	pinTerminal(t, "some-unknown-terminal", "")
	spec, err := BuildSpec(baseCfg(), Options{
		Project: t.TempDir(),
		Command: []string{"sh"},
		TTY:     yes(true),
	})
	if err != nil {
		t.Fatal(err)
	}
	if v, ok := spec.Env["TERM"]; ok {
		t.Errorf("Env[TERM] = %q, want it absent so docker's own xterm stands", v)
	}
}

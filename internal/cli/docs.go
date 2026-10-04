package cli

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// WriteReference writes docs/cli.md: every command of the tree NewRootCmd
// builds, with its usage line, its help, its examples and its flags.
//
// It is generated rather than written because the help text is the thing
// users read first and the one most likely to move: a hand-kept reference
// drifts the first time a flag is renamed, and nobody notices until a reader
// types the old one. TestCLIReferenceIsCurrent fails when docs/cli.md and
// this output differ, so the two change in the same commit.
//
// The Markdown is written here rather than by cobra's doc package, which
// would bring a man-page module into go.mod for a feature we do not use.
//
// Long help is put in a plain text block, as the terminal shows it, not
// re-flowed into prose: it is written for an 80-column terminal, with
// indented examples and aligned lists that Markdown would re-flow or read as
// syntax (`<id>` as a tag, `*.example.com` as emphasis).
func WriteReference(w io.Writer) error {
	root := NewRootCmd()
	var b strings.Builder
	b.WriteString("# sandbox-cli command reference\n\n")
	b.WriteString("<!-- Generated from the command tree by `make docs-cli` (internal/cli/docs.go). Do not edit by hand. -->\n\n")
	b.WriteString("Every command and subcommand of `sandbox-cli`, as `sandbox-cli COMMAND --help` prints it.\n")
	b.WriteString("Flags go before `--`; everything after it is the sandbox's command. `sandbox-cli completion`\n")
	b.WriteString("(shell completion scripts) and `sandbox-cli help` are cobra's own and not listed.\n\n")
	b.WriteString("Setting up what these commands talk to is in [self-hosting.md](self-hosting.md),\n")
	b.WriteString("[local-macos.md](local-macos.md) and, for the gateway-only commands (`ssh`, `job`,\n")
	b.WriteString("`service`, `secret`, `org`, `gateway`), [fleet.md](fleet.md).\n\n")

	writeFlagTable(&b, "Global flags", root.PersistentFlags())

	b.WriteString("## Commands\n\n| Command | |\n|---|---|\n")
	walk(root, func(c *cobra.Command, depth int) {
		if depth == 0 {
			return
		}
		fmt.Fprintf(&b, "| [`%s`](#%s) | %s |\n", c.CommandPath(), anchor(c.CommandPath()), cell(c.Short))
	})
	b.WriteString("\n")

	walk(root, func(c *cobra.Command, depth int) {
		if depth == 0 {
			return
		}
		// Top-level commands are sections; subcommands sit under them, so the
		// page's outline is the tree's.
		level := "##"
		if depth > 1 {
			level = "###"
		}
		fmt.Fprintf(&b, "%s %s\n\n", level, c.CommandPath())
		if c.Short != "" {
			b.WriteString(strings.TrimSpace(c.Short) + ".\n\n")
		}
		fmt.Fprintf(&b, "```text\n%s\n```\n\n", usageLine(c))
		if long := strings.TrimSpace(c.Long); long != "" {
			fmt.Fprintf(&b, "```text\n%s\n```\n\n", long)
		}
		if subs := visible(c); len(subs) > 0 {
			b.WriteString("Subcommands: ")
			for i, s := range subs {
				if i > 0 {
					b.WriteString(", ")
				}
				fmt.Fprintf(&b, "[`%s`](#%s)", s.Name(), anchor(s.CommandPath()))
			}
			b.WriteString(".\n\n")
		}
		if ex := strings.TrimRight(c.Example, "\n "); ex != "" {
			b.WriteString("Examples:\n\n```sh\n" + dedent(ex) + "\n```\n\n")
		}
		writeFlagTable(&b, "Flags", c.NonInheritedFlags())
	})
	_, err := io.WriteString(w, b.String())
	return err
}

// walk visits c and every visible command under it, depth first, in the
// order `--help` lists them (by name).
func walk(c *cobra.Command, fn func(*cobra.Command, int)) {
	var rec func(*cobra.Command, int)
	rec = func(c *cobra.Command, depth int) {
		fn(c, depth)
		for _, s := range visible(c) {
			rec(s, depth+1)
		}
	}
	rec(c, 0)
}

// visible is c's subcommands as help lists them, without cobra's own.
func visible(c *cobra.Command) []*cobra.Command {
	var out []*cobra.Command
	for _, s := range c.Commands() {
		if s.Hidden || s.Name() == "help" || s.Name() == "completion" {
			continue
		}
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name() < out[j].Name() })
	return out
}

// usageLine is the line under "Usage:" in --help.
func usageLine(c *cobra.Command) string {
	line := c.UseLine()
	if c.HasAvailableSubCommands() && !c.Runnable() {
		line = c.CommandPath() + " [command]"
	}
	return line
}

func writeFlagTable(b *strings.Builder, title string, fs *pflag.FlagSet) {
	var rows []string
	fs.VisitAll(func(f *pflag.Flag) {
		if f.Hidden || f.Name == "help" {
			return
		}
		name := "--" + f.Name
		if f.Shorthand != "" {
			name = "-" + f.Shorthand + ", " + name
		}
		if t := f.Value.Type(); t != "bool" {
			name += " " + t
		}
		rows = append(rows, fmt.Sprintf("| `%s` | %s | %s |", name, defaultCell(f), cell(f.Usage)))
	})
	if len(rows) == 0 {
		return
	}
	fmt.Fprintf(b, "%s:\n\n| Flag | Default | |\n|---|---|---|\n%s\n\n", title, strings.Join(rows, "\n"))
}

// defaultCell is a flag's default, or nothing when it is the zero value.
// A path under the generating user's home is written with ~, so the page
// does not depend on who ran `make docs-cli`.
func defaultCell(f *pflag.Flag) string {
	d := f.DefValue
	switch d {
	case "", "false", "0", "[]", "0s":
		return ""
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" && home != string(filepath.Separator) {
		d = strings.ReplaceAll(d, home, "~")
	}
	return "`" + strings.ReplaceAll(d, "`", "'") + "`"
}

// cell makes text safe inside a table cell: a pipe would end the cell, a
// newline the row, and an angle bracket would read as a tag.
func cell(s string) string {
	r := strings.NewReplacer("|", `\|`, "\n", " ", "<", "&lt;", ">", "&gt;")
	return r.Replace(strings.TrimSpace(s))
}

// anchor is the id a heading gets on GitHub and on the site: lowercase,
// spaces as dashes, punctuation other than - and _ dropped.
func anchor(heading string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(heading) {
		switch {
		case r == ' ':
			b.WriteRune('-')
		case r == '-' || r == '_' || r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			b.WriteRune(r)
		}
	}
	return b.String()
}

// dedent strips the indentation cobra examples carry for the terminal.
func dedent(s string) string {
	lines := strings.Split(s, "\n")
	min := -1
	for _, l := range lines {
		if strings.TrimSpace(l) == "" {
			continue
		}
		n := len(l) - len(strings.TrimLeft(l, " "))
		if min < 0 || n < min {
			min = n
		}
	}
	for i, l := range lines {
		if len(l) >= min && min > 0 {
			lines[i] = l[min:]
		}
	}
	return strings.Join(lines, "\n")
}

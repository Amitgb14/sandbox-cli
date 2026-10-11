package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// cliReference is the generated page, relative to this package.
var cliReference = filepath.Join("..", "..", "docs", "cli.md")

// TestCLIReferenceIsCurrent fails when docs/cli.md is not what the command
// tree generates: a flag, a command or a line of help changed without the
// reference. UPDATE_CLI_DOCS=1 (make docs-cli) rewrites it instead.
func TestCLIReferenceIsCurrent(t *testing.T) {
	var got bytes.Buffer
	if err := WriteReference(&got); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("UPDATE_CLI_DOCS") == "1" {
		if err := os.WriteFile(cliReference, got.Bytes(), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}
	want, err := os.ReadFile(cliReference)
	if err != nil {
		t.Fatalf("%v: run `make docs-cli` to generate it", err)
	}
	if bytes.Equal(got.Bytes(), want) {
		return
	}
	g, w := strings.Split(got.String(), "\n"), strings.Split(string(want), "\n")
	for i := 0; i < len(g) || i < len(w); i++ {
		var gl, wl string
		if i < len(g) {
			gl = g[i]
		}
		if i < len(w) {
			wl = w[i]
		}
		if gl != wl {
			t.Fatalf("docs/cli.md is stale (first difference at line %d):\n  file:      %q\n  generated: %q\nrun `make docs-cli` and commit the result", i+1, wl, gl)
		}
	}
}

// TestCLIReferenceCoversEveryCommand pins that the walk misses nothing a
// user can type: every visible command's path is a heading.
func TestCLIReferenceCoversEveryCommand(t *testing.T) {
	var out bytes.Buffer
	if err := WriteReference(&out); err != nil {
		t.Fatal(err)
	}
	n := 0
	walk(NewRootCmd(), func(c *cobra.Command, depth int) {
		if depth == 0 {
			return
		}
		n++
		if !strings.Contains(out.String(), "# "+c.CommandPath()+"\n") {
			t.Errorf("no heading for %s", c.CommandPath())
		}
	})
	if n < 30 {
		t.Fatalf("walked only %d commands", n)
	}
	if strings.Contains(out.String(), "# sandbox-cli help") || strings.Contains(out.String(), "# sandbox-cli completion") {
		t.Error("cobra's own help and completion commands are listed")
	}
}

// TestAnchorMatchesGitHub pins the anchor rule the site uses too
// (web/src/lib/slug.ts), so a link written against GitHub works on both.
func TestAnchorMatchesGitHub(t *testing.T) {
	for in, want := range map[string]string{
		"sandbox-cli ssh-key add": "sandbox-cli-ssh-key-add",
		"Jobs & secrets":          "jobs--secrets",
		"What `sandboxd` refuses": "what-sandboxd-refuses",
	} {
		if got := anchor(in); got != want {
			t.Errorf("anchor(%q) = %q, want %q", in, got, want)
		}
	}
}

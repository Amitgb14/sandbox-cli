package contract

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const repoRoot = "../.."

// The checked-in mirror must be what the generator produces from the Go types.
//
// This is the whole point of the generator. Before it, the mirror was
// hand-maintained and had drifted in the way hand-maintained copies do —
// silently, and in the fields that had just changed: `AgentInfo` was three
// fields where the Go struct had ten, and `SessionSummary` was missing entirely,
// so the one shape whose meaning had changed was the one a client could not read
// the contract for. Nothing catches that except a test that regenerates and
// compares.
func TestContractMirrorIsInSync(t *testing.T) {
	want, err := Generate(RootFile(repoRoot), Deps(repoRoot), Preamble)
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(repoRoot, "docs", "studio-api", "types.ts"))
	if err != nil {
		t.Fatalf("reading the mirror: %v", err)
	}
	if string(got) == want {
		return
	}

	// The failure names the first line that differs rather than printing two
	// files: a diff of 1600 lines is a wall, and what a reader needs is the
	// field that moved and the one command that fixes it.
	gotLines, wantLines := strings.Split(string(got), "\n"), strings.Split(want, "\n")
	for i := 0; i < len(gotLines) || i < len(wantLines); i++ {
		g, w := "", ""
		if i < len(gotLines) {
			g = gotLines[i]
		}
		if i < len(wantLines) {
			w = wantLines[i]
		}
		if g != w {
			t.Fatalf("docs/studio-api/types.ts is out of date with internal/studioapi/types.go\n"+
				"  first difference at line %d\n"+
				"    checked in: %q\n"+
				"    generated:  %q\n"+
				"  run `make contract` to regenerate it",
				i+1, g, w)
		}
	}
	t.Fatal("the mirror and the generated output differ in length but not in any line")
}

// A field the server omits when empty must be optional in the mirror, and a
// field it always sends must not be.
//
// Absent and zero are different answers throughout this API — an unset exit
// code is a run that has not finished, an empty `routedFrom` is a run that used
// the agent it was given — and a mirror that made everything optional would
// erase the distinction the Go side is careful to draw.
func TestOptionalityFollowsOmitempty(t *testing.T) {
	out, err := Generate(RootFile(repoRoot), Deps(repoRoot), Preamble)
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	for _, want := range []string{
		"  id: string;",               // Run.ID — always sent
		"  exitCode?: number;",        // set only once a run has exited
		"  handoffFrom?: string;",     // absent unless somebody handed the work over
		"  dockerAvailable: boolean;", // health always answers this
	} {
		if !strings.Contains(out, want) {
			t.Errorf("generated mirror is missing %q", want)
		}
	}
}

// A named string type with declared values becomes a union, because a client
// switching on it exhaustively should be checked by its own compiler rather
// than by hope.
func TestNamedStringTypesBecomeUnions(t *testing.T) {
	out, err := Generate(RootFile(repoRoot), Deps(repoRoot), Preamble)
	if err != nil {
		t.Fatalf("generating: %v", err)
	}
	for _, want := range []string{
		"export type RunState =",
		`  "running" |`,
		"export type LogEventType =",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("generated mirror is missing %q", want)
		}
	}
	if strings.Contains(out, "export type RunState = string;") {
		t.Error("RunState rendered as a bare string; its declared values are the contract")
	}
}

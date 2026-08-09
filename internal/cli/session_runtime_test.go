package cli

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Amitgb14/sandbox-cli/internal/fleet"
	"github.com/Amitgb14/sandbox-cli/internal/runtime"
	"github.com/Amitgb14/sandbox-cli/internal/sandbox"
)

// The RUNTIME column: what a run's boundary actually was, in the listing that
// answers "what is running".
//
// Its rule is conditional and the condition is the interesting part — see
// renderSessions. These two tests are the pair that pins it: absent when every
// session shares the host kernel, present *on every row* the moment one does
// not.

func runtimeRow(id, name, rt string, running bool) runtime.ContainerInfo {
	c := runtime.ContainerInfo{
		ID:        id,
		Name:      name,
		Runtime:   rt,
		State:     "exited",
		StartedAt: time.Now().Add(-time.Minute),
		Labels:    map[string]string{sandbox.LabelBranch: "feature-a"},
	}
	if running {
		c.State = "running"
	}
	return c
}

func TestListingHidesTheRuntimeColumnOnAnOrdinaryHost(t *testing.T) {
	rows := []runtime.ContainerInfo{
		runtimeRow("aaaaaaaaaaaa1111", "sandbox-app-a", "runc", true),
		runtimeRow("bbbbbbbbbbbb2222", "sandbox-app-b", "runc", false),
	}
	var out bytes.Buffer
	if err := renderSessions(&out, rows, true, time.Now()); err != nil {
		t.Fatalf("renderSessions: %v", err)
	}
	if strings.Contains(out.String(), "RUNTIME") {
		t.Errorf("a column that reads runc on every row costs the width the branch needs:\n%s", out.String())
	}
	if strings.Contains(out.String(), "runc") {
		t.Errorf("the default runtime is named in the listing:\n%s", out.String())
	}
}

func TestListingShowsTheRuntimeColumnForEveryRowOnceOneIsStronger(t *testing.T) {
	rows := []runtime.ContainerInfo{
		runtimeRow("aaaaaaaaaaaa1111", "sandbox-app-a", "kata-runtime", true),
		runtimeRow("bbbbbbbbbbbb2222", "sandbox-app-b", "runc", true),
		// Exited, because `list --all` is mostly finished sessions and the
		// alignment guard below is exactly what a header/row mismatch breaks.
		runtimeRow("cccccccccccc3333", "sandbox-app-c", "", false),
	}
	var out bytes.Buffer
	if err := renderSessions(&out, rows, true, time.Now()); err != nil {
		t.Fatalf("renderSessions: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "RUNTIME") {
		t.Fatalf("no RUNTIME column beside a session with its own kernel:\n%s", got)
	}
	// The weaker rows are the point: "which of these did not get the boundary I
	// thought I asked for" cannot be answered by marking only the strong ones.
	for _, want := range []string{"kata-runtime", "runc"} {
		if !strings.Contains(got, want) {
			t.Errorf("listing does not name the runtime %q:\n%s", want, got)
		}
	}
	// An engine that reported nothing reads as unknown, not as blank.
	lines := strings.Split(strings.TrimSpace(got), "\n")
	last := lines[len(lines)-1]
	if !strings.Contains(last, "-") {
		t.Errorf("a session with no reported runtime has an empty cell: %q", last)
	}
	// Every row keeps its columns: a header of 8 and a row of 7 is a table that
	// has quietly stopped lining up.
	//
	// Split on runs of two or more spaces, not on whitespace: the tabwriter has
	// already turned the tabs into padding (minimum two), and a cell's own value
	// can contain a single space — `sessionStatus` renders a finished session as
	// "exited (0)". Counting with strings.Fields would score that row one cell
	// too many and make the exited case untestable, which is the case this guard
	// most needs to cover.
	cellSplit := regexp.MustCompile(`\s{2,}`)
	head := len(cellSplit.Split(strings.TrimSpace(lines[0]), -1))
	for _, l := range lines[1:] {
		if n := len(cellSplit.Split(strings.TrimSpace(l), -1)); n != head {
			t.Errorf("row has %d cells, header has %d: %q", n, head, l)
		}
	}
}

// TestFleetStatusShowsTheRuntimeOnTheSameRuleAsList pins the parity CLAUDE.md
// asks for: a fleet agent is a session, and the two tables must not describe one
// container's boundary differently. Both read showRuntimeColumn.
func TestFleetStatusShowsTheRuntimeOnTheSameRuleAsList(t *testing.T) {
	ordinary := runtimeRow("aaaaaaaaaaaa1111", "sandbox-app-a", "runc", true)
	strong := runtimeRow("bbbbbbbbbbbb2222", "sandbox-app-b", "kata-runtime", true)

	var plain bytes.Buffer
	if err := renderFleetStatus(&plain, []fleet.Status{{Branch: "a", Container: &ordinary}}); err != nil {
		t.Fatalf("renderFleetStatus: %v", err)
	}
	if strings.Contains(plain.String(), "RUNTIME") {
		t.Errorf("fleet status names the default runtime on an ordinary host:\n%s", plain.String())
	}

	var mixed bytes.Buffer
	rows := []fleet.Status{
		{Branch: "a", Container: &strong},
		{Branch: "b", Container: &ordinary},
		// A branch whose container has been reaped: unknown, not "the default".
		{Branch: "c"},
	}
	if err := renderFleetStatus(&mixed, rows); err != nil {
		t.Fatalf("renderFleetStatus: %v", err)
	}
	got := mixed.String()
	for _, want := range []string{"RUNTIME", "kata-runtime", "runc"} {
		if !strings.Contains(got, want) {
			t.Errorf("fleet status does not mention %q:\n%s", want, got)
		}
	}
	cellSplit := regexp.MustCompile(`\s{2,}`)
	lines := strings.Split(strings.TrimSpace(got), "\n")
	head := len(cellSplit.Split(strings.TrimSpace(lines[0]), -1))
	for _, l := range lines[1:] {
		if n := len(cellSplit.Split(strings.TrimSpace(l), -1)); n != head {
			t.Errorf("row has %d cells, header has %d: %q", n, head, l)
		}
	}
}

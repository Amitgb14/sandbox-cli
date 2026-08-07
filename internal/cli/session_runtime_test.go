package cli

import (
	"bytes"
	"strings"
	"testing"
	"time"

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
		runtimeRow("cccccccccccc3333", "sandbox-app-c", "", true),
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
	head := len(strings.Fields(lines[0]))
	for _, l := range lines[1:] {
		if n := len(strings.Fields(l)); n != head {
			t.Errorf("row has %d cells, header has %d: %q", n, head, l)
		}
	}
}

func TestStrongerRuntimeNamesOnlyWhatItKnows(t *testing.T) {
	for _, name := range []string{"runsc", "runsc-kvm", "kata", "kata-runtime", "kata-qemu", "crun-vm"} {
		if !runtime.StrongerRuntime(name) {
			t.Errorf("StrongerRuntime(%q) = false, want true", name)
		}
	}
	// "" is the host default (runc), and an unknown name is reported by its name
	// rather than promoted: nothing here may claim a boundary a run did not get.
	for _, name := range []string{"", "runc", "crun", "youki", "docker-runc", "RUNSC"} {
		if runtime.StrongerRuntime(name) {
			t.Errorf("StrongerRuntime(%q) = true, want false", name)
		}
	}
}

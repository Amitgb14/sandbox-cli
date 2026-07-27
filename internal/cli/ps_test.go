package cli

import "testing"

// TestParsePsRows covers the shape of docker's output, and one bug in
// particular: the trailing fields are the optional labels, so a container with
// no repo/branch/agent — every run started outside a git repository — ends its
// line in empty fields. Trimming the whole output with TrimSpace ate them, the
// row came back with two fields instead of five, and the container was silently
// dropped from the listing. A container nobody can list is one nobody can stop.
func TestParsePsRows(t *testing.T) {
	out := "sandbox-abc\tUp 23 seconds\t\t\t\n" +
		"sandbox-def\tExited (0) 2 minutes ago\trepo-1234\tfeature/x\tclaude\n"

	rows := parsePsRows(out)
	if len(rows) != 2 {
		t.Fatalf("parsed %d rows, want 2: %+v", len(rows), rows)
	}
	if rows[0].Name != "sandbox-abc" || rows[0].Status != "Up 23 seconds" {
		t.Errorf("row 0 = %+v", rows[0])
	}
	if rows[0].Repo != "" || rows[0].Branch != "" || rows[0].Agent != "" {
		t.Errorf("row 0 should have empty labels, got %+v", rows[0])
	}
	if rows[1].Agent != "claude" || rows[1].Branch != "feature/x" || rows[1].Repo != "repo-1234" {
		t.Errorf("row 1 = %+v", rows[1])
	}

	if len(parsePsRows("")) != 0 {
		t.Error("empty output should yield no rows")
	}
	if len(parsePsRows("\n\n")) != 0 {
		t.Error("blank lines should yield no rows")
	}
	// A short row is padded, not dropped: a missing label is a blank column.
	short := parsePsRows("sandbox-xyz\tUp 1 second\n")
	if len(short) != 1 || short[0].Name != "sandbox-xyz" {
		t.Errorf("a short row must still list: %+v", short)
	}
}

func TestDashRendersEmptyColumns(t *testing.T) {
	if dash("") != "-" || dash("   ") != "-" {
		t.Error("an empty label should render as a dash, not a gap")
	}
	if dash("claude") != "claude" {
		t.Error("a real value must pass through")
	}
}

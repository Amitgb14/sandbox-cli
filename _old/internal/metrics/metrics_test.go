package metrics

import (
	"bufio"
	"io"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"
)

func TestParseBytes(t *testing.T) {
	cases := []struct {
		in   string
		want float64
		ok   bool
	}{
		{"512MiB", 512 * (1 << 20), true},
		{"7.633GiB", 7.633 * (1 << 30), true},
		{"0B", 0, true},
		{"1.5kB", 1500, true},
		{"garbage", 0, false},
		{"MiB", 0, false},
	}
	for _, c := range cases {
		got, ok := parseBytes(c.in)
		if ok != c.ok {
			t.Errorf("parseBytes(%q) ok=%v, want %v", c.in, ok, c.ok)
			continue
		}
		if ok && !approx(got, c.want) {
			t.Errorf("parseBytes(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

func TestParseMemUsage(t *testing.T) {
	display, frac := parseMemUsage("512MiB / 1024MiB")
	if display != "512MiB/1024MiB" {
		t.Errorf("display = %q", display)
	}
	if !approx(frac, 0.5) {
		t.Errorf("frac = %v, want 0.5", frac)
	}
	// Unparseable limit -> fraction 0, display still shows used.
	d2, f2 := parseMemUsage("512MiB")
	if d2 != "512MiB" || f2 != 0 {
		t.Errorf("got (%q, %v), want (512MiB, 0)", d2, f2)
	}
}

func TestBar(t *testing.T) {
	if got := bar(0, 8); got != "▕"+strings.Repeat("░", 8)+"▏" {
		t.Errorf("empty bar = %q", got)
	}
	if got := bar(1, 8); got != "▕"+strings.Repeat("▓", 8)+"▏" {
		t.Errorf("full bar = %q", got)
	}
	// Out-of-range clamps.
	if got := bar(2, 4); got != "▕"+strings.Repeat("▓", 4)+"▏" {
		t.Errorf("clamped bar = %q", got)
	}
}

func TestFormatDuration(t *testing.T) {
	cases := map[time.Duration]string{
		5 * time.Second:                       "5s",
		65 * time.Second:                      "1m05s",
		(12*60 + 4) * time.Second:             "12m04s",
		90*time.Second + 400*time.Millisecond: "1m30s",
	}
	for d, want := range cases {
		if got := formatDuration(d); got != want {
			t.Errorf("formatDuration(%v) = %q, want %q", d, got, want)
		}
	}
}

// TestFooterForwardsOutputIntact verifies the sticky footer passes container
// output through byte-for-byte (only adding erase/redraw around it), so program
// output is never corrupted.
func TestFooterForwardsOutputIntact(t *testing.T) {
	pr, pw, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	f := NewTermFooter(pw)
	f.SetStatus("STATUS")
	f.Print(pw, []byte("hello\n"))
	f.Print(pw, []byte("world\n"))
	f.Finish()
	pw.Close()

	data, _ := io.ReadAll(bufio.NewReader(pr))
	out := string(data)
	// The payload lines must be present in order.
	if i, j := strings.Index(out, "hello"), strings.Index(out, "world"); i < 0 || j < 0 || i > j {
		t.Errorf("payload not forwarded intact: %q", out)
	}
	// Status text was drawn at least once.
	if !strings.Contains(out, "STATUS") {
		t.Errorf("status not drawn: %q", out)
	}
}

// layout must right-align the branch without ever wrapping the footer: it is
// erased with a single-line escape, so a second line would be left behind.
func TestLayout(t *testing.T) {
	left, right := " left ", "⎇ main "
	got := layout(left, right, 40)
	if utf8.RuneCountInString(got) != 39 { // one short of the right edge
		t.Errorf("width = %d, want 39: %q", utf8.RuneCountInString(got), got)
	}
	if !strings.HasPrefix(got, left) || !strings.HasSuffix(got, right) {
		t.Errorf("segments not at the edges: %q", got)
	}

	// Too narrow for both -> the branch is dropped, never wrapped.
	if got := layout(left, right, 10); got != left {
		t.Errorf("narrow layout = %q, want %q", got, left)
	}
	// Unknown width (not a terminal) -> no right edge to align to.
	if got := layout(left, right, 0); got != left+"· "+right {
		t.Errorf("unknown-width layout = %q", got)
	}
	// No branch -> unchanged, whatever the width.
	if got := layout(left, "", 40); got != left {
		t.Errorf("empty right = %q, want %q", got, left)
	}
}

// The gauge falls back to a bare line when there is no branch, and shows one
// when there is.
func TestMeterFormatBranch(t *testing.T) {
	m := &Meter{start: time.Now(), branch: "feature/x"}
	if got := m.format(); !strings.Contains(got, "⎇ feature/x") {
		t.Errorf("branch missing from gauge: %q", got)
	}
	m.branch = ""
	if got := m.format(); strings.Contains(got, "⎇") {
		t.Errorf("unexpected branch label: %q", got)
	}
}

func TestHumanBytes(t *testing.T) {
	cases := map[float64]string{
		512:             "512B",
		1536:            "1.5KiB",
		512 * (1 << 20): "512.0MiB",
		2.5 * (1 << 30): "2.5GiB",
	}
	for in, want := range cases {
		if got := humanBytes(in); got != want {
			t.Errorf("humanBytes(%v) = %q, want %q", in, got, want)
		}
	}
}

func TestMeterSummary(t *testing.T) {
	// No sample captured -> empty summary (avoids noise on trivial short runs).
	m := &Meter{start: time.Now()}
	if s := m.Summary(); s != "" {
		t.Errorf("expected empty summary before any sample, got %q", s)
	}
	// After a sample, the summary reports peak memory and CPU.
	m.sampled = true
	m.peakMemText = "512MiB"
	m.peakCPU = 138
	got := m.Summary()
	if !strings.Contains(got, "peak mem 512MiB") || !strings.Contains(got, "cpu peak 138%") {
		t.Errorf("unexpected summary: %q", got)
	}
}

func approx(a, b float64) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d < 1e-6*(1+abs(b))
}

func abs(x float64) float64 {
	if x < 0 {
		return -x
	}
	return x
}

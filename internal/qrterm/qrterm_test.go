package qrterm

import (
	"strings"
	"testing"

	"rsc.io/qr"
)

const link = "sandboxstudio://pair#v=1&url=http%3A%2F%2Fmac.tailnet.ts.net%3A8787&token=abc123&name=this+mac"

// unrender reads the half blocks back into modules, true for light. A drawing
// that loses or shifts one module still looks like a QR code to a person and
// does not scan, so the only useful test is that every module survives.
func unrender(t *testing.T, s string) [][]bool {
	t.Helper()
	var grid [][]bool
	for _, line := range strings.Split(strings.TrimSuffix(s, "\n"), "\n") {
		var top, bottom []bool
		for _, r := range line {
			switch r {
			case '█':
				top, bottom = append(top, true), append(bottom, true)
			case '▀':
				top, bottom = append(top, true), append(bottom, false)
			case '▄':
				top, bottom = append(top, false), append(bottom, true)
			case ' ':
				top, bottom = append(top, false), append(bottom, false)
			default:
				t.Fatalf("unexpected rune %q in rendered code", r)
			}
		}
		grid = append(grid, top, bottom)
	}
	return grid
}

func TestRenderPreservesEveryModule(t *testing.T) {
	code, err := qr.Encode(link, qr.L)
	if err != nil {
		t.Fatal(err)
	}
	out, err := Render(link, false)
	if err != nil {
		t.Fatal(err)
	}
	grid := unrender(t, out)
	n := code.Size + 2*quietZone
	if len(grid[0]) != n {
		t.Fatalf("width = %d, want %d (code %d + quiet zone %d each side)", len(grid[0]), n, code.Size, quietZone)
	}
	if want := (n + 1) / 2; len(grid)/2 != want {
		t.Fatalf("lines = %d, want %d: two modules per line", len(grid)/2, want)
	}
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			cx, cy := x-quietZone, y-quietZone
			wantLight := cx < 0 || cy < 0 || cx >= code.Size || cy >= code.Size || !code.Black(cx, cy)
			if grid[y][x] != wantLight {
				t.Fatalf("module (%d,%d): light = %v, want %v", x, y, grid[y][x], wantLight)
			}
		}
	}
}

func TestRenderColourPaintsEveryLineAndResets(t *testing.T) {
	out, err := Render(link, true)
	if err != nil {
		t.Fatal(err)
	}
	for i, line := range strings.Split(strings.TrimSuffix(out, "\n"), "\n") {
		if !strings.HasPrefix(line, "\x1b[97;40m") || !strings.HasSuffix(line, "\x1b[0m") {
			t.Fatalf("line %d is not painted and reset: %q", i, line)
		}
	}
	plain, _ := Render(link, false)
	if strings.Contains(plain, "\x1b") {
		t.Fatal("uncoloured output carries an escape code; it is what gets written to logs")
	}
}

func TestRenderRefusesWhatCannotBeEncoded(t *testing.T) {
	if _, err := Render(strings.Repeat("x", 4000), false); err == nil {
		t.Fatal("want an error for text past the largest QR version")
	}
}

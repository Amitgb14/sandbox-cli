// Package qrterm draws a QR code as terminal text, for the pairing link
// sandbox-studio-api prints.
//
// It is the only importer of rsc.io/qr, and deliberately so. The module
// otherwise depends on the standard library, cobra and yaml.v3, and the rule
// behind that (internal/s3 is hand-rolled for it) is to refuse transitive code
// with a release cadence to track. rsc.io/qr has no dependencies of its own and
// its last release was in 2018 — there is none to track. A hand-rolled
// Reed–Solomon encoder would be more
// code in this repository to get subtly wrong, where the failure is a code that
// looks right and does not scan. Kept to one package so replacing it is a
// one-file change.
package qrterm

import (
	"fmt"
	"strings"

	"rsc.io/qr"
)

// quietZone is the light border a scanner needs to find the code, in modules.
// The specification asks for four; fewer is what makes a code drawn against a
// terminal's own background fail to scan with nothing visibly wrong with it.
const quietZone = 4

// Render draws text as a QR code with half-block characters: one character per
// module across, two modules per character down. A terminal cell is roughly
// twice as tall as it is wide, so this is what makes the modules square — a
// full block per module draws a code twice as tall as wide, which scanners read
// poorly, and at twice the height.
//
// The glyphs draw the *light* modules, so on a dark terminal the code comes out
// dark-on-light the way a scanner expects. On a light terminal that polarity
// inverts, which is what colour is for: with colour set, every line is painted
// bright-white-on-black, so the code is correct whatever the theme. It is
// optional because the escape codes are noise wherever the output is a log file
// rather than a terminal.
//
// Error correction is level L, the lowest. Correction buys resilience against a
// damaged or partly covered code, which a screen is not, and every level up
// adds modules — which means smaller modules on the same screen, and a harder
// scan from across a desk.
func Render(text string, colour bool) (string, error) {
	code, err := qr.Encode(text, qr.L)
	if err != nil {
		return "", fmt.Errorf("drawing a QR code: %w", err)
	}
	return render(code.Size, code.Black, colour), nil
}

// render is Render over any module grid, so the drawing can be tested against a
// grid whose every module is known.
func render(size int, black func(x, y int) bool, colour bool) string {
	n := size + 2*quietZone
	light := func(x, y int) bool {
		x, y = x-quietZone, y-quietZone
		if x < 0 || y < 0 || x >= size || y >= size {
			return true // the quiet zone, including the half row past an odd edge
		}
		return !black(x, y)
	}
	var b strings.Builder
	for y := 0; y < n; y += 2 {
		if colour {
			b.WriteString("\x1b[97;40m")
		}
		for x := 0; x < n; x++ {
			top, bottom := light(x, y), light(x, y+1)
			switch {
			case top && bottom:
				b.WriteString("█")
			case top:
				b.WriteString("▀")
			case bottom:
				b.WriteString("▄")
			default:
				b.WriteByte(' ')
			}
		}
		if colour {
			// Reset before the newline, so a terminal that carries attributes
			// across lines does not paint the text after the code.
			b.WriteString("\x1b[0m")
		}
		b.WriteByte('\n')
	}
	return b.String()
}

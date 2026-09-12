package ui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/zebadrabbit/sitrep/internal/theme"
)

// Mirrored braille graph: rx grows upward from a center line, tx grows
// downward, so the two directions read at a glance without a legend
// (HANDOFF §5 #6). Each braille cell is 2 columns × 4 dots.

// braille dot bit layout (unicode 0x2800 + bits):
//
//	col0: 0x01 0x02 0x04 0x40   col1: 0x08 0x10 0x20 0x80   (top → bottom)
var dotsL = [4]rune{0x40, 0x04, 0x02, 0x01} // bottom → top
var dotsR = [4]rune{0x80, 0x20, 0x10, 0x08}

// Mirror renders rx (up) and tx (down) over w cells and rows lines per
// half. Values are scaled to the larger of the two series' maxima so the
// halves share a scale. Samples are drawn from the left and the graph
// scrolls once it is full, so a short history doesn't sit at the far right.
func Mirror(rx, tx []float64, w, rows int) string {
	if w <= 0 || rows <= 0 {
		return ""
	}
	n := w * 2 // two samples per cell
	rx, tx = tail(rx, n), tail(tx, n)
	if len(tx) > len(rx) {
		rx = append(rx, make([]float64, len(tx)-len(rx))...)
	}
	peak := 0.0
	for _, v := range append(append([]float64{}, rx...), tx...) {
		peak = max(peak, v)
	}
	if peak == 0 {
		peak = 1
	}
	s := theme.Current()
	up := half(rx, w, rows, peak, false)
	down := half(tx, w, rows, peak, true)
	var b strings.Builder
	for i := len(up) - 1; i >= 0; i-- {
		b.WriteString(paint(up[i], loud(rx, w, peak), s.OK, s.Dim) + "\n")
	}
	for i := range down {
		b.WriteString(paint(down[i], loud(tx, w, peak), s.Accent, s.Dim))
		if i < len(down)-1 {
			b.WriteString("\n")
		}
	}
	return b.String()
}

// quiet is the share of peak under which a column is drawn dim: throughput
// is not an error state, so the ramp is cool → bright, not green → red.
const quiet = 0.25

// loud marks the columns whose taller sample reaches quiet × peak.
func loud(v []float64, w int, peak float64) []bool {
	out := make([]bool, w)
	for i := range out {
		for side := 0; side < 2; side++ {
			if idx := i*2 + side; idx < len(v) && v[idx] >= quiet*peak {
				out[i] = true
			}
		}
	}
	return out
}

// paint styles one braille row per column, in runs so the ANSI stays short.
func paint(line string, bright []bool, hi, lo lipgloss.Style) string {
	var b, run strings.Builder
	cur := false
	for i, r := range []rune(line) {
		if bright[i] != cur && run.Len() > 0 {
			b.WriteString(pick(cur, hi, lo).Render(run.String()))
			run.Reset()
		}
		cur = bright[i]
		run.WriteRune(r)
	}
	b.WriteString(pick(cur, hi, lo).Render(run.String()))
	return b.String()
}

func pick(bright bool, hi, lo lipgloss.Style) lipgloss.Style {
	if bright {
		return hi
	}
	return lo
}

func tail(v []float64, n int) []float64 {
	if len(v) > n {
		return v[len(v)-n:]
	}
	return v
}

// half builds rows lines of braille for one direction; row 0 is nearest the
// center line. flip draws downward.
func half(v []float64, w, rows int, peak float64, flip bool) []string {
	total := rows * 4
	lines := make([][]rune, rows)
	for r := range lines {
		lines[r] = make([]rune, w)
		for c := range lines[r] {
			lines[r][c] = 0x2800
		}
	}
	for i := 0; i < w; i++ {
		for side, dots := range [][4]rune{dotsL, dotsR} {
			idx := i*2 + side
			if idx >= len(v) {
				continue
			}
			h := int(v[idx] / peak * float64(total))
			if v[idx] > 0 && h == 0 {
				h = 1
			}
			for d := 0; d < h && d < total; d++ {
				row, bit := d/4, d%4
				if flip {
					bit = 3 - bit
				}
				lines[row][i] |= dots[bit]
			}
		}
	}
	out := make([]string, rows)
	for r := range lines {
		out[r] = string(lines[r])
	}
	return out
}

package ui

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/zebadrabbit/sitrep/internal/theme"
)

var sparkRunes = []rune("▁▂▃▄▅▆▇█")

// Sparkline renders the last w values scaled to 0..max (max ≥ 1).
func Sparkline(vals []float64, w int, max float64) string {
	if w <= 0 {
		return ""
	}
	if len(vals) > w {
		vals = vals[len(vals)-w:]
	}
	if max <= 0 {
		max = 1
	}
	var b strings.Builder
	for i := 0; i < w-len(vals); i++ {
		b.WriteRune(' ')
	}
	for _, v := range vals {
		i := int(v / max * float64(len(sparkRunes)-1))
		if i < 0 {
			i = 0
		}
		if i >= len(sparkRunes) {
			i = len(sparkRunes) - 1
		}
		b.WriteRune(sparkRunes[i])
	}
	return b.String()
}

// Bar renders a usage bar colored by threshold: ! at ≥85, !! at ≥95.
func Bar(pct float64, w int) string {
	if w < 3 {
		return ""
	}
	s := theme.Current()
	fill := int(pct/100*float64(w) + 0.5)
	if fill > w {
		fill = w
	}
	style := s.OK
	switch {
	case pct >= 95:
		style = s.Crit
	case pct >= 85:
		style = s.Warn
	}
	return style.Render(strings.Repeat("█", fill)) + s.Dim.Render(strings.Repeat("░", w-fill))
}

// Threshold returns the warning glyph for a percentage, or "".
func Threshold(pct float64) string {
	g := theme.Current().Glyph
	switch {
	case pct >= 95:
		return theme.Current().Crit.Render(g.Crit)
	case pct >= 85:
		return theme.Current().Warn.Render(g.Warn)
	}
	return ""
}

// Age formats a duration the way the Ports AGE column wants: 12d 4h, 6h 12m, 41m, 12s.
func Age(d time.Duration) string {
	switch {
	case d < 0:
		return "?"
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh %dm", int(d.Hours()), int(d.Minutes())%60)
	}
	days := int(d.Hours()) / 24
	return fmt.Sprintf("%dd %dh", days, int(d.Hours())%24)
}

// Bytes formats a byte count: 1.2G, 512M, 3.4K.
func Bytes(n uint64) string {
	const k = 1024
	units := "BKMGTP"
	f := float64(n)
	i := 0
	for f >= k && i < len(units)-1 {
		f /= k
		i++
	}
	if i == 0 {
		return fmt.Sprintf("%d%c", n, units[i])
	}
	if f >= 100 {
		return fmt.Sprintf("%.0f%c", f, units[i])
	}
	return fmt.Sprintf("%.1f%c", f, units[i])
}

// Box draws a titled card. Title is rendered in accent inside the top border.
func Box(title, body string, w int) string {
	s := theme.Current()
	inner := w - 2
	if inner < 4 {
		return ""
	}
	top := "╭ " + s.Accent.Render(title) + " " + strings.Repeat("─", max(0, inner-lipgloss.Width(title)-2)) + "╮"
	var b strings.Builder
	b.WriteString(s.Dim.Render(top) + "\n")
	for _, line := range strings.Split(body, "\n") {
		pad := inner - lipgloss.Width(line)
		if pad < 0 {
			line = lipgloss.NewStyle().MaxWidth(inner).Render(line)
			pad = 0
		}
		b.WriteString(s.Dim.Render("│") + line + strings.Repeat(" ", pad) + s.Dim.Render("│") + "\n")
	}
	b.WriteString(s.Dim.Render("╰" + strings.Repeat("─", inner) + "╯"))
	return b.String()
}

// Columns lays blocks into n columns of equal width, filling column by column.
func Columns(blocks []string, n, w int) string {
	if n < 1 {
		n = 1
	}
	if len(blocks) == 0 {
		return ""
	}
	colW := w / n
	per := (len(blocks) + n - 1) / n
	cols := make([]string, 0, n)
	for c := 0; c < n; c++ {
		lo, hi := c*per, min((c+1)*per, len(blocks))
		if lo >= hi {
			break
		}
		cols = append(cols, lipgloss.NewStyle().Width(colW).Render(strings.Join(blocks[lo:hi], "\n")))
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, cols...)
}

// Table renders rows with left-aligned columns of computed width, header dim.
func Table(header []string, rows [][]string, w int) string {
	widths := make([]int, len(header))
	for i, h := range header {
		widths[i] = lipgloss.Width(h)
	}
	for _, r := range rows {
		for i := range header {
			if i < len(r) && lipgloss.Width(r[i]) > widths[i] {
				widths[i] = lipgloss.Width(r[i])
			}
		}
	}
	line := func(cells []string) string {
		var b strings.Builder
		for i := range header {
			c := ""
			if i < len(cells) {
				c = cells[i]
			}
			b.WriteString(c + strings.Repeat(" ", max(0, widths[i]-lipgloss.Width(c))))
			if i < len(header)-1 {
				b.WriteString("  ")
			}
		}
		return lipgloss.NewStyle().MaxWidth(w).Render(b.String())
	}
	out := []string{theme.Current().Dim.Render(line(header))}
	for _, r := range rows {
		out = append(out, line(r))
	}
	return strings.Join(out, "\n")
}

// Package ui holds shared widgets. Colors come from theme, never from here.
package ui

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/zebadrabbit/sitrep/internal/theme"
)

// Hotkey renders "k" + rest with theme.Hotkey on the key only ("q" + "uit" → quit).
func Hotkey(key, rest string) string {
	return theme.Current().Hotkey.Render(key) + rest
}

// Check renders a status glyph in its color.
func Check(glyph string) string {
	s := theme.Current()
	switch glyph {
	case s.Glyph.OK:
		return s.OK.Render(glyph)
	case s.Glyph.Fail, s.Glyph.Crit:
		return s.Crit.Render(glyph)
	case s.Glyph.Degraded, s.Glyph.Warn:
		return s.Warn.Render(glyph)
	}
	return glyph
}

// Cursor marks line as the selected row: accent bar on the left, theme.Sel
// background across the full width w. The one background sitrep paints.
func Cursor(line string, w int) string {
	s := theme.Current()
	line = s.Accent.Render("▎") + line
	if pad := w - lipgloss.Width(line); pad > 0 {
		line += strings.Repeat(" ", pad)
	}
	out := s.Sel.Render(line)
	// Nested styles end in a reset that also drops our background; re-arm it after each one.
	if bg, _, ok := strings.Cut(s.Sel.Render(" "), " "); ok && bg != "" {
		const reset = "\x1b[m"
		out = strings.ReplaceAll(strings.TrimSuffix(out, reset), reset, reset+bg) + reset
	}
	return out
}

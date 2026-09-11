// Package ui holds shared widgets. Colors come from theme, never from here.
package ui

import "github.com/zebadrabbit/sitrep/internal/theme"

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

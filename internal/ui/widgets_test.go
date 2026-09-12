package ui

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/zebadrabbit/sitrep/internal/theme"
)

func TestAge(t *testing.T) {
	cases := map[time.Duration]string{
		12 * time.Second: "12s", 41 * time.Minute: "41m",
		6*time.Hour + 12*time.Minute: "6h 12m", 12*24*time.Hour + 4*time.Hour: "12d 4h",
	}
	for d, want := range cases {
		if got := Age(d); got != want {
			t.Errorf("Age(%v)=%q want %q", d, got, want)
		}
	}
}

func TestSparklineWidth(t *testing.T) {
	s := Sparkline([]float64{0, 50, 100}, 5, 100)
	if r := []rune(stripAnsi(s)); len(r) != 5 || r[4] != '█' || r[2] != '▁' {
		t.Errorf("got %q", s)
	}
	th := theme.Current()
	if !strings.Contains(s, th.Crit.Render("█")) || !strings.Contains(s, th.OK.Render("▁▄")) {
		t.Errorf("cells not colored by height: %q", s)
	}
}

func TestBytes(t *testing.T) {
	if got := Bytes(1536 * 1024 * 1024); got != "1.5G" {
		t.Errorf("got %q", got)
	}
}

func TestCursorBackgroundSurvivesInnerReset(t *testing.T) {
	inner := "a " + theme.Current().Port.Render("22") + " b"
	got := Cursor(inner, 12)
	if w := lipgloss.Width(got); w != 12 {
		t.Fatalf("width %d, want 12: %q", w, got)
	}
	bg, _, _ := strings.Cut(theme.Current().Sel.Render(" "), " ")
	if bg == "" {
		t.Skip("theme has no selection color")
	}
	if i := strings.LastIndex(got, " b"); !strings.Contains(got[:i], "\x1b[m"+bg) {
		t.Fatalf("background not re-armed after inner reset: %q", got)
	}
	if !strings.HasSuffix(got, "\x1b[m") || strings.HasSuffix(got, bg+"\x1b[m") {
		t.Fatalf("bad tail: %q", got)
	}
}

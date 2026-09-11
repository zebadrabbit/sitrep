package ui

import (
	"strings"
	"testing"
)

func TestMirrorShape(t *testing.T) {
	rx := []float64{0, 1, 2, 3, 4, 5, 6, 7, 8}
	tx := []float64{8, 0, 0, 0, 0, 0, 0, 0, 0}
	out := Mirror(rx, tx, 10, 2)
	lines := strings.Split(out, "\n")
	if len(lines) != 4 {
		t.Fatalf("want 4 lines (2 up + 2 down), got %d", len(lines))
	}
	for _, l := range lines {
		if n := len([]rune(stripAnsi(l))); n != 10 {
			t.Errorf("line width %d, want 10: %q", n, l)
		}
	}
	if Mirror(nil, nil, 0, 2) != "" {
		t.Error("zero width should be empty")
	}
}

func stripAnsi(s string) string {
	var b strings.Builder
	in := false
	for _, r := range s {
		switch {
		case r == '\x1b':
			in = true
		case in && r == 'm':
			in = false
		case !in:
			b.WriteRune(r)
		}
	}
	return b.String()
}

package ui

import (
	"testing"
	"time"
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
	if r := []rune(s); len(r) != 5 || r[4] != '█' || r[2] != '▁' {
		t.Errorf("got %q", s)
	}
}

func TestBytes(t *testing.T) {
	if got := Bytes(1536 * 1024 * 1024); got != "1.5G" {
		t.Errorf("got %q", got)
	}
}

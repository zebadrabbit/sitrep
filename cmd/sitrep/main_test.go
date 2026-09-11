package main

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestKeyMsgs(t *testing.T) {
	got := keyMsgs("j,enter,space,,G")
	want := []tea.KeyPressMsg{{Code: 'j', Text: "j"}, {Code: tea.KeyEnter}, {Code: tea.KeySpace}, {Code: 'G', Text: "G"}}
	if len(got) != len(want) {
		t.Fatalf("got %d keys, want %d: %v", len(got), len(want), got)
	}
	for i := range want {
		if got[i].Code != want[i].Code || got[i].Text != want[i].Text || got[i].String() != want[i].String() {
			t.Errorf("key %d = %+v (%q), want %+v", i, got[i], got[i].String(), want[i])
		}
	}
}

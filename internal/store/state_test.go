package store

import (
	"testing"
	"time"
)

func TestSaveLoadStateText(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	want := State{Kind: "text", TS: time.Date(2026, 5, 28, 12, 0, 0, 0, time.UTC)}
	if err := SaveState(want, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	got, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if got.Kind != want.Kind || !got.TS.Equal(want.TS) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	text, err := LoadText()
	if err != nil {
		t.Fatal(err)
	}
	if string(text) != "hello" {
		t.Fatalf("text = %q, want hello", text)
	}
}

func TestLoadStateMissingIsEmpty(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	s, err := LoadState()
	if err != nil {
		t.Fatal(err)
	}
	if s.Kind != "" {
		t.Fatalf("expected zero state, got %+v", s)
	}
}

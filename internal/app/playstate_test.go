package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlayState_RecordGetRoundTrip(t *testing.T) {
	p := filepath.Join(t.TempDir(), "playstate.json")
	ps := loadPlayState(p) // file absent → empty, no error
	if got := ps.Get("fake/g"); !got.IsZero() {
		t.Fatalf("absent game should be zero time, got %v", got)
	}
	ps.Record("fake/g")
	if got := ps.Get("fake/g"); got.IsZero() {
		t.Fatalf("Record then Get should be non-zero")
	}
	ps2 := loadPlayState(p)
	if got := ps2.Get("fake/g"); got.IsZero() {
		t.Fatalf("value did not persist across reload")
	}
}

func TestPlayState_CorruptFileTolerated(t *testing.T) {
	p := filepath.Join(t.TempDir(), "playstate.json")
	if err := os.WriteFile(p, []byte("}{ not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	ps := loadPlayState(p)
	if got := ps.Get("any/game"); !got.IsZero() {
		t.Fatalf("corrupt file should yield empty store")
	}
	ps.Record("any/game")
	if ps.Get("any/game").IsZero() {
		t.Fatalf("Record after corrupt-load failed")
	}
}

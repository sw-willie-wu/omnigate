package app

import (
	"path/filepath"
	"testing"

	"omnigate/internal/store"
)

func TestPlayState_RecordGetRoundTrip(t *testing.T) {
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "omnigate.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	ps := loadPlayState(st)
	if got := ps.Get("fake/g"); !got.IsZero() {
		t.Fatalf("absent game should be zero, got %v", got)
	}
	ps.Record("fake/g")
	if got := ps.Get("fake/g"); got.IsZero() {
		t.Fatalf("Record then Get should be non-zero")
	}
	ps2 := loadPlayState(st) // same store = reload
	if got := ps2.Get("fake/g"); got.IsZero() {
		t.Fatalf("value did not persist across reload")
	}
}

func TestPlayState_NilStoreTolerated(t *testing.T) { // was TestPlayState_CorruptFileTolerated
	ps := loadPlayState(nil) // DB-open failure → nil store; in-memory only, no panic
	if got := ps.Get("any/game"); !got.IsZero() {
		t.Fatalf("nil store should yield empty")
	}
	ps.Record("any/game")
	if ps.Get("any/game").IsZero() {
		t.Fatalf("Record after nil-load should still update in-memory")
	}
}

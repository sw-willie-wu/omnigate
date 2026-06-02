package hypergryph

import (
	"path/filepath"
	"testing"
)

func TestFolderNames_NonEmptyKnownGame(t *testing.T) {
	fn := FolderNames()
	if len(fn) == 0 {
		t.Fatal("FolderNames() is empty")
	}
	want := filepath.Join("games", "EndField Game")
	if got := fn["hypergryph/endfield"]; got != want {
		t.Errorf("endfield folder = %q, want %q", got, want)
	}
}

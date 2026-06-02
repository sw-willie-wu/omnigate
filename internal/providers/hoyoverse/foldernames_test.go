package hoyoverse

import "testing"

func TestFolderNames_NonEmptyKnownGame(t *testing.T) {
	fn := FolderNames()
	if len(fn) == 0 {
		t.Fatal("FolderNames() is empty")
	}
	if got := fn["hoyoverse/genshin"]; got != "Genshin Impact game" {
		t.Errorf("genshin folder = %q, want %q", got, "Genshin Impact game")
	}
}

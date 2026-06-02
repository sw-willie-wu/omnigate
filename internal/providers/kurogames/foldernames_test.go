package kurogames

import "testing"

func TestFolderNames_NonEmptyKnownGame(t *testing.T) {
	fn := FolderNames()
	if len(fn) == 0 {
		t.Fatal("FolderNames() is empty")
	}
	if got := fn["kurogames/wutheringwaves"]; got != "Wuthering Waves Game" {
		t.Errorf("wutheringwaves folder = %q, want %q", got, "Wuthering Waves Game")
	}
}

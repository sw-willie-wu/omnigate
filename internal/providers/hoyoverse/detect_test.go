package hoyoverse

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"launcher-collection-tmp/internal/core"
)

func TestDetectInstall_FindsKnownGames(t *testing.T) {
	// Build a fake HoYoPlay tree
	tmp := t.TempDir()
	gamesDir := filepath.Join(tmp, "games")
	if err := os.MkdirAll(filepath.Join(gamesDir, "Genshin Impact game"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(gamesDir, "Star Rail Games"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(gamesDir, "ZenlessZoneZero Game"), 0o755); err != nil {
		t.Fatal(err)
	}
	// (config.ini is read by detect; for now, place a minimal stub)
	if err := os.WriteFile(filepath.Join(tmp, "config.ini"),
		[]byte("[hyp]\nchannel=1\nprimary_game=hyp_global\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := DetectInstall(context.Background(), tmp)
	if err != nil {
		t.Fatalf("DetectInstall: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("got %d games, want 3", len(got))
	}
	seen := map[core.GameID]bool{}
	for _, ig := range got {
		seen[ig.GameID] = true
	}
	for _, want := range []core.GameID{"hoyoverse/genshin", "hoyoverse/starrail", "hoyoverse/zzz"} {
		if !seen[want] {
			t.Errorf("missing game %s", want)
		}
	}
}

func TestDetectInstall_MissingFolderReturnsEmpty(t *testing.T) {
	got, err := DetectInstall(context.Background(), "C:/path/that/does/not/exist")
	if err != nil {
		t.Fatalf("expected nil error, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected 0 games, got %d", len(got))
	}
}

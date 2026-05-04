package kurogames

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCurrentBgURL_FallsBackToDefaultWhenNoCache(t *testing.T) {
	// Point UserConfigDir to an empty temp dir so the cache scan finds nothing.
	t.Setenv("APPDATA", t.TempDir())
	got := CurrentBgURL(nil)
	if got != defaultBgURL {
		t.Errorf("CurrentBgURL on empty FS = %q, want defaultBgURL", got)
	}
}

func TestFindCachedBgURL_PicksLastMatchInNewestFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("APPDATA", tmp)
	base := filepath.Join(tmp, "KRLauncher", "G153", "C50004",
		"KRWebViewUserData", "EBWebView", "Default", "Cache", "Cache_Data")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	// data_2 is older with one match; data_1 is newer with two matches.
	if err := os.WriteFile(filepath.Join(base, "data_2"),
		[]byte(`prefix "firstFrameImage":"https://hw-pcdownload-qcloud.aki-game.net/launcher/clientUpload/OLD123.webp" suffix`),
		0o644); err != nil {
		t.Fatal(err)
	}
	// Force older mtime on data_2.
	old := os.Getenv("TMP") // not used; just to stretch the file write earlier
	_ = old
	// Sleep-free: rewrite data_2 with an explicit older mtime via os.Chtimes.
	older := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(base, "data_2"), older, older); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(base, "data_1"),
		[]byte(`A "firstFrameImage":"https://hw-pcdownload-qcloud.aki-game.net/launcher/clientUpload/MID999.webp" middle "firstFrameImage":"https://hw-pcdownload-qcloud.aki-game.net/launcher/clientUpload/NEWEST_X.webp" tail`),
		0o644); err != nil {
		t.Fatal(err)
	}
	got := findCachedBgURL(nil)
	want := "https://hw-pcdownload-qcloud.aki-game.net/launcher/clientUpload/NEWEST_X.webp"
	if got != want {
		t.Errorf("findCachedBgURL = %q, want %q", got, want)
	}
}

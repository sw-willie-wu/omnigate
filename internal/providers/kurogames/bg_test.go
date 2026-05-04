package kurogames

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCurrentBg_FallsBackToDefaultsWhenNoCache(t *testing.T) {
	// Point UserConfigDir to an empty temp dir so the cache scan finds nothing.
	t.Setenv("APPDATA", t.TempDir())
	img, vid := CurrentBg(nil)
	if img != defaultBgURL {
		t.Errorf("image = %q, want defaultBgURL", img)
	}
	if vid != defaultBgVideoURL {
		t.Errorf("video = %q, want defaultBgVideoURL", vid)
	}
}

func TestFindCachedBgPair_PicksLastPairInNewestFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("APPDATA", tmp)
	base := filepath.Join(tmp, "KRLauncher", "G153", "C50004",
		"KRWebViewUserData", "EBWebView", "Default", "Cache", "Cache_Data")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	// data_2 is older with one pair.
	if err := os.WriteFile(filepath.Join(base, "data_2"),
		[]byte(`prefix "backgroundFile":"https://hw-pcdownload-qcloud.aki-game.net/launcher/clientUpload/OLDVID.mp4","backgroundFileType":2,"firstFrameImage":"https://hw-pcdownload-qcloud.aki-game.net/launcher/clientUpload/OLDIMG.webp" suffix`),
		0o644); err != nil {
		t.Fatal(err)
	}
	older := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.Chtimes(filepath.Join(base, "data_2"), older, older); err != nil {
		t.Fatal(err)
	}
	// data_1 is newer with two pairs; the last one should win.
	if err := os.WriteFile(filepath.Join(base, "data_1"),
		[]byte(`A "backgroundFile":"https://hw-pcdownload-qcloud.aki-game.net/launcher/clientUpload/MIDVID.mp4","backgroundFileType":2,"firstFrameImage":"https://hw-pcdownload-qcloud.aki-game.net/launcher/clientUpload/MIDIMG.webp" middle `+
			`"backgroundFile":"https://hw-pcdownload-qcloud.aki-game.net/launcher/clientUpload/NEWVID.mp4","backgroundFileType":2,"firstFrameImage":"https://hw-pcdownload-qcloud.aki-game.net/launcher/clientUpload/NEWIMG.webp" tail`),
		0o644); err != nil {
		t.Fatal(err)
	}
	gotImg, gotVid := findCachedBgPair(nil)
	wantImg := "https://hw-pcdownload-qcloud.aki-game.net/launcher/clientUpload/NEWIMG.webp"
	wantVid := "https://hw-pcdownload-qcloud.aki-game.net/launcher/clientUpload/NEWVID.mp4"
	if gotImg != wantImg {
		t.Errorf("image = %q, want %q", gotImg, wantImg)
	}
	if gotVid != wantVid {
		t.Errorf("video = %q, want %q", gotVid, wantVid)
	}
}

package app

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSettings_LoadDefaultsWhenMissing(t *testing.T) {
	tmp := t.TempDir()
	s, err := LoadSettings(filepath.Join(tmp, "settings.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if s.App.Language != "zh-TW" {
		t.Errorf("default lang = %s, want zh-TW", s.App.Language)
	}
	if s.App.BannerAnimationPref != "video-when-available" {
		t.Errorf("default bannerPref = %s", s.App.BannerAnimationPref)
	}
	if s.Backends.Hoyoverse.HoYoplayPath != `C:\Program Files\HoYoPlay` {
		t.Errorf("default HoYoPlay path = %s", s.Backends.Hoyoverse.HoYoplayPath)
	}
}

func TestSettings_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "settings.toml")
	s := defaultSettings()
	s.App.Language = "en"
	s.Backends.Hoyoverse.HoYoplayPath = "D:/HoYoPlay"
	if err := SaveSettings(p, s); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.App.Language != "en" || got.Backends.Hoyoverse.HoYoplayPath != "D:/HoYoPlay" {
		t.Errorf("round-trip mismatch: %+v", got)
	}
	// Confirm file exists on disk
	if _, err := os.Stat(p); err != nil {
		t.Errorf("file not written: %v", err)
	}
}

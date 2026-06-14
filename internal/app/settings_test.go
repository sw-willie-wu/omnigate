package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omnigate/internal/providers/hoyoverse"
)

func TestSettings_LoadDefaultsWhenMissing(t *testing.T) {
	tmp := t.TempDir()
	s, err := LoadSettings(filepath.Join(tmp, "settings.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Version != 3 {
		t.Errorf("default Version = %d, want 3", s.Version)
	}
	if s.App.Language != "zh-TW" {
		t.Errorf("default lang = %s, want zh-TW", s.App.Language)
	}
	if s.Backends.Hoyoverse.Path != `C:\Program Files\HoYoPlay` {
		t.Errorf("default hoyoverse path = %q", s.Backends.Hoyoverse.Path)
	}
	if s.Backends.Kurogames.Path != `C:\Program Files\Wuthering Waves` {
		t.Errorf("default kurogames path = %q", s.Backends.Kurogames.Path)
	}
	if s.Backends.Hypergryph.Path != `C:\Program Files\GRYPHLINK` {
		t.Errorf("default hypergryph path = %q", s.Backends.Hypergryph.Path)
	}
}

func TestSettings_RoundTripWritesVersion1(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "settings.toml")
	s := defaultSettings()
	s.App.Language = "en"
	if err := SaveSettings(p, s); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), "version = 3") {
		t.Errorf("written file missing 'version = 3':\n%s", raw)
	}
	if !strings.Contains(string(raw), `path = "C:\\Program Files\\HoYoPlay"`) &&
		!strings.Contains(string(raw), `path = 'C:\Program Files\HoYoPlay'`) {
		t.Errorf("written file missing canonical 'path' key for hoyoverse:\n%s", raw)
	}
	if strings.Contains(string(raw), "hoyoplay_path") {
		t.Errorf("written file should not contain legacy hoyoplay_path:\n%s", raw)
	}
}

func TestSettings_MigrateLegacyHoyoplayPath(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "settings.toml")
	// Write an M1-format settings file with hoyoplay_path
	m1 := `[app]
language = "zh-TW"
banner_animation_pref = "video-when-available"
show_technical_info = false

[backends.hoyoverse]
hoyoplay_path = "D:\\HoYoPlay"
region = "global"
`
	if err := os.WriteFile(p, []byte(m1), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Backends.Hoyoverse.Path != `D:\HoYoPlay` {
		t.Errorf("migrated Path = %q, want D:\\HoYoPlay", s.Backends.Hoyoverse.Path)
	}
	// On save, canonical schema is written
	if err := SaveSettings(p, s); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	if strings.Contains(string(raw), "hoyoplay_path") {
		t.Errorf("save still contains hoyoplay_path; migration incomplete:\n%s", raw)
	}
	if !strings.Contains(string(raw), "version = 3") {
		t.Errorf("save missing version = 3:\n%s", raw)
	}
}

func TestSettings_MalformedTOMLReturnsDefaults(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "settings.toml")
	if err := os.WriteFile(p, []byte("this is not valid toml ====="), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSettings(p)
	if err == nil {
		t.Errorf("expected error from LoadSettings on malformed TOML")
	}
	// Even on error, the returned struct should be safe (defaults).
	if s.Version != 3 {
		t.Errorf("returned Version on malformed = %d, want 3", s.Version)
	}
}

func TestSettings_FreshInstallSavesVersion1(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "settings.toml")
	// LoadSettings on missing file returns defaults silently
	s, err := LoadSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	// Save it back
	if err := SaveSettings(p, s); err != nil {
		t.Fatal(err)
	}
	// Re-load — should NOT trigger migration (Path already populated, Version=3)
	s2, err := LoadSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Version != 3 {
		t.Errorf("re-loaded Version = %d, want 3", s2.Version)
	}
	if s2.Backends.Hoyoverse.Path != `C:\Program Files\HoYoPlay` {
		t.Errorf("re-loaded hoyoverse Path = %q", s2.Backends.Hoyoverse.Path)
	}
}

func TestSettingsV2_GamesRoundTrip(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.toml")
	s := defaultSettings()
	s.Games = map[string]GameSettings{"hoyoverse/genshin": {Path: `D:\G`}}
	if err := SaveSettings(p, s); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 3 {
		t.Errorf("version = %d, want 3", got.Version)
	}
	if got.Games["hoyoverse/genshin"].Path != `D:\G` {
		t.Errorf("override not round-tripped: %+v", got.Games)
	}
}

func TestMigrateV1_CustomRoot_WritesOverrides(t *testing.T) {
	root := t.TempDir()
	_ = os.MkdirAll(filepath.Join(root, "games", "Genshin Impact game"), 0o755) // optional; migration must NOT require it
	raw := "version = 1\n[backends.hoyoverse]\npath = '" + root + "'\n"
	p := filepath.Join(t.TempDir(), "settings.toml")
	_ = os.WriteFile(p, []byte(raw), 0o644)
	got, err := LoadSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	if got.Version != 3 {
		t.Fatalf("version=%d", got.Version)
	}
	want := filepath.Join(root, "games", "Genshin Impact game")
	if got.Games["hoyoverse/genshin"].Path != want {
		t.Errorf("override=%q want %q", got.Games["hoyoverse/genshin"].Path, want)
	}
}

func TestMigrateV1_DefaultRoot_NoOverride(t *testing.T) {
	raw := "version = 1\n[backends.hoyoverse]\npath = '" + hoyoverse.DefaultRoot + "'\n"
	p := filepath.Join(t.TempDir(), "settings.toml")
	_ = os.WriteFile(p, []byte(raw), 0o644)
	got, _ := LoadSettings(p)
	if _, ok := got.Games["hoyoverse/genshin"]; ok {
		t.Errorf("unexpected override for default-root user: %+v", got.Games)
	}
}

func TestMigrateV1_OfflineCustomRoot_SeedsWithoutStat(t *testing.T) {
	root := `Z:\NeverMountedDrive\HoYoPlay` // does not exist
	raw := "version = 1\n[backends.hoyoverse]\npath = '" + root + "'\n"
	p := filepath.Join(t.TempDir(), "settings.toml")
	_ = os.WriteFile(p, []byte(raw), 0o644)
	got, _ := LoadSettings(p)
	want := filepath.Join(root, "games", "Genshin Impact game")
	if got.Games["hoyoverse/genshin"].Path != want {
		t.Errorf("offline override=%q want %q", got.Games["hoyoverse/genshin"].Path, want)
	}
}

func TestMigrateV0Chain_HoyoplayPathToOverride(t *testing.T) {
	// v0 file (hoyoplay_path, no version) → project to path → derive override.
	root := `D:\CustomHoYo`
	raw := "[backends.hoyoverse]\nhoyoplay_path = '" + root + "'\n"
	p := filepath.Join(t.TempDir(), "settings.toml")
	_ = os.WriteFile(p, []byte(raw), 0o644)
	got, _ := LoadSettings(p)
	want := filepath.Join(root, "games", "Genshin Impact game")
	if got.Games["hoyoverse/genshin"].Path != want {
		t.Errorf("v0 chain override=%q want %q", got.Games["hoyoverse/genshin"].Path, want)
	}
}

func TestSettings_V2toV3_MigratesFirstNonEmptyTempDir(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "settings.toml")
	v2 := "version = 2\n\n[backends.hoyoverse]\npath = \"C:\\\\HP\"\nregion = \"global\"\ntemp_dir = \"D:\\\\hoyo-temp\"\n\n[backends.kurogames]\npath = \"C:\\\\WW\"\ntemp_dir = \"D:\\\\kuro-temp\"\n"
	if err := os.WriteFile(p, []byte(v2), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Version != 3 {
		t.Errorf("Version = %d, want 3", s.Version)
	}
	if s.App.TempDir != `D:\hoyo-temp` {
		t.Errorf("App.TempDir = %q, want D:\\hoyo-temp (first non-empty, hoyo first)", s.App.TempDir)
	}
}

func TestSettings_V2toV3_ExplicitAppTempDirNotClobbered(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "settings.toml")
	// Explicit [app] temp_dir present alongside a per-backend temp_dir: the
	// explicit value must win (guard reads raw.App.TempDir before the collapse).
	v2 := "version = 2\n\n[app]\ntemp_dir = \"D:\\\\explicit\"\n\n[backends.hoyoverse]\npath = \"C:\\\\HP\"\nregion = \"global\"\ntemp_dir = \"D:\\\\hoyo\"\n"
	if err := os.WriteFile(p, []byte(v2), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.App.TempDir != `D:\explicit` {
		t.Errorf("App.TempDir = %q, want D:\\explicit (explicit value must not be clobbered)", s.App.TempDir)
	}
}

func TestSettings_V1toV3_MigratesTempDir(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "settings.toml")
	// A v1 file must hit both migrateV1ToV2 and the v2→v3 temp_dir collapse.
	v1 := "version = 1\n\n[backends.kurogames]\npath = \"C:\\\\WW\"\ntemp_dir = \"D:\\\\kuro\"\n"
	if err := os.WriteFile(p, []byte(v1), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Version != 3 {
		t.Errorf("Version = %d, want 3", s.Version)
	}
	if s.App.TempDir != `D:\kuro` {
		t.Errorf("App.TempDir = %q, want D:\\kuro (v1→v3 chain)", s.App.TempDir)
	}
}

func TestSettings_V2toV3_MigratesHypergryphOnlyTempDir(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "settings.toml")
	// Only hypergryph sets a legacy temp_dir → exercises the 3rd precedence slot
	// and the hypergryphRawTOML raw read.
	v2 := "version = 2\n\n[backends.hypergryph]\npath = \"C:\\\\EF\"\ntemp_dir = \"D:\\\\gryph\"\n"
	if err := os.WriteFile(p, []byte(v2), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.App.TempDir != `D:\gryph` {
		t.Errorf("App.TempDir = %q, want D:\\gryph (hypergryph-only migration)", s.App.TempDir)
	}
}

func TestSettings_V2toV3_AllEmptyStaysEmpty(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "settings.toml")
	v2 := "version = 2\n\n[backends.hoyoverse]\npath = \"C:\\\\HP\"\nregion = \"global\"\n"
	if err := os.WriteFile(p, []byte(v2), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.App.TempDir != "" {
		t.Errorf("App.TempDir = %q, want empty", s.App.TempDir)
	}
}

func TestSettings_AppTempDir_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "settings.toml")
	s := defaultSettings()
	s.App.TempDir = `D:\global-temp`
	if err := SaveSettings(p, s); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.App.TempDir != `D:\global-temp` {
		t.Errorf("round-trip App.TempDir = %q", loaded.App.TempDir)
	}
}

func TestSettings_GameBackgroundPath_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "settings.toml")
	s := defaultSettings()
	s.Games = map[string]GameSettings{"hoyoverse/genshin": {BackgroundPath: `D:\pic.png`}}
	if err := SaveSettings(p, s); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Games["hoyoverse/genshin"].BackgroundPath != `D:\pic.png` {
		t.Errorf("round-trip BackgroundPath = %q", loaded.Games["hoyoverse/genshin"].BackgroundPath)
	}
}

func TestSettings_SaveDoesNotEmitBackendTempDir(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "settings.toml")
	s := defaultSettings()
	s.App.TempDir = `D:\g`
	if err := SaveSettings(p, s); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	out := string(b)
	if !strings.Contains(out, `temp_dir = "D:\\g"`) && !strings.Contains(out, "temp_dir = 'D:\\g'") {
		t.Errorf("App.TempDir not written:\n%s", out)
	}
	if strings.Count(out, "temp_dir") != 1 {
		t.Errorf("expected exactly one temp_dir ([app]); got:\n%s", out)
	}
}

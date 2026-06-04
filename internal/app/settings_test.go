package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"

	"omnigate/internal/providers/hoyoverse"
)

func TestSettings_LoadDefaultsWhenMissing(t *testing.T) {
	tmp := t.TempDir()
	s, err := LoadSettings(filepath.Join(tmp, "settings.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Version != 2 {
		t.Errorf("default Version = %d, want 2", s.Version)
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
	if !strings.Contains(string(raw), "version = 2") {
		t.Errorf("written file missing 'version = 2':\n%s", raw)
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
	if !strings.Contains(string(raw), "version = 2") {
		t.Errorf("save missing version = 2:\n%s", raw)
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
	if s.Version != 2 {
		t.Errorf("returned Version on malformed = %d, want 2", s.Version)
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
	// Re-load — should NOT trigger migration (Path already populated, Version=2)
	s2, err := LoadSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	if s2.Version != 2 {
		t.Errorf("re-loaded Version = %d, want 2", s2.Version)
	}
	if s2.Backends.Hoyoverse.Path != `C:\Program Files\HoYoPlay` {
		t.Errorf("re-loaded hoyoverse Path = %q", s2.Backends.Hoyoverse.Path)
	}
}

func TestSettings_KurogamesTempDir_DefaultEmpty(t *testing.T) {
	tmp := t.TempDir()
	s, err := LoadSettings(filepath.Join(tmp, "settings.toml"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Backends.Kurogames.TempDir != "" {
		t.Errorf("default TempDir = %q, want empty", s.Backends.Kurogames.TempDir)
	}
}

func TestSettings_KurogamesTempDir_RoundTrip(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "settings.toml")
	s := defaultSettings()
	s.Backends.Kurogames.TempDir = `D:\my-temp`
	if err := SaveSettings(p, s); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Backends.Kurogames.TempDir != `D:\my-temp` {
		t.Errorf("round-trip TempDir = %q", loaded.Backends.Kurogames.TempDir)
	}
}

func TestSettings_KurogamesTempDir_BackwardCompat(t *testing.T) {
	tmp := t.TempDir()
	p := filepath.Join(tmp, "settings.toml")
	m2 := "version = 1\n\n[app]\nlanguage = \"zh-TW\"\n\n[backends.kurogames]\npath = \"C:\\\\Program Files\\\\Wuthering Waves\"\n"
	if err := os.WriteFile(p, []byte(m2), 0o644); err != nil {
		t.Fatal(err)
	}
	s, err := LoadSettings(p)
	if err != nil {
		t.Fatal(err)
	}
	if s.Backends.Kurogames.Path != `C:\Program Files\Wuthering Waves` {
		t.Errorf("path lost during load: %q", s.Backends.Kurogames.Path)
	}
	if s.Backends.Kurogames.TempDir != "" {
		t.Errorf("TempDir = %q on M2-era file", s.Backends.Kurogames.TempDir)
	}
}

func TestSettings_HoyoverseSettings_TempDir_RoundTrip(t *testing.T) {
	s := Settings{
		Version: 1,
		Backends: BackendSettings{
			Hoyoverse: HoyoverseSettings{
				Path:    `C:\Program Files\HoYoPlay`,
				Region:  "global",
				TempDir: `D:\genshin-temp`,
			},
		},
	}
	data, err := toml.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var s2 Settings
	if err := toml.Unmarshal(data, &s2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s2.Backends.Hoyoverse.TempDir != `D:\genshin-temp` {
		t.Errorf("TempDir round-trip lost: %q", s2.Backends.Hoyoverse.TempDir)
	}
}

func TestSettings_HoyoverseSettings_TempDir_Omitempty(t *testing.T) {
	s := Settings{
		Version: 1,
		Backends: BackendSettings{
			Hoyoverse: HoyoverseSettings{Path: `C:\Program Files\HoYoPlay`, Region: "global"},
			// TempDir omitted → zero value ""
		},
	}
	data, err := toml.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "temp_dir") {
		t.Errorf("zero-value TempDir should be omitted; got:\n%s", string(data))
	}
}

func TestSettings_HypergryphTempDirRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "settings.toml")
	s := defaultSettings() // NOTE: unexported (settings.go:61); NOT DefaultSettings
	s.Backends.Hypergryph.Path = `C:\Games\GRYPHLINK`
	s.Backends.Hypergryph.TempDir = `D:\omnigate-temp`
	if err := SaveSettings(path, s); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := LoadSettings(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if loaded.Backends.Hypergryph.TempDir != `D:\omnigate-temp` {
		t.Errorf("TempDir = %q, want D:\\omnigate-temp", loaded.Backends.Hypergryph.TempDir)
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
	if got.Version != 2 {
		t.Errorf("version = %d, want 2", got.Version)
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
	if got.Version != 2 {
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

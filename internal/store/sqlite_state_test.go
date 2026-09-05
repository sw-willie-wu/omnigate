package store

import (
	"path/filepath"
	"testing"
)

func openTmp(t *testing.T) *SQLiteStore {
	t.Helper()
	s, err := OpenSQLite(filepath.Join(t.TempDir(), "omnigate.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestDeleteMeta(t *testing.T) {
	s := openTmp(t)
	if err := s.SetMeta("k", "v"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetMeta("keep", "1"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteMeta("k"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := s.GetMeta("k"); err != nil || ok {
		t.Fatalf("GetMeta after delete: ok=%v err=%v", ok, err)
	}
	// deletion is scoped to its key — sibling meta rows survive
	if _, ok, _ := s.GetMeta("keep"); !ok {
		t.Fatal("unrelated meta key deleted")
	}
	// deleting an absent key is a no-op, not an error
	if err := s.DeleteMeta("absent"); err != nil {
		t.Fatalf("DeleteMeta(absent) = %v", err)
	}
}

func TestMetaConfigRoundTrip(t *testing.T) {
	s := openTmp(t)
	if _, ok, _ := s.GetMeta("legacy_settings_migrated"); ok {
		t.Fatal("unexpected meta")
	}
	if err := s.SetMeta("legacy_settings_migrated", "1"); err != nil {
		t.Fatal(err)
	}
	if v, ok, _ := s.GetMeta("legacy_settings_migrated"); !ok || v != "1" {
		t.Fatalf("meta=%q ok=%v", v, ok)
	}

	if err := s.SetConfig("app.language", "zh-TW"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetConfig("app.language", "en"); err != nil { // upsert
		t.Fatal(err)
	}
	all, _ := s.AllConfig()
	if all["app.language"] != "en" {
		t.Fatalf("config=%v", all)
	}
}

func TestGameSettingsReplace(t *testing.T) {
	s := openTmp(t)
	if err := s.ReplaceGameSettings(map[string]GameOverride{
		"GenshinImpact":  {Path: `C:\g`, BackgroundPath: `C:\bg.png`},
		"WutheringWaves": {Path: "", BackgroundPath: `C:\w.png`}, // ClearGameOverride shape
	}); err != nil {
		t.Fatal(err)
	}
	got, _ := s.AllGameSettings()
	if len(got) != 2 || got["WutheringWaves"].BackgroundPath != `C:\w.png` || got["WutheringWaves"].Path != "" {
		t.Fatalf("got=%v", got)
	}
	// Replace is authoritative: a game absent from the map is deleted.
	s.ReplaceGameSettings(map[string]GameOverride{"GenshinImpact": {Path: `C:\g2`}})
	got, _ = s.AllGameSettings()
	if len(got) != 1 || got["GenshinImpact"].Path != `C:\g2` {
		t.Fatalf("got=%v", got)
	}
}

func TestPlaystateRoundTrip(t *testing.T) {
	s := openTmp(t)
	if err := s.SetPlaystate("genshin", 1700000000); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPlaystate("genshin", 1700000099); err != nil { // upsert
		t.Fatal(err)
	}
	all, _ := s.AllPlaystate()
	if all["genshin"] != 1700000099 {
		t.Fatalf("playstate=%v", all)
	}
}

func TestAccountUIDRoundTrip(t *testing.T) {
	s := openTmp(t)
	s.SetAccountUID("cuid-1", "100200300", "")
	s.SetAccountUID("cuid-1", "100200300", "Main") // upsert label
	all, _ := s.AllAccountUID()
	if all["cuid-1"].UID != "100200300" || all["cuid-1"].Label != "Main" {
		t.Fatalf("uid=%v", all)
	}
}

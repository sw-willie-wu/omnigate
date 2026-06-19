package app

import (
	"path/filepath"
	"testing"

	"omnigate/internal/store"
)

func openState(t *testing.T) store.StateStore {
	t.Helper()
	s, err := store.OpenSQLite(filepath.Join(t.TempDir(), "omnigate.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSettingsDB_RoundTrip_BoolAndEmptyPathParity(t *testing.T) {
	st := openState(t)
	in := defaultSettings()
	in.App.ShowTechnicalInfo = true
	in.App.Language = "en"
	in.Backends.Hoyoverse.Path = "" // cleared → must load back as the default, not ""
	in.Backends.Kurogames.Path = `D:\WW`
	in.Games["GenshinImpact"] = GameSettings{Path: "", BackgroundPath: `C:\bg.png`}
	if err := saveSettingsToDB(st, in); err != nil {
		t.Fatal(err)
	}

	got, err := loadSettingsFromDB(st)
	if err != nil {
		t.Fatal(err)
	}
	def := defaultSettings()
	if got.App.ShowTechnicalInfo != true {
		t.Fatal("ShowTechnicalInfo=true lost")
	}
	if got.App.Language != "en" {
		t.Fatalf("language=%q", got.App.Language)
	}
	if got.Backends.Hoyoverse.Path != def.Backends.Hoyoverse.Path {
		t.Fatalf("empty hoyo path should fall back to default, got %q", got.Backends.Hoyoverse.Path)
	}
	if got.Backends.Kurogames.Path != `D:\WW` {
		t.Fatalf("kuro path=%q", got.Backends.Kurogames.Path)
	}
	if got.Backends.Hoyoverse.Region != def.Backends.Hoyoverse.Region {
		t.Fatalf("region=%q", got.Backends.Hoyoverse.Region)
	}
	if got.Games == nil {
		t.Fatal("Games must be non-nil")
	}
	g := got.Games["GenshinImpact"]
	if g.Path != "" || g.BackgroundPath != `C:\bg.png` {
		t.Fatalf("game override=%+v", g)
	}
	if got.Version != 3 {
		t.Fatalf("version=%d", got.Version)
	}
}

func TestSettingsDB_FalseBoolSurvives(t *testing.T) {
	st := openState(t)
	// Seed an explicit "true" row first, then save false — load must reflect
	// false. This distinguishes "row parsed as false" from "row ignored → default
	// false" (which a default of false would otherwise mask).
	st.SetConfig(ckShowTech, "true")
	in := defaultSettings()
	in.App.ShowTechnicalInfo = false
	saveSettingsToDB(st, in)
	got, _ := loadSettingsFromDB(st)
	if got.App.ShowTechnicalInfo != false {
		t.Fatal("false must overwrite a prior true, not be defaulted/ignored")
	}
}

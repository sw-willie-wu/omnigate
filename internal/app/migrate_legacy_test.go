package app

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"omnigate/internal/store"
)

func writeFile(t *testing.T, p, body string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func openStoreAt(t *testing.T, dataDir string) *store.SQLiteStore {
	t.Helper()
	s, err := store.OpenSQLite(filepath.Join(dataDir, "omnigate.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestPromoteGachaDB_CreatesOmnigateAndKeepsSource(t *testing.T) {
	d := t.TempDir()
	writeFile(t, filepath.Join(d, "gacha.db"), "PULLS")
	pendingBak, err := promoteGachaDB(d, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pendingBak != filepath.Join(d, "gacha.db") {
		t.Fatalf("pendingBak=%q", pendingBak)
	}
	if b, _ := os.ReadFile(filepath.Join(d, "omnigate.db")); string(b) != "PULLS" {
		t.Fatal("omnigate.db not promoted")
	}
	// crash-safety: source still present until the post-open .bak rename
	if _, err := os.Stat(filepath.Join(d, "gacha.db")); err != nil {
		t.Fatal("source gacha.db should still exist")
	}
	if _, err := os.Stat(filepath.Join(d, "omnigate.db.tmp")); !os.IsNotExist(err) {
		t.Fatal(".tmp should be gone")
	}
}

func TestPromoteGachaDB_StaleTmpCleaned(t *testing.T) {
	d := t.TempDir()
	writeFile(t, filepath.Join(d, "omnigate.db.tmp"), "GARBAGE")
	writeFile(t, filepath.Join(d, "gacha.db"), "PULLS")
	if _, err := promoteGachaDB(d, nil); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(filepath.Join(d, "omnigate.db")); string(b) != "PULLS" {
		t.Fatal("stale tmp not replaced by clean copy")
	}
}

func TestPromoteGachaDB_BothPresent_NoChange(t *testing.T) {
	d := t.TempDir()
	writeFile(t, filepath.Join(d, "omnigate.db"), "REAL")
	writeFile(t, filepath.Join(d, "gacha.db"), "OLD")
	pendingBak, err := promoteGachaDB(d, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pendingBak != "" {
		t.Fatal("should not promote when omnigate.db exists")
	}
	if b, _ := os.ReadFile(filepath.Join(d, "omnigate.db")); string(b) != "REAL" {
		t.Fatal("omnigate.db clobbered")
	}
}

func TestImportLegacy_AllThree_RenamesBakAndSetsFlags(t *testing.T) {
	d := t.TempDir()
	writeFile(t, filepath.Join(d, "settings.toml"), "version = 3\n[app]\nlanguage = \"en\"\n")
	pj, _ := json.Marshal(map[string]string{"genshin": "2026-01-02T03:04:05Z"})
	writeFile(t, filepath.Join(d, "playstate.json"), string(pj))
	writeFile(t, filepath.Join(d, "wuwa_uid_cache.json"), `{"cuid-1":"100200300"}`) // legacy bare-string form
	st := openStoreAt(t, d)

	importLegacyFiles(d, st, nil)

	if c, _ := st.AllConfig(); c[ckLanguage] != "en" {
		t.Fatalf("settings not imported: %v", c)
	}
	wantT, _ := time.Parse(time.RFC3339, "2026-01-02T03:04:05Z")
	if ps, _ := st.AllPlaystate(); ps["genshin"] != wantT.Unix() {
		t.Fatalf("playstate mapped wrong: got %d want %d", ps["genshin"], wantT.Unix())
	}
	if u, _ := st.AllAccountUID(); u["cuid-1"].UID != "100200300" {
		t.Fatal("uid not imported")
	}
	for _, f := range []string{"settings.toml", "playstate.json", "wuwa_uid_cache.json"} {
		if _, err := os.Stat(filepath.Join(d, f)); !os.IsNotExist(err) {
			t.Fatalf("%s should be renamed .bak", f)
		}
		if _, err := os.Stat(filepath.Join(d, f+".bak")); err != nil {
			t.Fatalf("%s.bak missing", f)
		}
	}
	for _, k := range []string{"legacy_settings_migrated", "legacy_playstate_migrated", "legacy_uid_migrated"} {
		if v, ok, _ := st.GetMeta(k); !ok || v != "1" {
			t.Fatalf("flag %s not set", k)
		}
	}
}

func TestImportLegacy_FlagSetBeforeRename_NoReimportClobber(t *testing.T) {
	d := t.TempDir()
	writeFile(t, filepath.Join(d, "settings.toml"), "version = 3\n[app]\nlanguage = \"en\"\n")
	st := openStoreAt(t, d)
	// Pre-set the flag to simulate "imported but rename failed last run".
	st.SetMeta("legacy_settings_migrated", "1")
	// Simulate a user edit already in the DB:
	st.SetConfig(ckLanguage, "ja")
	importLegacyFiles(d, st, nil)
	if c, _ := st.AllConfig(); c[ckLanguage] != "ja" {
		t.Fatal("stale settings.toml must NOT clobber the DB edit")
	}
}

func TestImportLegacy_CWDFallback(t *testing.T) {
	// settings.toml only in CWD (≠ dataDir): still imported once.
	d := t.TempDir()
	cwd := t.TempDir()
	writeFile(t, filepath.Join(cwd, "settings.toml"), "version = 3\n[app]\nlanguage = \"ko\"\n")
	st := openStoreAt(t, d)
	importLegacyFilesFrom(d, cwd, st, nil)
	if c, _ := st.AllConfig(); c[ckLanguage] != "ko" {
		t.Fatal("CWD-fallback import failed")
	}
}

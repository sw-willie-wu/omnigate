package app

import (
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"time"

	"omnigate/internal/store"
)

func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}

// promoteGachaDB turns a legacy <dataDir>/gacha.db into <dataDir>/omnigate.db
// via a crash-safe temp→rename (spec §6.2). Returns the source gacha.db path
// (pendingBak) to be renamed to .bak AFTER the caller successfully opens the DB,
// or "" if nothing was promoted. logger may be nil.
func promoteGachaDB(dataDir string, logger *slog.Logger) (pendingBak string, err error) {
	if logger == nil {
		logger = slog.Default()
	}
	db := filepath.Join(dataDir, "omnigate.db")
	tmp := db + ".tmp"
	gacha := filepath.Join(dataDir, "gacha.db")

	_ = os.Remove(tmp) // discard any stale .tmp from a prior crashed copy

	if _, err := os.Stat(db); err == nil {
		if _, gerr := os.Stat(gacha); gerr == nil {
			logger.Warn("legacy gacha.db present alongside omnigate.db; its pulls are NOT auto-merged", "path", gacha)
		}
		return "", nil
	}
	if _, gerr := os.Stat(gacha); gerr != nil {
		return "", nil // no legacy DB
	}
	if err := copyFile(gacha, tmp); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, db); err != nil { // atomic on same volume
		_ = os.Remove(tmp)
		return "", err
	}
	return gacha, nil // caller renames → .bak only after a successful open
}

// importLegacyFiles is the production entry: looks in dataDir, falling back to
// the process CWD for each source (spec §6.5).
func importLegacyFiles(dataDir string, st store.StateStore, logger *slog.Logger) {
	cwd, _ := os.Getwd()
	importLegacyFilesFrom(dataDir, cwd, st, logger)
}

// importLegacyFilesFrom is the testable seam with an explicit fallback dir. Each
// source is gated on its own meta flag, set BEFORE the rename so a successful
// import is never re-applied even if the rename fails or a sibling errors
// (spec §6.4 / review NEW-MAJOR-A).
func importLegacyFilesFrom(dataDir, cwd string, st store.StateStore, logger *slog.Logger) {
	if logger == nil {
		logger = slog.Default()
	}
	findFile := func(name string) string {
		p := filepath.Join(dataDir, name)
		if _, err := os.Stat(p); err == nil {
			return p
		}
		if cwd != "" && cwd != dataDir {
			q := filepath.Join(cwd, name)
			if _, err := os.Stat(q); err == nil {
				return q
			}
		}
		return ""
	}
	importOne := func(flag, name string, do func(path string) error) {
		if v, ok, _ := st.GetMeta(flag); ok && v == "1" {
			return
		}
		path := findFile(name)
		if path == "" {
			return
		}
		if err := do(path); err != nil {
			logger.Error("legacy import failed; will retry next run", "source", name, "err", err)
			return
		}
		if err := st.SetMeta(flag, "1"); err != nil { // flag BEFORE rename (spec §6.6)
			logger.Error("legacy flag write failed", "source", name, "err", err)
			return
		}
		if err := os.Rename(path, path+".bak"); err != nil {
			logger.Warn("legacy .bak rename failed; source lingers but is guarded by flag", "source", name, "err", err)
		}
	}

	importOne("legacy_settings_migrated", "settings.toml", func(p string) error {
		s, err := LoadSettings(p) // retained file-based loader + v0/v1/v2→v3 migrations
		if err != nil {
			return err
		}
		return saveSettingsToDB(st, s)
	})
	importOne("legacy_playstate_migrated", "playstate.json", func(p string) error {
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		var m map[string]time.Time
		if err := json.Unmarshal(b, &m); err != nil {
			return err
		}
		for game, t := range m {
			if err := st.SetPlaystate(game, t.Unix()); err != nil {
				return err
			}
		}
		return nil
	})
	importOne("legacy_uid_migrated", "wuwa_uid_cache.json", func(p string) error {
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		var m map[string]acctMeta // acctMeta.UnmarshalJSON handles legacy bare-string
		if err := json.Unmarshal(b, &m); err != nil {
			return err
		}
		for cuid, a := range m {
			if err := st.SetAccountUID(cuid, a.UID, a.Label); err != nil {
				return err
			}
		}
		return nil
	})
}

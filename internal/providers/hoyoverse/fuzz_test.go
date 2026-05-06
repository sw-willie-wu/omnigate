package hoyoverse

import (
	"os"
	"path/filepath"
	"testing"
)

func FuzzConfigIni(f *testing.F) {
	f.Add([]byte("[General]\ngame_version=5.6.0\n"))
	f.Fuzz(func(t *testing.T, data []byte) {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, "config.ini"), data, 0o644)
		_, _ = ReadGameVersion(dir) // must not panic
	})
}

func FuzzManifestParse(f *testing.F) {
	f.Add([]byte(`{"retcode":0,"data":{"game_packages":[]}}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = parseGamePackagesResponse(data)
	})
}

func FuzzHdiffmapParse(f *testing.F) {
	f.Add([]byte(`{"entries":[]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = parseHdiffmap(data)
	})
}

func FuzzApplyWALParse(f *testing.F) {
	f.Add([]byte(`{"pending":[],"done":[]}`))
	f.Fuzz(func(t *testing.T, data []byte) {
		dir := t.TempDir()
		_ = os.WriteFile(filepath.Join(dir, "apply.wal"), data, 0o644)
		_, _ = readApplyWAL(dir)
	})
}

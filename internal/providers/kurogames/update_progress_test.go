package kurogames

import (
	"encoding/json"
	"launcher-collection-tmp/internal/core"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProgress_WriteAndLoadEntry(t *testing.T) {
	tmp := t.TempDir()
	p := newProgressStore(tmp, "kurogames/wutheringwaves", "3.4.0")
	if err := p.Init("etag-abc"); err != nil {
		t.Fatal(err)
	}
	mt := time.Now().Truncate(time.Millisecond)
	if err := p.MarkComplete("Engine/foo.dll", mt, 12345); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadProgress(p.dir())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ETag != "etag-abc" {
		t.Errorf("ETag = %q", loaded.ETag)
	}
	e, ok := loaded.Entries["Engine/foo.dll"]
	if !ok {
		t.Fatal("entry not loaded")
	}
	if e.Size != 12345 || !e.MTime.Equal(mt) {
		t.Errorf("entry mismatch: %+v", e)
	}
}

func TestProgress_AtomicWrite(t *testing.T) {
	tmp := t.TempDir()
	p := newProgressStore(tmp, "kurogames/wutheringwaves", "3.4.0")
	if err := p.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(p.dir(), "progress.json")
	tmpPath := mainPath + ".tmp"
	if _, err := os.Stat(mainPath); err != nil {
		t.Errorf("progress.json missing: %v", err)
	}
	if _, err := os.Stat(tmpPath); err == nil {
		t.Errorf(".tmp should be cleaned")
	}
}

func TestProgress_LoadCorruptReturnsErr(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "kurogames-wutheringwaves", "3.4.0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "progress.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadProgress(dir); err == nil {
		t.Error("expected err on corrupt JSON")
	}
}

func TestProgress_RenameToPredlReady(t *testing.T) {
	tmp := t.TempDir()
	p := newProgressStore(tmp, "kurogames/wutheringwaves", "3.4.0")
	if err := p.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	if err := p.MarkComplete("a.dll", time.Now(), 1); err != nil {
		t.Fatal(err)
	}
	if err := p.RenameToPredlReady(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.dir(), "progress.json")); err == nil {
		t.Errorf("progress.json should be gone")
	}
	body, _ := os.ReadFile(filepath.Join(p.dir(), "predl_ready.json"))
	var v struct {
		ETag string `json:"etag"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("predl_ready.json malformed: %v", err)
	}
	if v.ETag != "etag-1" {
		t.Errorf("ETag = %q", v.ETag)
	}
}

func TestProgress_RecoveryScan_ApplyWalWins(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "kurogames-wutheringwaves", "3.4.0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "apply.wal"), []byte(`{"etag":"e","done":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "progress.json"), []byte(`{"etag":"e","entries":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	state := ScanRecovery(dir)
	if state.Phase != core.RecoveryPhaseApplyResume {
		t.Errorf("Phase = %v, want core.RecoveryPhaseApplyResume", state.Phase)
	}
	if _, err := os.Stat(filepath.Join(dir, "progress.json")); err == nil {
		t.Errorf("progress.json should be deleted")
	}
}

func TestProgress_RecoveryScan_PredlOverProgress(t *testing.T) {
	tmp := t.TempDir()
	dir := filepath.Join(tmp, "kurogames-wutheringwaves", "3.4.0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "predl_ready.json"), []byte(`{"etag":"e","entries":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "progress.json"), []byte(`{"etag":"e","entries":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	state := ScanRecovery(dir)
	if state.Phase != core.RecoveryPhasePredlAwaiting {
		t.Errorf("Phase = %v, want core.RecoveryPhasePredlAwaiting", state.Phase)
	}
	if _, err := os.Stat(filepath.Join(dir, "progress.json")); err == nil {
		t.Errorf("progress.json should be deleted")
	}
}

// Spec §7.2 mandates 4 RecoveryScan variants for the (phase × wasPredl)
// matrix referenced by spec §3.5 row 4 (interrupted_resume LastError) +
// errcode_coverage_test.go.

func TestRecoveryScan_DownloadOnly_NotFromPredl(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "progress.json"), []byte(`{"etag":"e","entries":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	state := ScanRecovery(tmp)
	if state.Phase != core.RecoveryPhaseDownloadResume {
		t.Errorf("Phase = %v, want core.RecoveryPhaseDownloadResume", state.Phase)
	}
	if state.WasPredl {
		t.Errorf("WasPredl = true; download-only sidecar has no predl context")
	}
}

func TestRecoveryScan_ApplyResume_FromFreshDownload(t *testing.T) {
	tmp := t.TempDir()
	wal := `{"etag":"e","was_predl":false,"pending":["a"],"done":[]}`
	if err := os.WriteFile(filepath.Join(tmp, "apply.wal"), []byte(wal), 0o644); err != nil {
		t.Fatal(err)
	}
	state := ScanRecovery(tmp)
	if state.Phase != core.RecoveryPhaseApplyResume {
		t.Errorf("Phase = %v, want core.RecoveryPhaseApplyResume", state.Phase)
	}
	if state.WasPredl {
		t.Errorf("WasPredl = true; WAL was_predl=false")
	}
}

func TestRecoveryScan_ApplyResume_FromPredl(t *testing.T) {
	tmp := t.TempDir()
	wal := `{"etag":"e","was_predl":true,"pending":["a"],"done":[]}`
	if err := os.WriteFile(filepath.Join(tmp, "apply.wal"), []byte(wal), 0o644); err != nil {
		t.Fatal(err)
	}
	state := ScanRecovery(tmp)
	if state.Phase != core.RecoveryPhaseApplyResume {
		t.Errorf("Phase = %v, want core.RecoveryPhaseApplyResume", state.Phase)
	}
	if !state.WasPredl {
		t.Errorf("WasPredl = false; WAL was_predl=true")
	}
}

func TestRecoveryScan_CorruptWal(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "apply.wal"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := ScanRecovery(tmp)
	if state.Phase != core.RecoveryCorrupt {
		t.Errorf("Phase = %v, want core.RecoveryCorrupt", state.Phase)
	}
}

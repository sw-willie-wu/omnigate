package core

import (
	"os"
	"path/filepath"
	"testing"
)

func TestScanRecovery_ApplyWalWins(t *testing.T) {
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
	if state.Phase != RecoveryPhaseApplyResume {
		t.Errorf("Phase = %v, want RecoveryPhaseApplyResume", state.Phase)
	}
	if _, err := os.Stat(filepath.Join(dir, "progress.json")); err == nil {
		t.Errorf("progress.json should be deleted")
	}
}

func TestScanRecovery_PredlOverProgress(t *testing.T) {
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
	if state.Phase != RecoveryPhasePredlAwaiting {
		t.Errorf("Phase = %v, want RecoveryPhasePredlAwaiting", state.Phase)
	}
	if _, err := os.Stat(filepath.Join(dir, "progress.json")); err == nil {
		t.Errorf("progress.json should be deleted")
	}
}

func TestScanRecovery_DownloadOnly_NotFromPredl(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "progress.json"), []byte(`{"etag":"e","entries":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	state := ScanRecovery(tmp)
	if state.Phase != RecoveryPhaseDownloadResume {
		t.Errorf("Phase = %v, want RecoveryPhaseDownloadResume", state.Phase)
	}
	if state.WasPredl {
		t.Errorf("WasPredl = true; download-only sidecar has no predl context")
	}
}

func TestScanRecovery_ApplyResume_FromFreshDownload(t *testing.T) {
	tmp := t.TempDir()
	wal := `{"etag":"e","was_predl":false,"pending":["a"],"done":[]}`
	if err := os.WriteFile(filepath.Join(tmp, "apply.wal"), []byte(wal), 0o644); err != nil {
		t.Fatal(err)
	}
	state := ScanRecovery(tmp)
	if state.Phase != RecoveryPhaseApplyResume {
		t.Errorf("Phase = %v, want RecoveryPhaseApplyResume", state.Phase)
	}
	if state.WasPredl {
		t.Errorf("WasPredl = true; WAL was_predl=false")
	}
}

func TestScanRecovery_ApplyResume_FromPredl(t *testing.T) {
	tmp := t.TempDir()
	wal := `{"etag":"e","was_predl":true,"pending":["a"],"done":[]}`
	if err := os.WriteFile(filepath.Join(tmp, "apply.wal"), []byte(wal), 0o644); err != nil {
		t.Fatal(err)
	}
	state := ScanRecovery(tmp)
	if state.Phase != RecoveryPhaseApplyResume {
		t.Errorf("Phase = %v, want RecoveryPhaseApplyResume", state.Phase)
	}
	if !state.WasPredl {
		t.Errorf("WasPredl = false; WAL was_predl=true")
	}
}

func TestScanRecovery_CorruptWal(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "apply.wal"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := ScanRecovery(tmp)
	if state.Phase != RecoveryCorrupt {
		t.Errorf("Phase = %v, want RecoveryCorrupt", state.Phase)
	}
}

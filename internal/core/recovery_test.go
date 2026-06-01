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

func TestScanRecovery_SophonApplyWAL(t *testing.T) {
	tmp := t.TempDir()
	wal := `{"game_id":"genshin","target_tag":"6.6.0","was_predl":false,"records":[{"kind":"chunk_assemble","state":"pending"}]}`
	if err := os.WriteFile(filepath.Join(tmp, "sophon_apply.wal"), []byte(wal), 0o644); err != nil {
		t.Fatal(err)
	}
	state := ScanRecovery(tmp)
	if state.Phase != RecoveryPhaseApplyResume {
		t.Errorf("Phase = %v, want RecoveryPhaseApplyResume", state.Phase)
	}
	if state.WasPredl {
		t.Errorf("WasPredl = true; sophon WAL was_predl=false")
	}
}

func TestScanRecovery_SophonApplyWAL_WasPredl(t *testing.T) {
	tmp := t.TempDir()
	wal := `{"game_id":"genshin","target_tag":"6.6.0","was_predl":true,"records":[]}`
	if err := os.WriteFile(filepath.Join(tmp, "sophon_apply.wal"), []byte(wal), 0o644); err != nil {
		t.Fatal(err)
	}
	state := ScanRecovery(tmp)
	if state.Phase != RecoveryPhaseApplyResume {
		t.Errorf("Phase = %v, want RecoveryPhaseApplyResume", state.Phase)
	}
	if !state.WasPredl {
		t.Errorf("WasPredl = false; sophon WAL was_predl=true")
	}
}

func TestScanRecovery_SophonApplyWAL_Corrupt(t *testing.T) {
	tmp := t.TempDir()
	if err := os.WriteFile(filepath.Join(tmp, "sophon_apply.wal"), []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	state := ScanRecovery(tmp)
	if state.Phase != RecoveryCorrupt {
		t.Errorf("Phase = %v, want RecoveryCorrupt", state.Phase)
	}
}

func TestScanRecovery_SophonProgress(t *testing.T) {
	tmp := t.TempDir()
	prog := `{"game_id":"genshin","version":"6.6.0","stage":"download","chunks_done":{"abc":true}}`
	if err := os.WriteFile(filepath.Join(tmp, "sophon_progress.json"), []byte(prog), 0o644); err != nil {
		t.Fatal(err)
	}
	state := ScanRecovery(tmp)
	if state.Phase != RecoveryPhaseDownloadResume {
		t.Errorf("Phase = %v, want RecoveryPhaseDownloadResume", state.Phase)
	}
}

func TestScanRecovery_Precedence_SophonOverV1(t *testing.T) {
	tmp := t.TempDir()
	// sophon_apply.wal present alongside the full v1 sidecar set.
	sophonWAL := `{"game_id":"genshin","target_tag":"6.6.0","was_predl":true,"records":[]}`
	if err := os.WriteFile(filepath.Join(tmp, "sophon_apply.wal"), []byte(sophonWAL), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "apply.wal"), []byte(`{"etag":"e","was_predl":false,"done":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "sophon_progress.json"), []byte(`{"chunks_done":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "progress.json"), []byte(`{"etag":"e","entries":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "predl_ready.json"), []byte(`{"etag":"e","entries":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	state := ScanRecovery(tmp)
	if state.Phase != RecoveryPhaseApplyResume {
		t.Errorf("Phase = %v, want RecoveryPhaseApplyResume (sophon WAL wins)", state.Phase)
	}
	if !state.WasPredl {
		t.Errorf("WasPredl = false; should come from the sophon WAL header (was_predl=true)")
	}
	// Sophon WAL supersedes ALL v1 companions at the same scope.
	for _, stale := range []string{"apply.wal", "sophon_progress.json", "progress.json", "predl_ready.json"} {
		if _, err := os.Stat(filepath.Join(tmp, stale)); err == nil {
			t.Errorf("%s should be deleted (superseded by sophon_apply.wal)", stale)
		}
	}
}

func TestScanRecovery_Precedence_SophonProgressOverV1Progress(t *testing.T) {
	tmp := t.TempDir()
	// sophon_progress.json beats v1 progress.json (no WALs present).
	if err := os.WriteFile(filepath.Join(tmp, "sophon_progress.json"), []byte(`{"chunks_done":{"a":true}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "progress.json"), []byte(`{"etag":"e","entries":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(tmp, "predl_ready.json"), []byte(`{"etag":"e","entries":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	state := ScanRecovery(tmp)
	if state.Phase != RecoveryPhaseDownloadResume {
		t.Errorf("Phase = %v, want RecoveryPhaseDownloadResume (sophon progress wins over v1)", state.Phase)
	}
	for _, stale := range []string{"progress.json", "predl_ready.json"} {
		if _, err := os.Stat(filepath.Join(tmp, stale)); err == nil {
			t.Errorf("%s should be deleted (superseded by sophon_progress.json)", stale)
		}
	}
}

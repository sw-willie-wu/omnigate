package core

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// RecoveryPhase identifies the in-progress sidecar state a directory contains.
// Used by ScanRecovery (defined in this package, body added in Task 2) to
// surface what the App layer should resume on next launch.
type RecoveryPhase int

const (
	RecoveryNone RecoveryPhase = iota
	RecoveryPhaseDownloadResume
	RecoveryPhaseApplyResume
	RecoveryPhasePredlAwaiting
	RecoveryCorrupt
)

// RecoveryState bundles the result of ScanRecovery: which phase the sidecar
// directory is in, whether the apply.wal was originally a predownload (so UI
// can surface "predl-resume" copy), and any parse error encountered.
type RecoveryState struct {
	Phase    RecoveryPhase
	WasPredl bool
	Err      error
}

// ScanRecovery resolves sidecar collisions in a version-scoped temp dir.
// Returns the recovery phase based on which sidecar files exist + their parse
// state. Side effects: cleans up stale companions when a definitive sidecar
// is found (e.g. apply.wal supersedes progress.json + predl_ready.json).
//
// WasPredl is set from apply.wal's `was_predl` header field — distinguishes
// "interrupted apply originated from a predl" from "interrupted apply from a
// fresh download", surfaced in the resume prompt copy.
func ScanRecovery(dir string) RecoveryState {
	hasSophonWAL := fileExists(filepath.Join(dir, "sophon_apply.wal"))
	hasSophonProgress := fileExists(filepath.Join(dir, "sophon_progress.json"))
	hasProgress := fileExists(filepath.Join(dir, "progress.json"))
	hasWAL := fileExists(filepath.Join(dir, "apply.wal"))
	hasPredl := fileExists(filepath.Join(dir, "predl_ready.json"))

	// Precedence (spec §0 / §6.9):
	//   sophon_apply.wal > apply.wal > sophon_progress.json
	//     > progress.json+predl > progress.json > predl_ready.json
	// Sophon sidecars supersede v1 companions at the same scope; the prevailing
	// sidecar deletes the stale companions (same semantics as v1 apply.wal).
	switch {
	case hasSophonWAL:
		// Sophon apply WAL supersedes every v1 companion AND sophon_progress.
		for _, stale := range []string{"apply.wal", "sophon_progress.json", "progress.json", "predl_ready.json"} {
			_ = os.Remove(filepath.Join(dir, stale))
		}
		walPath := filepath.Join(dir, "sophon_apply.wal")
		body, err := os.ReadFile(walPath)
		if err != nil {
			return RecoveryState{Phase: RecoveryCorrupt, Err: err}
		}
		var hdr struct {
			WasPredl bool `json:"was_predl"`
		}
		if err := json.Unmarshal(body, &hdr); err != nil {
			return RecoveryState{Phase: RecoveryCorrupt, Err: err}
		}
		return RecoveryState{Phase: RecoveryPhaseApplyResume, WasPredl: hdr.WasPredl}

	case hasWAL:
		if hasSophonProgress {
			_ = os.Remove(filepath.Join(dir, "sophon_progress.json"))
		}
		if hasProgress {
			_ = os.Remove(filepath.Join(dir, "progress.json"))
		}
		if hasPredl {
			_ = os.Remove(filepath.Join(dir, "predl_ready.json"))
		}
		walPath := filepath.Join(dir, "apply.wal")
		body, err := os.ReadFile(walPath)
		if err != nil {
			return RecoveryState{Phase: RecoveryCorrupt, Err: err}
		}
		var hdr struct {
			WasPredl bool `json:"was_predl"`
		}
		if err := json.Unmarshal(body, &hdr); err != nil {
			return RecoveryState{Phase: RecoveryCorrupt, Err: err}
		}
		return RecoveryState{Phase: RecoveryPhaseApplyResume, WasPredl: hdr.WasPredl}

	case hasSophonProgress:
		// Sophon download-phase resume. Supersedes v1 progress.json + predl_ready.json.
		if hasProgress {
			_ = os.Remove(filepath.Join(dir, "progress.json"))
		}
		if hasPredl {
			_ = os.Remove(filepath.Join(dir, "predl_ready.json"))
		}
		return RecoveryState{Phase: RecoveryPhaseDownloadResume}

	case hasProgress && hasPredl:
		_ = os.Remove(filepath.Join(dir, "progress.json"))
		if _, err := loadProgressFile(filepath.Join(dir, "predl_ready.json")); err != nil {
			_ = os.Remove(filepath.Join(dir, "predl_ready.json"))
			return RecoveryState{Phase: RecoveryNone}
		}
		return RecoveryState{Phase: RecoveryPhasePredlAwaiting}

	case hasProgress:
		if _, err := loadProgressFile(filepath.Join(dir, "progress.json")); err != nil {
			_ = os.Remove(filepath.Join(dir, "progress.json"))
			return RecoveryState{Phase: RecoveryNone}
		}
		return RecoveryState{Phase: RecoveryPhaseDownloadResume}

	case hasPredl:
		if _, err := loadProgressFile(filepath.Join(dir, "predl_ready.json")); err != nil {
			_ = os.Remove(filepath.Join(dir, "predl_ready.json"))
			return RecoveryState{Phase: RecoveryNone}
		}
		return RecoveryState{Phase: RecoveryPhasePredlAwaiting}

	default:
		return RecoveryState{Phase: RecoveryNone}
	}
}

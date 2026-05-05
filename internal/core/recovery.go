package core

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

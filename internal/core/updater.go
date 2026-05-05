package core

import (
	"context"
	"fmt"
)

// Updater is an optional interface providers may implement to support
// game updates. M3.A: kurogames implements it. hoyoverse and hypergryph
// do NOT in M3.A; M3.B and M3.C add them respectively.
//
// App layer type-asserts at RPC entry: p, ok := provider.(core.Updater).
type Updater interface {
	// CheckForUpdate fetches the per-game manifest. Idempotent. Read-only.
	// Returns a populated UpdatePlan or an error. Plan.Files is filtered
	// to exclude entries whose hash matches the currently-installed file.
	CheckForUpdate(ctx context.Context, gid GameID) (UpdatePlan, error)

	// RunUpdate executes a previously-checked plan. Emits progress via
	// onEvent (synchronous callback, not channel). Returns nil on success,
	// ctx.Err() on cancel, or *UpdateError on terminal failure.
	//
	// Re-verifies plan.ManifestETag at entry; mismatch returns
	// *UpdateError{Code: "manifest_changed"}.
	//
	// Plan-Kind dispatch:
	//   PlanUpdate      → download phase + apply phase
	//   PlanPredownload → download phase only; renames progress.json to
	//                     predl_ready.json on completion
	RunUpdate(ctx context.Context, plan UpdatePlan, onEvent func(UpdateEvent)) error
}

// UpdatePlan describes the work needed to bring an installed game from
// its current version to the manifest's target version.
type UpdatePlan struct {
	GameID       GameID     // which game this plan is for
	Kind         PlanKind   // PlanUpdate | PlanPredownload
	ManifestETag string     // re-checked at RunUpdate entry; mismatch → ErrManifestChanged
	Version      string     // human-readable label, e.g. "3.4.0"
	Files        []FileTask // already filtered: only files whose hash differs from current install
	TotalBytes   int64      // sum of Files[].Size; used for download progress denominator
}

// FileTask is one file to download + apply during an update run.
//
// `Hash` is hex-encoded; the algorithm is provider-defined. Kurogames
// (the only Updater impl in M3.A) uses MD5 — see
// docs/superpowers/research/m3a-kuro-update-protocol.md. M3.B+ providers
// may use a different algorithm; verifiers MUST be paired with their
// provider's manifest source.
type FileTask struct {
	Path string // relative to game install dir, e.g. "Wuthering Waves Game/foo/bar.dll"
	Hash string // hex-encoded provider-specific hash (MD5 for kurogames)
	Size int64  // expected byte size
	URL  string // full CDN URL; sanitized before logging via sanitizeURL
}

// UpdateEvent is emitted by RunUpdate via the onEvent callback during
// download and apply phases. Throttled to ~8 Hz at the App layer for
// byte-progress; phase transitions / cancel / error / done bypass the
// throttle and emit synchronously.
type UpdateEvent struct {
	Phase       Phase  // PhaseDownload | PhaseApply
	Current     int64  // bytes done in PhaseDownload, file count applied in PhaseApply
	Total       int64  // TotalBytes (PhaseDownload) or len(plan.Files) (PhaseApply)
	CurrentFile string // optional: name of file currently being processed
}

// UpdateError is the structured error type returned by RunUpdate /
// CheckForUpdate. Transported to frontend via GameUpdateState.LastError
// in the snapshot returned by UpdateStatusAll RPC; frontend renders text
// via i18n key update.errors.<Code> with Params substitution. Go side
// never sends pre-rendered text.
type UpdateError struct {
	Code      string            // see spec §6.1 catalog
	Params    map[string]string // template substitution data; URLs sanitized
	Retryable bool              // true → toast shows retry button (frontend reads this flag)
}

func (e *UpdateError) Error() string {
	return fmt.Sprintf("%s: %v", e.Code, e.Params)
}

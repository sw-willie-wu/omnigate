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

// CheckForUpdateProgress is an optional interface a Provider may implement
// to surface progress during the heavy local-file MD5 verification step
// inside CheckForUpdate. App layer prefers this method when available so
// the BottomBar can show "驗證本地檔案 X / Y" instead of a frozen UI during
// the seconds-to-minutes verify phase.
//
// onProgress is called from filterChangedFiles after each local file has
// been hashed and compared. It receives (done, total) where total is the
// manifest's full file count and done is the number processed so far.
// Callbacks may be invoked from arbitrary goroutines and must be cheap.
type CheckForUpdateProgress interface {
	CheckForUpdateWithProgress(ctx context.Context, gid GameID, onProgress func(done, total int)) (UpdatePlan, error)
}

// PredownloadChecker is an optional interface a Provider may implement to
// support predownloading the next game version ahead of release. The App
// type-asserts it at the Refresh probe (to light the predl button) and at
// StartPredownload (to build the predl plan).
type PredownloadChecker interface {
	// SupportsPredownload is the cheap, PER-GAME capability predicate. One
	// provider type can serve several games whose predl support lands in
	// different phases (e.g. hoyoverse: Genshin then HSR/ZZZ), so capability
	// is gated per game, NOT by Go interface satisfaction.
	SupportsPredownload(gid GameID) bool
	// CheckForPredownload builds a predl plan targeting the predownload
	// manifest (Kind=PlanPredownload, Version=<predl target>). For any gid
	// where SupportsPredownload is false, or when no predl is currently
	// published, it returns ErrPredownloadUnsupported.
	CheckForPredownload(ctx context.Context, gid GameID, onProgress func(done, total int)) (UpdatePlan, error)
}

// ReasonCode identifies WHY an update plan was constructed. Used by frontend
// to render appropriate tooltips on the [Update] button. M3.B introduced.
type ReasonCode string

const (
	ReasonUnspecified       ReasonCode = ""
	ReasonVersionChanged    ReasonCode = "version_changed"
	ReasonAudioPackAdded    ReasonCode = "audio_pack_added"
	ReasonVersionAndAudio   ReasonCode = "version_and_audio"
	ReasonPredownload       ReasonCode = "predownload"
	ReasonResumeInterrupted ReasonCode = "resume_interrupted"
)

// UpdatePlan describes the work needed to bring an installed game from
// its current version to the manifest's target version.
type UpdatePlan struct {
	GameID       GameID     `json:"game_id"`       // which game this plan is for
	Kind         PlanKind   `json:"kind"`          // PlanUpdate | PlanPredownload
	ManifestETag string     `json:"manifest_etag"` // re-checked at RunUpdate entry; mismatch → ErrManifestChanged
	Version      string     `json:"version"`       // human-readable label, e.g. "3.4.0"
	Files        []FileTask `json:"files,omitempty"`
	TotalBytes   int64      `json:"total_bytes"` // sum of Files[].Size; used for download progress denominator
	// Reason identifies why this plan was constructed. Frontend renders
	// it as a tooltip on the [Update] button. Empty string (ReasonUnspecified)
	// is the M3.A-era zero value; frontend renders no tooltip in that case.
	Reason ReasonCode `json:"reason,omitempty"`
}

// FileTask is one file to download + apply during an update run.
//
// `Hash` is hex-encoded; the algorithm is provider-defined. Kurogames
// (the only Updater impl in M3.A) uses MD5 — see
// docs/superpowers/research/m3a-kuro-update-protocol.md. M3.B+ providers
// may use a different algorithm; verifiers MUST be paired with their
// provider's manifest source.
type FileTask struct {
	Path string `json:"path"` // relative to game install dir
	Hash string `json:"hash"` // hex-encoded provider-specific hash (MD5 for kurogames)
	Size int64  `json:"size"` // expected byte size
	URL  string `json:"url"`  // full CDN URL; sanitized before logging via sanitizeURL
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
	Stage       string // optional fine-grained stage (e.g. extracting/patching) for the UI label; "" → use Phase
}

// UpdateError is the structured error type returned by RunUpdate /
// CheckForUpdate. Transported to frontend via GameUpdateState.LastError
// in the snapshot returned by UpdateStatusAll RPC; frontend renders text
// via i18n key update.errors.<Code> with Params substitution. Go side
// never sends pre-rendered text.
type UpdateError struct {
	Code      string            `json:"code"`             // see spec §6.1 catalog
	Params    map[string]string `json:"params,omitempty"` // template substitution data; URLs sanitized
	Retryable bool              `json:"retryable"`        // true → toast shows retry button (frontend reads this flag)
}

func (e *UpdateError) Error() string {
	return fmt.Sprintf("%s: %v", e.Code, e.Params)
}

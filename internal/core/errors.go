package core

import "errors"

// Sentinel errors that the rest of the codebase wraps with %w. The frontend
// uses ErrorCode to map these to stable JSON-friendly codes.
var (
	ErrUnknownGame            = errors.New("unknown game id")
	ErrGameNotInstalled       = errors.New("game not installed")
	ErrBackendNotConfigured   = errors.New("backend not configured")
	ErrLauncherMissing        = errors.New("launcher folder not found")
	ErrAssetNotAvailable      = errors.New("asset not available")
	ErrGachaURLUnavailable    = errors.New("gacha history url unavailable")
	ErrPredownloadUnsupported = errors.New("predownload not supported for this game")
)

// ErrorCode returns a stable JSON-friendly code for the given error. The
// frontend uses this code to choose UX (CTA, retry, dim). Returns "internal"
// for nil or unrecognized errors.
func ErrorCode(err error) string {
	switch {
	case err == nil:
		return "internal"
	case errors.Is(err, ErrUnknownGame):
		return "unknown_game"
	case errors.Is(err, ErrGameNotInstalled):
		return "not_installed"
	case errors.Is(err, ErrBackendNotConfigured):
		return "not_configured"
	case errors.Is(err, ErrLauncherMissing):
		return "launcher_missing"
	case errors.Is(err, ErrAssetNotAvailable):
		return "asset_unavailable"
	case errors.Is(err, ErrGachaURLUnavailable):
		return "gacha_url"
	default:
		return "internal"
	}
}

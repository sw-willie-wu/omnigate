package core

// PathProvider is implemented by Providers that have a primary on-disk root
// (the directory the user configures in settings.toml). App's BackendStatus
// derivation uses PrimaryPath to detect "path_unset" / "launcher_missing".
//
// Optional interface — providers that don't have a path-based detection
// model (e.g. registry-only) simply do not implement this; status falls back
// to "ok" if DetectInstall returns games, "empty" otherwise.
type PathProvider interface {
	PrimaryPath() string // empty string when not configured
}

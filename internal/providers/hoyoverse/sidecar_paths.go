package hoyoverse

import (
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"omnigate/internal/core"
)

// flatGameID converts "<backend>/<localPart>" to "<backend>-<localPart>"
// (single replacement). Mirrors update_handler.go's flatten convention
// (strings.Replace with limit 1).
func flatGameID(gid core.GameID) string {
	return strings.Replace(string(gid), "/", "-", 1)
}

// gameSidecarDir returns the per-game sidecar root: <tempRoot>/<gid-flat>.
// Used by hoyoverse-local sidecars that persist across versions
// (e.g. last_apply_target.json).
func gameSidecarDir(tempRoot string, gid core.GameID) string {
	return filepath.Join(tempRoot, flatGameID(gid))
}

// versionSidecarDir returns the per-version sidecar dir:
// <tempRoot>/<gid-flat>/<version>. Used by sidecars scoped to a single
// update run (progress.json, apply.wal, extract_progress.json,
// predl_ready.json, staging/).
func versionSidecarDir(tempRoot string, gid core.GameID, version string) string {
	return filepath.Join(tempRoot, flatGameID(gid), version)
}

// sophonSubdir returns the cross-version Sophon sidecar root:
// <gameSidecarDir>/.sophon. The dot-prefix keeps App.scanForRecoveryRoot from
// misclassifying it as a version dir (see internal/app/update_handler.go skip
// guard). Holds manifests/ + applied.json (§4.1, §A.8).
func sophonSubdir(tempRoot string, gid core.GameID) string {
	return filepath.Join(gameSidecarDir(tempRoot, gid), ".sophon")
}

// sophonManifestsDir returns <…/.sophon>/manifests — raw <build_id>__<cat>.manifest.pb.zst blobs.
func sophonManifestsDir(tempRoot string, gid core.GameID) string {
	return filepath.Join(sophonSubdir(tempRoot, gid), "manifests")
}

// sophonAppliedJSONPath returns <…/.sophon>/applied.json — the applied-manifest index (§4.1).
func sophonAppliedJSONPath(tempRoot string, gid core.GameID) string {
	return filepath.Join(sophonSubdir(tempRoot, gid), "applied.json")
}

// sophonStagingDir returns the branch-split staging dir
// <versionSidecarDir>/staging/<branchKind>/<buildID> (branchKind ∈ {"main","predl"}, §A.8).
func sophonStagingDir(tempRoot string, gid core.GameID, version, branchKind, buildID string) string {
	return filepath.Join(versionSidecarDir(tempRoot, gid, version), "staging", branchKind, buildID)
}

// loadJSONSidecar reads and parses a JSON sidecar at path with corrupt-file
// recovery semantics:
//   - ENOENT (file absent) returns (nil, nil) — caller treats as no sidecar
//   - JSON parse error logs warn + os.Remove + returns (nil, nil) — corrupt
//     state heals itself on next write
//   - other I/O errors propagate as (nil, err)
//
// Used by all hoyoverse-local sidecars (last_apply_target.json,
// extract_progress.json, predl_ready.json) — see spec §2 "Field naming
// convention" + sidecar schema reference.
func loadJSONSidecar[T any](path string) (*T, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, err
	}
	var v T
	if err := json.Unmarshal(data, &v); err != nil {
		slog.Warn("hoyoverse: corrupt sidecar; deleting", "path", path, "err", err)
		_ = os.Remove(path)
		return nil, nil
	}
	return &v, nil
}

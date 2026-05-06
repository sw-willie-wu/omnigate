package hypergryph

import (
	"context"

	"omnigate/internal/core"
)

// fetchVersion: no clean local-FS version source surfaced during pre-spec
// research (Endfield.exe FileVersion is the Unity engine version
// 2021.3.34f5; Endfield_Data/app.info has only "Gryphline\nEndfield";
// GRYPHLINK's <root>/<x.y.z>/ folder is launcher-version, not game).
//
// M2 returns empty VersionInfo for hypergryph; sidebar shows "就緒" with no
// `· vX.Y` suffix. M3+ may add an API-based version check.
//
// During impl smoke: if a clean version source surfaces (Addressables build
// version under Endfield_Data/StreamingAssets/aa/, registry under
// HKCU\Software\Hypergryph\Endfield, GRYPHLINK launcher local API), wire
// it then; do NOT block on research per spec §3 risk note.
func fetchVersion(_ context.Context, _ string, _ core.GameID) (core.VersionInfo, error) {
	return core.VersionInfo{}, nil
}

package hoyoverse

import (
	"sync"

	"omnigate/internal/core"
	"omnigate/internal/providers/hoyoverse/sophon"
)

type planFlavor int

const (
	flavorNone      planFlavor = iota
	flavorPatch
	flavorFull
	flavorAudioOnly
	flavorPredlPatch
	flavorPredlFull

	flavorSophonPatch      // 6
	flavorSophonBuild      // 7
	flavorSophonFull       // 8
	flavorSophonPredlPatch // 9
	flavorSophonPredlBuild // 10
)

func (f planFlavor) String() string {
	switch f {
	case flavorNone:
		return "none"
	case flavorPatch:
		return "patch"
	case flavorFull:
		return "full"
	case flavorAudioOnly:
		return "audio_only"
	case flavorPredlPatch:
		return "predl_patch"
	case flavorPredlFull:
		return "predl_full"
	case flavorSophonPatch:
		return "sophon_patch"
	case flavorSophonBuild:
		return "sophon_build"
	case flavorSophonFull:
		return "sophon_full"
	case flavorSophonPredlPatch:
		return "sophon_predl_patch"
	case flavorSophonPredlBuild:
		return "sophon_predl_build"
	}
	return "unknown"
}

type genshinPlan struct {
	core.UpdatePlan
	flavor         planFlavor
	sourceVersion  string
	manifestETag   string
	audioLanguages []string
	predlAvailable bool

	// Sophon (M3.B v2) fields. Zero for HSR/ZZZ legacy plans.
	sophonBranch              *sophon.BranchInfo
	sophonBuildID             string
	sophonCategories          []sophon.Category
	sophonChunkSources        []sophon.ChunkSource
	sophonPatches             []sophon.PatchInstr
	sophonDeletes             []sophon.DeleteInstr
	sophonPatchAssetsFromMain map[string][]sophon.ChunkSource // assetPath → main-manifest chunk plan (§6.4 demotion)
	sophonAssetMD5            map[string]string               // §E item 5: assetPath → expected whole-file MD5 (AssetHashMd5); set by Task 18 for every asset that emits chunk_assemble sources, consumed by Task 20 to set chunk_assemble record AssetMD5
	sophonRawManifests        map[string][]byte               // §E.2 P11: category MatchingField → raw .pb.zst bytes of the NEW main manifest; populated by Task 18 (FetchManifestRaw), consumed by Task 20 finalize → SaveAppliedManifest (dedup-cache persist, BLOCKER-2 fix)
	predlConsume              bool
	predlSnapshot             *sophonPlanSnapshot // §E item 4: declared in THIS file (plan_internal.go), populated by Task 18; value field on sophonPredlReadyFile.PlanSnapshot
	predlPlan                 *predlPlanCache
}

type manifestCache struct {
	mu    sync.Mutex
	plans map[core.GameID]*genshinPlan
}

func newManifestCache() *manifestCache {
	return &manifestCache{plans: make(map[core.GameID]*genshinPlan)}
}

func (c *manifestCache) put(gid core.GameID, gp *genshinPlan) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.plans[gid] = gp
}

func (c *manifestCache) get(gid core.GameID) *genshinPlan {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.plans[gid]
}

// predlPlanCache is the in-memory parallel predl plan captured at
// CheckForUpdate time (§3.3). Serialized into predl_ready.json.PlanSnapshot
// when the predl run completes (§7.1). Lives only on genshinPlan.predlPlan.
type predlPlanCache struct {
	Flavor         planFlavor
	BuildID        string
	SourceVersion  string
	TargetVersion  string
	AudioLanguages []string
	ChunkSources   []sophon.ChunkSource
	Patches        []sophon.PatchInstr
	Deletes        []sophon.DeleteInstr
	Categories     []sophon.Category
}

// sophonPlanSnapshot is the predl snapshot persisted inside predl_ready.json
// (§A.6 / §7.1). The sophon.* element types carry no json tags, so they
// serialize as exported PascalCase fields — acceptable because this snapshot
// is private to the hoyoverse package. Do NOT add json tags to the sophon
// types (§A.6 note). Consumed by detectPredlConsume / RunUpdate (Task 18/21).
type sophonPlanSnapshot struct {
	SophonChunkSources []sophon.ChunkSource `json:"sophon_chunk_sources"`
	SophonPatches      []sophon.PatchInstr  `json:"sophon_patches"`
	SophonDeletes      []sophon.DeleteInstr `json:"sophon_deletes"`
	Categories         []sophon.Category    `json:"categories"`
}

// sophonPredlReadyFile is the predl_ready.json sidecar for a staged Sophon
// predownload (§7.1). It embeds the v1 core.ProgressFile base (Entries stays
// empty for Sophon — chunk progress lives in sophon_progress.json) so the v1
// recovery readers keep working, and adds the Sophon predl fields. Read by
// detectPredlConsume (Task 18); written by the predl path in RunUpdate (Task 21).
type sophonPredlReadyFile struct {
	core.ProgressFile                    // v1 base; Entries empty for Sophon
	Kind           string             `json:"kind"`            // "sophon_patch" | "sophon_build"
	BuildID        string             `json:"build_id"`
	SourceVersion  string             `json:"source_version"`
	TargetVersion  string             `json:"target_version"`
	AudioLanguages []string           `json:"audio_languages"`
	StagedAt       string             `json:"staged_at"`       // RFC3339
	PlanSnapshot   sophonPlanSnapshot `json:"plan_snapshot"`   // value, not pointer (T21-B)
}

// planFlavorFromString maps a flavor String() value back to its planFlavor.
// Used by Task 21 to reconstruct flavor from a predl Kind ("sophon_patch"/
// "sophon_build") or a resumed WAL Flavor (§E item 7 / §E.2 P6). Unknown → flavorNone.
func planFlavorFromString(s string) planFlavor {
	switch s {
	case "sophon_patch":
		return flavorSophonPatch
	case "sophon_build":
		return flavorSophonBuild
	case "sophon_full":
		return flavorSophonFull
	case "sophon_predl_patch":
		return flavorSophonPredlPatch
	case "sophon_predl_build":
		return flavorSophonPredlBuild
	default:
		return flavorNone
	}
}

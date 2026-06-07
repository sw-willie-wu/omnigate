# Predownload Phase 3 (HSR/ZZZ legacy) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add predownload for HSR/ZZZ (the legacy `getGamePackages` hoyoverse games), including the legacy predl APPLY path that does not exist yet (spec §2.2/B2).

**Architecture:** Legacy predl mirrors the legacy update path: `buildPredlPlan` builds a plan from `entry.PreDownload` (`flavorPredlPatch`/`flavorPredlFull`); the download phase stages the blobs + records the flavor in `predl_ready.json`'s `planSnapshot`; on apply, `loadLegacyPredlConsume` restores `progress.json` from the staged `predl_ready.json` and rebuilds `gp` so the normal apply path reuses the staged zips (works for both warm cache same-session and cold cache after restart). The apply switch gains `flavorPredlPatch`/`flavorPredlFull` cases that behave identically to `flavorPatch`/`flavorFull`.

**Tech Stack:** Go 1.26 (`internal/providers/hoyoverse`). All new logic is unit-testable without network (responses constructed inline; consume tested via on-disk sidecars). `export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"`; no `-race` (CGO off).

**Scope:** Phase 3 only (HSR/ZZZ legacy). Spec: `docs/superpowers/specs/2026-06-07-omnigate-predownload-wiring-design.md` §2.2, §9 Phase 3. Phase 1 (kuro) + Phase 2 (Genshin Sophon) already shipped to dev (`fbb4a72`); the shared infra (`core.PredownloadChecker`, App probe, BottomBar) is in place. Genshin/Sophon predl is unaffected by this plan.

---

## Key existing facts (verified)

- `HypGameEntry.PreDownload` is a `*HypGamePackagesMain` with `.Major HypPackageInfo` and `.Patches []HypPackageInfo` — identical shape to `.Main` (`version.go:43-51`). So `buildPredlPlan` mirrors `buildPlan` sourced from `PreDownload`.
- `planFlavor` already defines `flavorPredlPatch` (4) and `flavorPredlFull` (5) (`plan_internal.go:17-18`), currently unused; `planFlavor.String()` returns `"predl_patch"`/`"predl_full"` (`plan_internal.go:37-40`).
- Legacy `RunUpdate` (`hoyoverse.go:388-491`): sophon dispatch (`isSophonFlavor`) → wal/extractProgress apply-resume → `if gp == nil { error }` (line 447-449) → `newProgressStore` → `downloadAll` → `if plan.Kind == PlanPredownload { RenameToPredlReady }` (464-473) → apply `switch gp.flavor` (475-490; handles `flavorPatch/flavorAudioOnly/flavorFull`, else `default: unsupported flavor`).
- `planSnapshot` (`update_progress.go:14-20`): SourceVersion/TargetVersion/Files/AudioLanguages/ManifestETag — **no Flavor**. `RenameToPredlReady(snap planSnapshot)` writes `predl_ready.json` (embeds `core.ProgressFile` + `PlanSnapshot`) and removes `progress.json`.
- `ApplyPredownload` (`update_handler.go`) flips `Kind→PlanUpdate` and calls `runUpdateWorker → RunUpdate` directly (no CheckForUpdate) — so apply hits a cold `manifestCache` after restart.
- `runPreflight(gp, tempRoot, gameDir, probe, predlAvailable) (*genshinPlan, bool, error)`; `freeSpaceProbe` = interface with `FreeBytes(path) (uint64, error)`; test stub `stubFreeSpace{bytes}` (`update_manifest_test.go:46-48`).
- `downloadAll` skips a file when `pf.Entries[path].Hash==task.Hash && .Size==task.Size` (`update_download.go:43`) — so restoring `progress.json` from the staged snapshot makes it skip already-staged blobs.

---

## File Structure

- `internal/providers/hoyoverse/update_progress.go` — add `Flavor` to `planSnapshot` (Task 1).
- `internal/providers/hoyoverse/hoyoverse.go` — write `Flavor` in the predl download branch (Task 1); `SupportsPredownload`→all + `checkForPredownloadLegacy` + `CheckForPredownload` legacy branch (Task 3); apply-switch predl cases + `loadLegacyPredlConsume` + RunUpdate consume block (Task 4).
- `internal/providers/hoyoverse/update_manifest.go` — `buildPredlPlan` (Task 2).
- `internal/providers/hoyoverse/predl_legacy_test.go` (NEW, plain) — all Phase 3 unit tests (Tasks 1-4).

---

## Task 1: planSnapshot.Flavor field + record it on predl staging

**Files:**
- Modify: `internal/providers/hoyoverse/update_progress.go:14-20` (`planSnapshot`)
- Modify: `internal/providers/hoyoverse/hoyoverse.go:464-472` (predl download branch)
- Test: `internal/providers/hoyoverse/predl_legacy_test.go` (NEW)

- [ ] **Step 1: Write the failing test**

Create `internal/providers/hoyoverse/predl_legacy_test.go`:

```go
package hoyoverse

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"omnigate/internal/core"
)

func TestPlanSnapshot_FlavorRoundTrip(t *testing.T) {
	tempRoot := t.TempDir()
	gid := core.GameID("hoyoverse/starrail")
	ps, err := newProgressStore(tempRoot, gid, "4.4.0", "etag-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.MarkComplete("game.zip", 10, time.Now(), "abc"); err != nil {
		t.Fatal(err)
	}
	snap := planSnapshot{
		SourceVersion: "4.3.0", TargetVersion: "4.4.0",
		Files:  []core.FileTask{{Path: "game.zip", Hash: "abc", Size: 10}},
		Flavor: flavorPredlPatch.String(),
	}
	if err := ps.RenameToPredlReady(snap); err != nil {
		t.Fatal(err)
	}
	prf, err := loadJSONSidecar[predlReadyFile](filepath.Join(versionSidecarDir(tempRoot, gid, "4.4.0"), "predl_ready.json"))
	if err != nil {
		t.Fatal(err)
	}
	if prf == nil {
		t.Fatal("predl_ready.json not found")
	}
	if prf.PlanSnapshot.Flavor != "predl_patch" {
		t.Errorf("Flavor = %q, want predl_patch", prf.PlanSnapshot.Flavor)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/providers/hoyoverse/ -run TestPlanSnapshot_FlavorRoundTrip`
Expected: FAIL — compile error: `planSnapshot` has no field `Flavor`.

- [ ] **Step 3: Add the Flavor field**

In `internal/providers/hoyoverse/update_progress.go`, add to `planSnapshot` (after `ManifestETag`):

```go
type planSnapshot struct {
	SourceVersion  string          `json:"source_version"`
	TargetVersion  string          `json:"target_version"`
	Files          []core.FileTask `json:"files"`
	AudioLanguages []string        `json:"audio_languages"`
	ManifestETag   string          `json:"manifest_etag"`
	Flavor         string          `json:"flavor,omitempty"` // planFlavor.String() of the predl flavor (predl_patch/predl_full); read back on apply to pick the apply path
}
```

- [ ] **Step 4: Record the flavor when staging a predl**

In `internal/providers/hoyoverse/hoyoverse.go`, the predl download branch (currently lines 464-472) — add `Flavor: gp.flavor.String()`:

```go
	if plan.Kind == core.PlanPredownload {
		snap := planSnapshot{
			SourceVersion:  gp.sourceVersion,
			TargetVersion:  plan.Version,
			Files:          plan.Files,
			AudioLanguages: gp.audioLanguages,
			ManifestETag:   plan.ManifestETag,
			Flavor:         gp.flavor.String(),
		}
		return ps.RenameToPredlReady(snap)
	}
```

- [ ] **Step 5: Run test to verify it passes**

Run: `go test ./internal/providers/hoyoverse/ -run TestPlanSnapshot_FlavorRoundTrip`
Expected: PASS. Then `go build ./...` clean.

- [ ] **Step 6: Commit** (deferred until the task's review gate returns APPROVE)

```bash
git add internal/providers/hoyoverse/update_progress.go internal/providers/hoyoverse/hoyoverse.go internal/providers/hoyoverse/predl_legacy_test.go
git commit -m "feat(predl): planSnapshot.Flavor — record predl flavor in predl_ready.json (legacy)"
```

---

## Task 2: buildPredlPlan (legacy plan from entry.PreDownload)

**Files:**
- Modify: `internal/providers/hoyoverse/update_manifest.go` (add `buildPredlPlan` after `runPreflight`, ~line 215)
- Test: `internal/providers/hoyoverse/predl_legacy_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/providers/hoyoverse/predl_legacy_test.go`:

```go
func predlResp(currentInPatches bool) *HypGetGamePackagesResponse {
	predl := &HypGamePackagesMain{
		Major: HypPackageInfo{
			Version:  "4.4.0",
			GamePkgs: []HypPackageData{{URL: "https://cdn.example/full_4.4.0.zip", MD5: "fullmd5", Size: 100}},
		},
	}
	if currentInPatches {
		predl.Patches = []HypPackageInfo{{
			Version:  "4.3.0",
			GamePkgs: []HypPackageData{{URL: "https://cdn.example/patch_4.3.0_to_4.4.0.zip", MD5: "patchmd5", Size: 50}},
		}}
	}
	resp := &HypGetGamePackagesResponse{ManifestETag: "etag-x"}
	resp.Data.GamePackages = []HypGameEntry{{
		Main:        HypGamePackagesMain{Major: HypPackageInfo{Version: "4.3.0"}},
		PreDownload: predl,
	}}
	return resp
}

func TestBuildPredlPlan_PatchFlavor(t *testing.T) {
	gp, err := buildPredlPlan(context.Background(), predlResp(true), core.GameID("hoyoverse/starrail"), "4.3.0", t.TempDir(), t.TempDir(), &stubFreeSpace{bytes: 100 * 1024 * 1024 * 1024})
	if err != nil {
		t.Fatal(err)
	}
	if gp == nil {
		t.Fatal("expected a predl plan")
	}
	if gp.flavor != flavorPredlPatch {
		t.Errorf("flavor = %v, want flavorPredlPatch", gp.flavor)
	}
	if gp.UpdatePlan.Kind != core.PlanPredownload || gp.UpdatePlan.Version != "4.4.0" {
		t.Errorf("plan = {Kind:%v Version:%q}, want PlanPredownload @ 4.4.0", gp.UpdatePlan.Kind, gp.UpdatePlan.Version)
	}
	if len(gp.UpdatePlan.Files) != 1 || gp.UpdatePlan.Files[0].Path != "patch_4.3.0_to_4.4.0.zip" {
		t.Fatalf("Files = %+v, want the patch zip", gp.UpdatePlan.Files)
	}
	if gp.sourceVersion != "4.3.0" {
		t.Errorf("sourceVersion = %q, want 4.3.0", gp.sourceVersion)
	}
}

func TestBuildPredlPlan_FullFlavor(t *testing.T) {
	// currentVer 4.0.0 is NOT in predl patches → full predl.
	gp, err := buildPredlPlan(context.Background(), predlResp(false), core.GameID("hoyoverse/starrail"), "4.0.0", t.TempDir(), t.TempDir(), &stubFreeSpace{bytes: 100 * 1024 * 1024 * 1024})
	if err != nil {
		t.Fatal(err)
	}
	if gp == nil || gp.flavor != flavorPredlFull {
		t.Fatalf("flavor = %v, want flavorPredlFull", gp.flavor)
	}
	if len(gp.UpdatePlan.Files) != 1 || gp.UpdatePlan.Files[0].Path != "full_4.4.0.zip" {
		t.Fatalf("Files = %+v, want the full zip", gp.UpdatePlan.Files)
	}
}

func TestBuildPredlPlan_NoPredl(t *testing.T) {
	resp := &HypGetGamePackagesResponse{ManifestETag: "etag-x"}
	resp.Data.GamePackages = []HypGameEntry{{Main: HypGamePackagesMain{Major: HypPackageInfo{Version: "4.3.0"}}}} // PreDownload nil
	gp, err := buildPredlPlan(context.Background(), resp, core.GameID("hoyoverse/starrail"), "4.3.0", t.TempDir(), t.TempDir(), &stubFreeSpace{bytes: 1 << 40})
	if err != nil {
		t.Fatalf("buildPredlPlan: %v", err)
	}
	if gp != nil {
		t.Fatalf("expected nil plan when no predownload published, got %+v", gp)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/providers/hoyoverse/ -run TestBuildPredlPlan`
Expected: FAIL — `buildPredlPlan` undefined.

- [ ] **Step 3: Implement buildPredlPlan**

In `internal/providers/hoyoverse/update_manifest.go`, after `runPreflight` (after line 215):

```go
// buildPredlPlan builds a predownload plan from entry.PreDownload (the next
// version), mirroring buildPlan's patch-vs-full decision sourced from PreDownload
// instead of Main. Returns (nil, nil) when no actionable predownload is published
// (no PreDownload, or its version equals the installed version). Flavors are
// flavorPredlPatch / flavorPredlFull; Kind is PlanPredownload.
func buildPredlPlan(
	ctx context.Context,
	resp *HypGetGamePackagesResponse,
	gid core.GameID,
	currentVer string,
	tempRoot string,
	gameDir string,
	probe freeSpaceProbe,
) (*genshinPlan, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if len(resp.Data.GamePackages) == 0 {
		return nil, fmt.Errorf("manifest has no game_packages")
	}
	entry := resp.Data.GamePackages[0]
	if entry.PreDownload == nil {
		return nil, nil
	}
	predlMajor := entry.PreDownload.Major
	if predlMajor.Version == "" || predlMajor.Version == currentVer {
		return nil, nil
	}

	gp := &genshinPlan{
		UpdatePlan: core.UpdatePlan{
			GameID:       gid,
			Kind:         core.PlanPredownload,
			ManifestETag: resp.ManifestETag,
			Version:      predlMajor.Version,
			Reason:       core.ReasonPredownload,
		},
		manifestETag:  resp.ManifestETag,
		sourceVersion: currentVer,
	}

	installedFolders, err := DetectInstalledLanguages(gameDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("detect audio langs: %w", err)
	}
	installedAPI := audioLanguageIntersect(&predlMajor, installedFolders)
	gp.audioLanguages = append([]string{}, installedAPI...)

	appendPkgs := func(info *HypPackageInfo) {
		gp.UpdatePlan.Files = make([]core.FileTask, 0, len(info.GamePkgs)+len(info.AudioPkgs))
		for _, pk := range info.GamePkgs {
			gp.UpdatePlan.Files = append(gp.UpdatePlan.Files, core.FileTask{URL: pk.URL, Hash: pk.MD5, Size: pk.Size, Path: filepath.Base(pk.URL)})
		}
		for _, pk := range info.AudioPkgs {
			if !contains(installedAPI, pk.Language) {
				continue
			}
			gp.UpdatePlan.Files = append(gp.UpdatePlan.Files, core.FileTask{URL: pk.URL, Hash: pk.MD5, Size: pk.Size, Path: filepath.Base(pk.URL)})
		}
	}

	for i := range entry.PreDownload.Patches {
		patch := entry.PreDownload.Patches[i]
		if patch.Version == currentVer {
			gp.flavor = flavorPredlPatch
			appendPkgs(&patch)
			out, _, perr := runPreflight(gp, tempRoot, gameDir, probe, false)
			return out, perr
		}
	}

	gp.flavor = flavorPredlFull
	appendPkgs(&predlMajor)
	out, _, perr := runPreflight(gp, tempRoot, gameDir, probe, false)
	return out, perr
}
```

(All helpers used — `contains`, `audioLanguageIntersect`, `runPreflight`, `DetectInstalledLanguages`, `fs`, `errors`, `fmt`, `filepath` — are already imported/defined in `update_manifest.go`, mirroring `buildPlan`.)

- [ ] **Step 4: Run tests to verify they pass**

Run: `go test ./internal/providers/hoyoverse/ -run TestBuildPredlPlan`
Expected: PASS (all three).

- [ ] **Step 5: Commit** (deferred until review APPROVE)

```bash
git add internal/providers/hoyoverse/update_manifest.go internal/providers/hoyoverse/predl_legacy_test.go
git commit -m "feat(predl): buildPredlPlan — legacy predl plan from entry.PreDownload"
```

---

## Task 3: checkForPredownloadLegacy + wire CheckForPredownload + SupportsPredownload(all)

**Files:**
- Modify: `internal/providers/hoyoverse/hoyoverse.go` (`SupportsPredownload`, `CheckForPredownload`, add `checkForPredownloadLegacy`)
- Test: `internal/providers/hoyoverse/predl_legacy_test.go`

- [ ] **Step 1: Write the failing test**

Append to `internal/providers/hoyoverse/predl_legacy_test.go`:

```go
func TestSupportsPredownload_IncludesLegacyAfterPhase3(t *testing.T) {
	p := New(Settings{}, nil)
	for _, gid := range []string{"hoyoverse/genshin", "hoyoverse/starrail", "hoyoverse/zzz"} {
		if !p.SupportsPredownload(core.GameID(gid)) {
			t.Errorf("%s must support predownload after Phase 3", gid)
		}
	}
	if p.SupportsPredownload(core.GameID("hoyoverse/unknown")) {
		t.Error("unknown game must not support predownload")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./internal/providers/hoyoverse/ -run TestSupportsPredownload_IncludesLegacyAfterPhase3`
Expected: FAIL — `SupportsPredownload` returns false for starrail/zzz (currently `g.UsesSophon`).

- [ ] **Step 3: Update SupportsPredownload + wire the legacy CheckForPredownload branch**

In `internal/providers/hoyoverse/hoyoverse.go`, change `SupportsPredownload` (currently `return g != nil && g.UsesSophon`) to all known games:

```go
func (p *Provider) SupportsPredownload(gid core.GameID) bool {
	return findByID(gid) != nil
}
```

In `CheckForPredownload`, replace the legacy branch (currently `if !g.UsesSophon { return core.UpdatePlan{}, core.ErrPredownloadUnsupported }`) to route to the new legacy path:

```go
func (p *Provider) CheckForPredownload(ctx context.Context, gid core.GameID, onProgress func(done, total int)) (core.UpdatePlan, error) {
	g := findByID(gid)
	if g == nil {
		return core.UpdatePlan{}, fmt.Errorf("%w: %s", core.ErrUnknownGame, gid)
	}
	if g.UsesSophon {
		return p.checkForPredownloadSophon(ctx, gid, onProgress)
	}
	return p.checkForPredownloadLegacy(ctx, gid)
}
```

Add `checkForPredownloadLegacy` (place it right after `checkForPredownloadSophon`, before `LastApplyTarget`):

```go
// checkForPredownloadLegacy builds a predl plan for HSR/ZZZ via getGamePackages
// (entry.PreDownload) and caches it so RunUpdate(PlanPredownload) can stage it.
// Returns ErrPredownloadUnsupported when no predownload is published.
func (p *Provider) checkForPredownloadLegacy(ctx context.Context, gid core.GameID) (core.UpdatePlan, error) {
	resp, err := p.fetchGetGamePackages(ctx, gid)
	if err != nil {
		return core.UpdatePlan{}, err
	}
	gameDir, err := p.gameDir(gid)
	if err != nil {
		return core.UpdatePlan{}, err
	}
	tempRoot := p.tempRoot(gid)
	currentVer, _ := ReadGameVersion(gameDir)

	gp, err := buildPredlPlan(ctx, resp, gid, currentVer, tempRoot, gameDir, p.freeSpaceProbe())
	if err != nil {
		return core.UpdatePlan{}, err
	}
	if gp == nil {
		return core.UpdatePlan{}, core.ErrPredownloadUnsupported
	}
	gp.predlAvailable = true
	p.manifestCache.put(gid, gp)
	return gp.UpdatePlan, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./internal/providers/hoyoverse/ -run 'TestSupportsPredownload|TestBuildPredlPlan|TestPlanSnapshot'`
Expected: PASS. `go build ./...` clean.

> **Note — delete TWO superseded Phase-2 tests in `internal/providers/hoyoverse/predownload_test.go`** (both encode the old "legacy unsupported" contract that Phase 3 reverses):
> 1. `TestSupportsPredownload_SophonGamesOnly` — asserted starrail/zzz return **false**; superseded by `TestSupportsPredownload_IncludesLegacyAfterPhase3` (Task 3 Step 1). Delete it.
> 2. `TestCheckForPredownload_LegacyUnsupported` — asserted `CheckForPredownload(starrail) → ErrPredownloadUnsupported`. After this task that call routes to `checkForPredownloadLegacy → fetchGetGamePackages`, which on a stub-less `New(Settings{},nil)` provider would issue a **real network GET** (hang/flake) and no longer returns `ErrPredownloadUnsupported`. Delete it (legacy predl is now supported; the "no predownload published" path is covered by `TestBuildPredlPlan_NoPredl`).
>
> Those are the ONLY two tests in `predownload_test.go` (Phase 2), so **delete the whole file** `internal/providers/hoyoverse/predownload_test.go` (leaving it with no tests would orphan its imports → compile error). The replacement coverage lives in `predl_legacy_test.go` (`TestSupportsPredownload_IncludesLegacyAfterPhase3` + `TestBuildPredlPlan_NoPredl`). Do NOT keep a renamed copy of either deleted test.

Run: `git rm internal/providers/hoyoverse/predownload_test.go`

- [ ] **Step 5: Commit** (deferred until review APPROVE)

```bash
git rm internal/providers/hoyoverse/predownload_test.go
git add internal/providers/hoyoverse/hoyoverse.go internal/providers/hoyoverse/predl_legacy_test.go
git commit -m "feat(predl): legacy CheckForPredownload + SupportsPredownload covers HSR/ZZZ"
```

---

## Task 4: legacy predl apply — consume reconstruction + apply-switch cases

**Files:**
- Modify: `internal/providers/hoyoverse/hoyoverse.go` (RunUpdate consume block + apply-switch cases; add `loadLegacyPredlConsume`)
- Test: `internal/providers/hoyoverse/predl_legacy_test.go`

- [ ] **Step 1: Write the failing tests**

Append to `internal/providers/hoyoverse/predl_legacy_test.go`:

```go
func stageLegacyPredl(t *testing.T, tempRoot string, gid core.GameID, version, flavor string) string {
	t.Helper()
	ps, err := newProgressStore(tempRoot, gid, version, "etag-1")
	if err != nil {
		t.Fatal(err)
	}
	if err := ps.MarkComplete("patch.zip", 50, time.Now(), "abc"); err != nil {
		t.Fatal(err)
	}
	snap := planSnapshot{
		SourceVersion: "4.3.0", TargetVersion: version,
		Files:        []core.FileTask{{Path: "patch.zip", Hash: "abc", Size: 50}},
		ManifestETag: "etag-1", Flavor: flavor,
	}
	if err := ps.RenameToPredlReady(snap); err != nil {
		t.Fatal(err)
	}
	return versionSidecarDir(tempRoot, gid, version)
}

func TestLoadLegacyPredlConsume_RestoresAndRebuilds(t *testing.T) {
	tempRoot := t.TempDir()
	gid := core.GameID("hoyoverse/starrail")
	versionDir := stageLegacyPredl(t, tempRoot, gid, "4.4.0", flavorPredlPatch.String())

	gp, err := loadLegacyPredlConsume(versionDir, "4.4.0")
	if err != nil {
		t.Fatal(err)
	}
	if gp == nil {
		t.Fatal("expected a consume plan")
	}
	if gp.flavor != flavorPredlPatch {
		t.Errorf("flavor = %v, want flavorPredlPatch", gp.flavor)
	}
	if gp.UpdatePlan.Kind != core.PlanUpdate {
		t.Errorf("Kind = %v, want PlanUpdate (apply)", gp.UpdatePlan.Kind)
	}
	if len(gp.UpdatePlan.Files) != 1 || gp.UpdatePlan.Files[0].Path != "patch.zip" {
		t.Fatalf("Files = %+v", gp.UpdatePlan.Files)
	}
	// progress.json restored (so downloadAll skips the staged blob), predl_ready.json removed.
	if _, err := os.Stat(filepath.Join(versionDir, "progress.json")); err != nil {
		t.Errorf("progress.json should be restored: %v", err)
	}
	if _, err := os.Stat(filepath.Join(versionDir, "predl_ready.json")); !os.IsNotExist(err) {
		t.Errorf("predl_ready.json should be removed, stat err = %v", err)
	}
	restored, err := core.LoadProgressFromPath(filepath.Join(versionDir, "progress.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := restored.Entries["patch.zip"]; !ok {
		t.Error("restored progress.json missing the staged entry")
	}
}

func TestLoadLegacyPredlConsume_VersionMismatch(t *testing.T) {
	tempRoot := t.TempDir()
	gid := core.GameID("hoyoverse/starrail")
	versionDir := stageLegacyPredl(t, tempRoot, gid, "4.4.0", flavorPredlFull.String())
	gp, err := loadLegacyPredlConsume(versionDir, "9.9.9") // different target
	if err != nil {
		t.Fatal(err)
	}
	if gp != nil {
		t.Error("expected nil when the staged predl targets a different version")
	}
	// predl_ready.json must be left intact on mismatch.
	if _, err := os.Stat(filepath.Join(versionDir, "predl_ready.json")); err != nil {
		t.Errorf("predl_ready.json must be preserved on mismatch: %v", err)
	}
}

func TestLoadLegacyPredlConsume_NoStaged(t *testing.T) {
	gp, err := loadLegacyPredlConsume(t.TempDir(), "4.4.0")
	if err != nil || gp != nil {
		t.Fatalf("expected (nil,nil) when nothing staged; got gp=%v err=%v", gp, err)
	}
}
```

- [ ] **Step 2: Run tests to verify they fail**

Run: `go test ./internal/providers/hoyoverse/ -run TestLoadLegacyPredlConsume`
Expected: FAIL — `loadLegacyPredlConsume` undefined.

- [ ] **Step 3: Add loadLegacyPredlConsume**

In `internal/providers/hoyoverse/hoyoverse.go`, add (near `RunUpdate`, e.g. just before it):

```go
// loadLegacyPredlConsume detects a staged legacy predl for `version`. When the
// staged predl_ready.json targets `version`, it rebuilds the apply plan from the
// snapshot, restores progress.json from the embedded ProgressFile (so downloadAll
// skips the already-staged zips), and removes predl_ready.json (the inverse of
// RenameToPredlReady). Returns (nil, nil) when nothing consumable is staged.
func loadLegacyPredlConsume(versionDir, version string) (*genshinPlan, error) {
	prf, err := loadJSONSidecar[predlReadyFile](filepath.Join(versionDir, "predl_ready.json"))
	if err != nil || prf == nil {
		return nil, nil
	}
	if prf.PlanSnapshot.TargetVersion != version {
		return nil, nil
	}
	flavor := flavorPredlFull
	if prf.PlanSnapshot.Flavor == flavorPredlPatch.String() {
		flavor = flavorPredlPatch
	}

	// Restore progress.json from the embedded ProgressFile, then drop predl_ready.json.
	pf := prf.ProgressFile
	data, err := json.MarshalIndent(&pf, "", "  ")
	if err != nil {
		return nil, err
	}
	progPath := filepath.Join(versionDir, "progress.json")
	tmp := progPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return nil, err
	}
	if err := os.Rename(tmp, progPath); err != nil {
		_ = os.Remove(tmp)
		return nil, err
	}
	_ = os.Remove(filepath.Join(versionDir, "predl_ready.json"))

	return &genshinPlan{
		UpdatePlan: core.UpdatePlan{
			GameID:       core.GameID(pf.GameID),
			Kind:         core.PlanUpdate,
			Version:      version,
			ManifestETag: prf.PlanSnapshot.ManifestETag,
			Files:        prf.PlanSnapshot.Files,
			Reason:       core.ReasonResumeInterrupted,
		},
		flavor:         flavor,
		manifestETag:   prf.PlanSnapshot.ManifestETag,
		sourceVersion:  prf.PlanSnapshot.SourceVersion,
		audioLanguages: prf.PlanSnapshot.AudioLanguages,
	}, nil
}
```

- [ ] **Step 4: Wire the consume block into RunUpdate + add apply-switch cases**

In `internal/providers/hoyoverse/hoyoverse.go` `RunUpdate`, replace the cold-cache guard (currently lines 447-449):

```go
	if gp == nil {
		return fmt.Errorf("RunUpdate called without prior CheckForUpdate; manifestCache miss")
	}
```

with the consume block (runs for any apply — warm or cold — because RenameToPredlReady removed progress.json, so even a warm gp would otherwise re-download):

```go
	// Legacy predl-consume: applying a version that has a staged predl_ready.json
	// means the package blobs are already downloaded. Restore progress.json +
	// rebuild gp from the snapshot so the normal apply path reuses the staged
	// zips. Runs regardless of manifestCache warmth.
	if plan.Kind == core.PlanUpdate {
		consumed, cerr := loadLegacyPredlConsume(versionDir, plan.Version)
		if cerr != nil {
			return cerr
		}
		if consumed != nil {
			gp = consumed
			plan.Files = consumed.UpdatePlan.Files
			plan.ManifestETag = consumed.manifestETag
		}
	}
	if gp == nil {
		return fmt.Errorf("RunUpdate called without prior CheckForUpdate; manifestCache miss")
	}
```

Then extend the apply `switch gp.flavor` (currently lines 475-490) so the predl flavors apply identically to their normal counterparts:

```go
	switch gp.flavor {
	case flavorPatch, flavorAudioOnly, flavorPredlPatch:
		stagingDir := filepath.Join(versionDir, "staging")
		_ = os.RemoveAll(stagingDir)
		for _, blob := range plan.Files {
			zipPath := filepath.Join(versionDir, blob.Path)
			if err := applyPatchZip(ctx, zipPath, gameDir, stagingDir, emit); err != nil {
				return err
			}
		}
		return runApplyPlanPatch(ctx, tempRoot, gameDir, gid, plan.Version, false, plan.ManifestETag, emit)
	case flavorFull, flavorPredlFull:
		return runApplyPlanFull(ctx, tempRoot, gameDir, gid, plan.Version, plan.ManifestETag, plan.Files, emit)
	default:
		return fmt.Errorf("unsupported flavor: %v", gp.flavor)
	}
```

(`wasPredl` stays `false`: the legacy path keys no predl-staging cleanup on it, and from apply onward this is a plain apply to the target version. The consume already removed predl_ready.json.)

- [ ] **Step 5: Run tests to verify they pass**

Run: `go test ./internal/providers/hoyoverse/ -run TestLoadLegacyPredlConsume`
Expected: PASS (all three). Then the whole package: `go test ./internal/providers/hoyoverse/` → PASS. `go build ./...` clean.

- [ ] **Step 6: Commit** (deferred until review APPROVE)

```bash
git add internal/providers/hoyoverse/hoyoverse.go internal/providers/hoyoverse/predl_legacy_test.go
git commit -m "feat(predl): legacy predl apply — consume reconstruction + apply-switch predl cases"
```

---

## Final verification (after all tasks committed)

- [ ] `go vet ./...` — clean
- [ ] `go test ./...` — green (untagged)
- [ ] `go test -tags integration ./internal/providers/hoyoverse/` — the 4 Phase-2 predl tests still pass; note the 2 PRE-EXISTING integration failures (`TestSophonResume_ApplyWALMidFlight`, `TestSophonPredl_PartialStaleStagingThresholdRecover`) which already fail on dev — not introduced here.
- [ ] `cd frontend && npm test` — vitest still green (no frontend change in Phase 3; the BottomBar/SidebarRow predl UI from Phase 1/2 already covers HSR/ZZZ once SupportsPredownload returns true).
- [ ] `wails build` — produces `build/bin/omnigate.exe`
- [ ] **USER smoke (Phase 3):** on a real HSR or ZZZ install during an open predl window — Refresh → predl button by the per-game gear → click → download/stage → 套用 (or, on patch day, the update button consumes the staged predl) → applies, version writeback, game launches.

---

## Self-review notes (author)

- **Spec coverage (§2.2):** buildPredlPlan from entry.PreDownload → Task 2; planSnapshot.Flavor → Task 1; legacy predl-consume reconstruction on cold-cache apply → Task 4 (`loadLegacyPredlConsume`); apply-switch predl cases → Task 4; SupportsPredownload covers HSR/ZZZ + CheckForPredownload legacy → Task 3. §9 Phase-3 row covered.
- **Warm vs cold cache (the B2 subtlety):** the consume block runs for ALL `plan.Kind==PlanUpdate` (not gated on `gp==nil`), because RenameToPredlReady removed progress.json — a warm gp from the predl CheckForPredownload would otherwise re-download. Both paths converge on `gp.flavor ∈ {flavorPredlPatch,flavorPredlFull}` → the new apply-switch cases. Bonus: a normal *update* to a version that happens to have a staged predl also consumes it (free optimization).
- **Type consistency:** `flavorPredlPatch.String()=="predl_patch"`, `flavorPredlFull.String()=="predl_full"` (plan_internal.go) — used symmetrically in Task 1 (write) and Task 4 (read). `buildPredlPlan` signature returns `(*genshinPlan, error)` (nil plan = no predl), distinct from `buildPlan`'s `(*genshinPlan, bool, error)`. `loadLegacyPredlConsume(versionDir, version) (*genshinPlan, error)`.
- **No regression:** Genshin/Sophon untouched (sophon dispatch + checkForPredownloadSophon unchanged); the apply-switch additions are new `case` labels only; `wasPredl=false` keeps legacy apply identical to today. Task 3 deletes the now-false `TestSupportsPredownload_SophonGamesOnly`.
- **Test reachability:** all Phase-3 logic is unit-testable without network (responses constructed inline; consume via on-disk sidecars) → runs under plain `go test ./...`. A full legacy download→apply integration test (new getGamePackages httptest harness + real zip blobs) is intentionally out of scope; the apply primitives (`runApplyPlanPatch/Full`, `applyPatchZip`) are already covered by `update_apply_test.go`, and USER smoke covers the live end-to-end.

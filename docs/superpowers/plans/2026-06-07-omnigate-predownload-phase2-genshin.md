# Predownload Phase 2 (Genshin/Sophon) + SidebarRow unify — Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Light & wire predownload for Genshin (Sophon) — make `CheckVersion` surface the predl target and add `CheckForPredownload` that builds the Sophon predl plan even when the game is up-to-date; plus unify `SidebarRow`'s predl status chip on `available_predl` (the Phase-1 follow-up).

**Architecture:** Phase 1 already built the shared infra (App probe + per-game `core.PredownloadChecker` gate + routing + frontend BottomBar). Phase 2 only adds the hoyoverse implementation for Sophon games: (1) `CheckVersion` reports `vi.Predownload` from `getGameBranches.PreDownload`; (2) `SupportsPredownload(gid)` returns `g.UsesSophon` (Genshin true; HSR/ZZZ legacy stay dark until Phase 3); (3) `CheckForPredownload` calls `buildSophonPredlPlan` DIRECTLY (bypassing the idle short-circuit that leaves `gp.predlPlan == nil` when up-to-date — spec §2.3 B1) and caches `gp` so the existing `runSophonPredownload` + predl-consume apply work unchanged.

**Tech Stack:** Go 1.26 (`internal/providers/hoyoverse`), Vue 3 + Pinia + vitest (`frontend/src`). Sophon tests reuse the integration harness (build tag `integration`).

**Scope:** Phase 2 (Genshin) + SidebarRow follow-up only. HSR/ZZZ legacy = Phase 3 (separate plan). Spec: `docs/superpowers/specs/2026-06-07-omnigate-predownload-wiring-design.md` §2.3, §1.5, §9.

**Toolchain note:** `export PATH="/c/Program Files/Go/bin:/c/Users/willie/go/bin:$PATH"`; `CGO_ENABLED=0` → never `-race`. Sophon integration tests run only under `-tags integration`.

---

## File Structure

- `internal/providers/hoyoverse/hoyoverse.go` — `CheckVersion` Sophon branch (Task 1); `SupportsPredownload` + `CheckForPredownload` + `checkForPredownloadSophon` + interface-compliance var (Task 2).
- `internal/providers/hoyoverse/update_sophon_plan.go` — add `sumPredlPlanBytes` helper (Task 2).
- `internal/providers/hoyoverse/predownload_test.go` (NEW, plain) — `SupportsPredownload` + legacy-unsupported unit tests (Task 2).
- `internal/providers/hoyoverse/predownload_integration_test.go` (NEW, `//go:build integration`) — Sophon `CheckVersion` predl + `CheckForPredownload` idle-build tests (Tasks 1-2).
- `internal/providers/hoyoverse/testdata/sophon/branches_idle_predl_build.json` (NEW) — fixture: main idle + predl-build flavor (Task 2).
- `frontend/src/components/SidebarRow.vue` — status chip reads `available_predl` not `has_predownload` (Task 3).
- `frontend/src/__tests__/sidebar_predl_status.test.ts` (NEW) — SidebarRow status test (Task 3).

---

## Task 1: CheckVersion (Sophon) surfaces vi.Predownload

**Files:**
- Modify: `internal/providers/hoyoverse/hoyoverse.go:154-163` (the `if g.UsesSophon` branch in `CheckVersion`)
- Test: `internal/providers/hoyoverse/predownload_integration_test.go` (NEW)

- [ ] **Step 1: Write the failing test**

Create `internal/providers/hoyoverse/predownload_integration_test.go`:

```go
//go:build integration

package hoyoverse

import (
	"context"
	"testing"
)

func TestCheckVersion_SophonSurfacesPredownload(t *testing.T) {
	// branches_with_predl.json: main.tag=6.6.0, pre_download.tag=6.7.0.
	fs := newFakeSophonServer(t, "branches_with_predl.json", "build_small.json", "patch_small.json")
	p, _, _ := newSophonProvider(t, fs, "6.6.0") // installed == main.tag → up-to-date
	vi, err := p.CheckVersion(context.Background(), genshinGID)
	if err != nil {
		t.Fatalf("CheckVersion: %v", err)
	}
	if vi.Latest != "6.6.0" || vi.Current != "6.6.0" {
		t.Errorf("Latest/Current = %q/%q, want 6.6.0/6.6.0", vi.Latest, vi.Current)
	}
	if vi.Predownload == nil || vi.Predownload.TargetVersion != "6.7.0" {
		t.Fatalf("vi.Predownload = %+v, want TargetVersion 6.7.0", vi.Predownload)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test -tags integration ./internal/providers/hoyoverse/ -run TestCheckVersion_SophonSurfacesPredownload`
Expected: FAIL — `vi.Predownload` is nil (the Sophon branch uses `fetchBranchTag` and never sets Predownload).

- [ ] **Step 3: Implement the Sophon CheckVersion predl surfacing**

In `internal/providers/hoyoverse/hoyoverse.go`, replace the `if g.UsesSophon { ... }` block (lines 154-163):

```go
	if g.UsesSophon {
		branch, err := p.fetchBranchInfo(ctx, g.APIGameID)
		if err != nil {
			return core.VersionInfo{}, err
		}
		if branch.Main.IsEmpty() {
			return core.VersionInfo{}, fmt.Errorf("getGameBranches: game id %q has empty main branch", g.APIGameID)
		}
		info := core.VersionInfo{Current: currentLocal, Latest: branch.Main.Tag}
		if info.Current == "" {
			info.Current = info.Latest
		}
		// Surface predl so App.CheckForUpdate (probe) can light the predl button
		// for Genshin. Phase-1 probe additionally gates on SupportsPredownload.
		if !branch.PreDownload.IsEmpty() && branch.PreDownload.Tag != info.Current {
			info.Predownload = &core.PredownloadInfo{TargetVersion: branch.PreDownload.Tag}
		}
		return info, nil
	}
```

(This replaces the `fetchBranchTag` call with `fetchBranchInfo`, which `CheckForUpdate`'s Sophon path already uses. `fetchBranchTag` becomes unused — leave it; it has its own test and is harmless. If `go vet`/lint flags it as unused, it does not, because it is exported-package-private but referenced in tests; verify with the test run in Step 4.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test -tags integration ./internal/providers/hoyoverse/ -run TestCheckVersion_SophonSurfacesPredownload`
Expected: PASS. Then confirm the non-tagged build still compiles + existing tests green:
`go build ./... && go test ./internal/providers/hoyoverse/`
Expected: PASS.

- [ ] **Step 5: Commit** (deferred until the task's review gate returns APPROVE)

```bash
git add internal/providers/hoyoverse/hoyoverse.go internal/providers/hoyoverse/predownload_integration_test.go
git commit -m "feat(predl): hoyoverse CheckVersion surfaces predl from getGameBranches (Sophon)"
```

---

## Task 2: hoyoverse SupportsPredownload + CheckForPredownload (Sophon idle build)

**Files:**
- Modify: `internal/providers/hoyoverse/hoyoverse.go` (add methods after `GetPredownloadAvailable`, ~line 264; add interface var at the block ~875-880)
- Modify: `internal/providers/hoyoverse/update_sophon_plan.go` (add `sumPredlPlanBytes` after `sumSophonTotalBytes`, ~line 453)
- Create: `internal/providers/hoyoverse/testdata/sophon/branches_idle_predl_build.json`
- Test: `internal/providers/hoyoverse/predownload_test.go` (NEW, plain) + add to `predownload_integration_test.go`

- [ ] **Step 1: Write the failing unit tests (plain, no harness)**

Create `internal/providers/hoyoverse/predownload_test.go`:

```go
package hoyoverse

import (
	"context"
	"errors"
	"testing"

	"omnigate/internal/core"
)

func TestSupportsPredownload_SophonGamesOnly(t *testing.T) {
	p := New(Settings{}, nil)
	if !p.SupportsPredownload(core.GameID("hoyoverse/genshin")) {
		t.Error("genshin (UsesSophon) must support predownload in Phase 2")
	}
	if p.SupportsPredownload(core.GameID("hoyoverse/starrail")) {
		t.Error("starrail (legacy) must NOT support predownload until Phase 3")
	}
	if p.SupportsPredownload(core.GameID("hoyoverse/zzz")) {
		t.Error("zzz (legacy) must NOT support predownload until Phase 3")
	}
}

func TestCheckForPredownload_LegacyUnsupported(t *testing.T) {
	p := New(Settings{}, nil)
	_, err := p.CheckForPredownload(context.Background(), core.GameID("hoyoverse/starrail"), nil)
	if !errors.Is(err, core.ErrPredownloadUnsupported) {
		t.Fatalf("err = %v, want ErrPredownloadUnsupported for legacy game", err)
	}
}
```

- [ ] **Step 2: Run unit tests to verify they fail**

Run: `go test ./internal/providers/hoyoverse/ -run 'TestSupportsPredownload_SophonGamesOnly|TestCheckForPredownload_LegacyUnsupported'`
Expected: FAIL — `SupportsPredownload` / `CheckForPredownload` undefined.

- [ ] **Step 3: Add `sumPredlPlanBytes` helper**

In `internal/providers/hoyoverse/update_sophon_plan.go`, after `sumSophonTotalBytes` (after line 453):

```go
// sumPredlPlanBytes is the progress denominator for a standalone predl plan
// (CheckForPredownload builds gp.predlPlan but not gp.sophon* fields, so
// sumSophonTotalBytes would return 0). Mirrors sumSophonTotalBytes over the
// predl plan's chunk sources + deduped patch blobs.
func sumPredlPlanBytes(pp *predlPlanCache) int64 {
	var total int64
	for _, s := range pp.ChunkSources {
		total += s.DecompSize
	}
	seen := map[string]bool{}
	for _, p := range pp.Patches {
		if seen[p.PatchName] {
			continue
		}
		seen[p.PatchName] = true
		total += p.PatchSize
	}
	return total
}
```

- [ ] **Step 4: Add `SupportsPredownload` + `CheckForPredownload` + `checkForPredownloadSophon`**

In `internal/providers/hoyoverse/hoyoverse.go`, after `GetPredownloadAvailable` (after line 264):

```go
// SupportsPredownload implements core.PredownloadChecker. PER-GAME: only Sophon
// games (Genshin) in Phase 2; HSR/ZZZ legacy predl lands in Phase 3. This is what
// keeps the Phase-1 App probe from lighting HSR/ZZZ buttons while one hoyoverse
// type satisfies the interface for all three games.
func (p *Provider) SupportsPredownload(gid core.GameID) bool {
	g := findByID(gid)
	return g != nil && g.UsesSophon
}

// CheckForPredownload implements core.PredownloadChecker. Routes Sophon (Genshin)
// to checkForPredownloadSophon; legacy (HSR/ZZZ) returns ErrPredownloadUnsupported
// until Phase 3.
func (p *Provider) CheckForPredownload(ctx context.Context, gid core.GameID, onProgress func(done, total int)) (core.UpdatePlan, error) {
	g := findByID(gid)
	if g == nil {
		return core.UpdatePlan{}, fmt.Errorf("%w: %s", core.ErrUnknownGame, gid)
	}
	if !g.UsesSophon {
		return core.UpdatePlan{}, core.ErrPredownloadUnsupported
	}
	return p.checkForPredownloadSophon(ctx, gid, onProgress)
}

// checkForPredownloadSophon builds the Sophon predl plan by calling
// buildSophonPredlPlan DIRECTLY, NOT via checkForUpdateSophon. The latter's idle
// short-circuit (hoyoverse.go ~610) returns flavorNone with gp.predlPlan==nil
// exactly when up-to-date — the case predl is offered (spec §2.3 B1). It caches
// gp so RunUpdate(PlanPredownload) → runSophonPredownload consumes gp.predlPlan.
func (p *Provider) checkForPredownloadSophon(ctx context.Context, gid core.GameID, onProgress func(done, total int)) (core.UpdatePlan, error) {
	g := findByID(gid)
	gameDir, err := p.gameDir(gid)
	if err != nil {
		return core.UpdatePlan{}, err
	}
	tempRoot := p.tempRoot(gid)

	branch, err := p.fetchBranchInfo(ctx, g.APIGameID)
	if err != nil {
		if ctx.Err() != nil {
			return core.UpdatePlan{}, ctx.Err()
		}
		return core.UpdatePlan{}, &core.UpdateError{Code: "sophon_manifest_fetch_failed", Retryable: true}
	}
	if branch.PreDownload.IsEmpty() {
		return core.UpdatePlan{}, core.ErrPredownloadUnsupported
	}

	currentLocal, _ := ReadGameVersion(gameDir)
	if currentLocal == "" {
		return core.UpdatePlan{}, &core.UpdateError{Code: "sophon_no_install", Retryable: false}
	}

	audioFolders, _ := DetectInstalledLanguages(gameDir)
	audioLangs := mapFoldersToMatchingFields(audioFolders)

	prev := LoadAppliedManifests(tempRoot, gid)
	oldMainManifest := prev.MatchByVersion(currentLocal, "game")

	gp := &genshinPlan{
		UpdatePlan: core.UpdatePlan{
			GameID:  gid,
			Kind:    core.PlanPredownload,
			Version: branch.PreDownload.Tag,
			Reason:  core.ReasonPredownload,
		},
		sophonBranch:              branch,
		sophonPatchAssetsFromMain: map[string][]sophon.ChunkSource{},
		sophonAssetMD5:            map[string]string{},
		sophonRawManifests:        map[string][]byte{},
		sourceVersion:             currentLocal,
		audioLanguages:            audioLangs,
	}

	predlAvail, err := buildSophonPredlPlan(ctx, p, gp, branch, g.PlatApp, currentLocal, audioLangs, oldMainManifest, prev, gameDir)
	if err != nil {
		if ctx.Err() != nil {
			return core.UpdatePlan{}, ctx.Err()
		}
		return core.UpdatePlan{}, err
	}
	if !predlAvail || gp.predlPlan == nil {
		// No chunk reuse possible (full-flavor predl is never offered, §0) → treat
		// as no actionable predl.
		return core.UpdatePlan{}, core.ErrPredownloadUnsupported
	}
	gp.predlAvailable = true
	gp.TotalBytes = sumPredlPlanBytes(gp.predlPlan)
	p.manifestCache.put(gid, gp)
	return gp.UpdatePlan, nil
}
```

Then add to the compile-time interface block (hoyoverse.go:875-880):

```go
	_ core.PredownloadChecker = (*Provider)(nil)
```

(`onProgress` is intentionally unused by the Sophon predl build — buildSophonPredlPlan does not surface verify progress for predl; the parameter exists to satisfy the interface. Prefix-underscore is not needed since it is a named interface-method param; Go does not flag unused method params.)

- [ ] **Step 5: Run unit tests to verify they pass**

Run: `go test ./internal/providers/hoyoverse/ -run 'TestSupportsPredownload_SophonGamesOnly|TestCheckForPredownload_LegacyUnsupported'`
Expected: PASS. Also `go build ./...` → clean (confirms `_ core.PredownloadChecker = (*Provider)(nil)` holds).

- [ ] **Step 6: Add the idle-predl-build fixture**

Create `internal/providers/hoyoverse/testdata/sophon/branches_idle_predl_build.json` (copy of `branches_with_predl.json` with `pre_download.diff_tags` set to `["6.5.0"]` so that an installed `6.6.0` is NOT in the predl diff window → forces predl **build** flavor, which reuses the proven `build_small.json` getBuild path):

```json
{
  "data": {
    "game_branches": [
      {
        "game": { "biz": "hk4e_global", "id": "gopR6Cufr3" },
        "main": {
          "branch": "main",
          "categories": [
            { "category_id": "10016", "matching_field": "game", "type": "CATEGORY_TYPE_RESOURCE" }
          ],
          "diff_tags": ["6.5.0"],
          "package_id": "pkg-main",
          "password": "",
          "tag": "6.6.0"
        },
        "pre_download": {
          "branch": "predownload",
          "categories": [
            { "category_id": "10016", "matching_field": "game", "type": "CATEGORY_TYPE_RESOURCE" }
          ],
          "diff_tags": ["6.5.0"],
          "package_id": "pkg-predl",
          "password": "",
          "tag": "6.7.0"
        }
      }
    ]
  },
  "message": "",
  "retcode": 0
}
```

- [ ] **Step 7: Write the failing Sophon integration test (idle case = B1 regression)**

Append to `internal/providers/hoyoverse/predownload_integration_test.go`:

```go
func TestCheckForPredownload_SophonBuildsPredlPlanWhenUpToDate(t *testing.T) {
	// main.tag=6.6.0, installed=6.6.0 → main is IDLE. pre_download.tag=6.7.0,
	// diff_tags=["6.5.0"] (excludes 6.6.0) → predl BUILD flavor (reuses build_small.json).
	fs := newFakeSophonServer(t, "branches_idle_predl_build.json", "build_small.json", "patch_small.json")
	p, gameDir, tempRoot := newSophonProvider(t, fs, "6.6.0")
	seedAppliedManifest(t, tempRoot, "6.6.0") // oldMainManifest != nil → enables predl build flavor
	seedOldFiles(t, gameDir)

	// Plain CheckForUpdate hits the idle short-circuit → predl NOT built (documents B1).
	if _, err := p.CheckForUpdate(context.Background(), genshinGID); err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if p.GetPredownloadAvailable(genshinGID) {
		t.Fatal("precondition: idle CheckForUpdate must not build the predl plan (B1 gap being fixed)")
	}

	// CheckForPredownload builds it directly even though up-to-date.
	plan, err := p.CheckForPredownload(context.Background(), genshinGID, nil)
	if err != nil {
		t.Fatalf("CheckForPredownload: %v", err)
	}
	if plan.Kind != core.PlanPredownload || plan.Version != "6.7.0" {
		t.Fatalf("plan = {Kind:%v Version:%q}, want PlanPredownload @ 6.7.0", plan.Kind, plan.Version)
	}
	gp := p.manifestCache.get(genshinGID)
	if gp == nil || gp.predlPlan == nil {
		t.Fatal("predlPlan must be populated in manifestCache after CheckForPredownload")
	}
	if gp.predlPlan.TargetVersion != "6.7.0" {
		t.Errorf("predlPlan.TargetVersion = %q, want 6.7.0", gp.predlPlan.TargetVersion)
	}
}
```

(This test imports `core` — add `"omnigate/internal/core"` to the integration test file's imports.)

- [ ] **Step 8: Run the integration test to verify it fails then passes**

Run (before Step 4's impl would be reverted — but since impl is already in): `go test -tags integration ./internal/providers/hoyoverse/ -run TestCheckForPredownload_SophonBuildsPredlPlanWhenUpToDate`
Expected: PASS (impl from Step 4 present). If it FAILS on a fixture-shape mismatch in `buildSophonPredlPlan` (e.g. getBuild manifest vs `branches_idle_predl_build.json` package_id), inspect the error and align the fixture's `pre_download.package_id`/`diff_tags` with what `build_small.json` + `newFakeSophonServer` serve for the predl slot; the proven reference is `TestSophonPredl_StagingThenLiveApply` (predl build flavor). Re-run until PASS.

- [ ] **Step 9: Run all hoyoverse tests (tagged + untagged)**

Run: `go test ./internal/providers/hoyoverse/ && go test -tags integration ./internal/providers/hoyoverse/`
Expected: PASS both.

- [ ] **Step 10: Commit** (deferred until review APPROVE)

```bash
git add internal/providers/hoyoverse/hoyoverse.go internal/providers/hoyoverse/update_sophon_plan.go internal/providers/hoyoverse/predownload_test.go internal/providers/hoyoverse/predownload_integration_test.go internal/providers/hoyoverse/testdata/sophon/branches_idle_predl_build.json
git commit -m "feat(predl): hoyoverse SupportsPredownload + CheckForPredownload (Sophon idle build)"
```

---

## Task 3: SidebarRow predl status chip unified on available_predl

**Files:**
- Modify: `frontend/src/components/SidebarRow.vue:36` (status chip predl branch)
- Test: `frontend/src/__tests__/sidebar_predl_status.test.ts` (NEW)

- [ ] **Step 1: Write the failing test**

Create `frontend/src/__tests__/sidebar_predl_status.test.ts`:

```ts
import { describe, it, expect, vi } from 'vitest';
import { mount } from '@vue/test-utils';
import { createPinia, setActivePinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import SidebarRow from '../components/SidebarRow.vue';
import en from '../locales/en.json';
import { useUpdatesStore } from '../stores/updates';

vi.mock('../composables/useDialog', () => ({ confirm: vi.fn().mockResolvedValue(true) }));
vi.mock('../../wailsjs/go/app/App', () => ({
  Launch: vi.fn(), StartUpdate: vi.fn(), StartPredownload: vi.fn(), CancelInFlight: vi.fn(),
  ApplyPredownload: vi.fn(), RemovePredownload: vi.fn(), DismissError: vi.fn(),
  ResumeInterrupted: vi.fn(), UpdateStatusAll: vi.fn(async () => ({})), CheckForUpdate: vi.fn(),
}));
vi.mock('../../wailsjs/runtime/runtime', () => ({ EventsOn: vi.fn() }));

function mountRow(rowExtra: any, snap: any) {
  setActivePinia(createPinia());
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } });
  const row = {
    id: 'kurogames/wutheringwaves', backend: 'kurogames',
    display_name: { en: 'Wuthering Waves' }, installed: true,
    has_predownload: false, current_version: '3.3.0', latest_version: '3.3.0',
    ...rowExtra,
  };
  const wrapper = mount(SidebarRow, { props: { row } as any, global: { plugins: [i18n] } });
  const updates = useUpdatesStore();
  if (snap) updates.byGame[row.id] = snap;
  return wrapper;
}

describe('SidebarRow predl status', () => {
  it('shows the predownload chip when available_predl is set', async () => {
    const wrapper = mountRow({}, { available_predl: { version: '3.4.0' }, in_flight: null });
    await wrapper.vm.$nextTick();
    expect(wrapper.find('.game-status-mini').classes()).toContain('predownload');
  });

  it('does NOT show predownload from has_predownload alone', async () => {
    const wrapper = mountRow({ has_predownload: true }, { available_predl: null, in_flight: null });
    await wrapper.vm.$nextTick();
    expect(wrapper.find('.game-status-mini').classes()).not.toContain('predownload');
    expect(wrapper.find('.game-status-mini').classes()).toContain('ready');
  });
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd frontend && npx vitest run src/__tests__/sidebar_predl_status.test.ts`
Expected: FAIL — second test fails: with `has_predownload:true` the current code returns the `predownload` chip regardless of `available_predl`.

- [ ] **Step 3: Implement the change**

In `frontend/src/components/SidebarRow.vue`, change the `status` function's predl branch (line 36) from `props.row.has_predownload` to the snapshot's `available_predl`:

```ts
  if (snap.value?.available_predl) return { key: 'predownload', label: t('status.predownload') + ' · 0%', cls: 'predownload' };
```

(`snap` is the already-declared `computed(() => updates.byGame[props.row.id] ?? null)` at SidebarRow.vue:42; reading `snap.value` inside `status()` is reactive because `status()` is re-invoked on each render. The function is declared above `snap`, but it is only *called* during render, after setup completes, so the closure resolves `snap` correctly.)

- [ ] **Step 4: Run test to verify it passes**

Run: `cd frontend && npx vitest run src/__tests__/sidebar_predl_status.test.ts`
Expected: PASS (both). Then run the full suite for no regression: `cd frontend && npx vitest run`
Expected: PASS (all files).

- [ ] **Step 5: Commit** (deferred until review APPROVE)

```bash
git add frontend/src/components/SidebarRow.vue frontend/src/__tests__/sidebar_predl_status.test.ts
git commit -m "feat(predl): SidebarRow status chip follows available_predl not has_predownload"
```

---

## Final verification (after all tasks committed)

- [ ] `go vet ./...` — clean
- [ ] `go test ./...` — green (untagged)
- [ ] `go test -tags integration ./internal/providers/hoyoverse/` — green (Sophon predl tests)
- [ ] `cd frontend && npm test` — vitest green (existing + new SidebarRow test)
- [ ] `cd frontend && npm run build` — clean
- [ ] `wails build` — produces `build/bin/omnigate.exe`
- [ ] **USER smoke (Phase 2):** on a real Genshin install during an open predl window — Refresh → predl button appears next to the per-game gear → click → Sophon predl staging progress → staged (☁✓ in sidebar) → on patch day, 套用/normal update consumes the staged predl. (HoYo predl windows are time-limited; smoke timing matters.)

---

## Self-review notes (author)

- **Spec coverage:** §2.3.1 CheckVersion predl surfacing → Task 1; §2.3.2 CheckForPredownload via buildSophonPredlPlan direct (B1) + SupportsPredownload=g.UsesSophon → Task 2; §2.3 TotalBytes cosmetic → Task 2 (`sumPredlPlanBytes`); §1.5 SidebarRow unify (Phase-1 follow-up the user asked to fold in) → Task 3. §9 Phase 2 row fully covered. Phase-1 App probe/routing/BottomBar already done — no change needed here; once `SupportsPredownload(genshin)` returns true, the probe lights Genshin automatically.
- **Per-game safety:** `SupportsPredownload` returns `g.UsesSophon` → Genshin true, HSR/ZZZ false → HSR/ZZZ stay dark (probe gate + CheckForPredownload legacy returns `ErrPredownloadUnsupported`). Matches §9 Phase-2 intermediate-state requirement.
- **Type consistency:** `checkForPredownloadSophon` mirrors `buildSophonPlan`'s prep (lines 370-380) exactly — `findByID`/`PlatApp`, `LoadAppliedManifests`, `MatchByVersion`, `DetectInstalledLanguages`+`mapFoldersToMatchingFields`, `buildSophonPredlPlan(ctx,p,gp,branch,platApp,currentLocal,audioLangs,oldMainManifest,prev,gameDir)`. `sumPredlPlanBytes` uses `predlPlanCache.ChunkSources` (`[]sophon.ChunkSource`, `.DecompSize`) + `.Patches` (`[]sophon.PatchInstr`, `.PatchName`/`.PatchSize`) — same fields `sumSophonTotalBytes` uses.
- **Test reachability:** Sophon tests behind `//go:build integration` (matching `integration_test.go`); final verification runs them with `-tags integration`. Unit tests (`SupportsPredownload`, legacy-unsupported) are plain (no harness) and run under `go test ./...`.

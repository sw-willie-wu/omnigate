# M3.C Phase B — Endfield Update Download + Apply Implementation Plan

> **STATUS: OUTLINE — not yet executable.** The verbatim TDD steps for this phase are
> written **after** Phase A's smoke (Task A6) validates the `get_latest` protocol on a
> real Endfield install, so the download/apply code is grounded in the confirmed
> `LOCAL_VERSION_SOURCE`, `UPTODATE_DISCRIMINATOR`, `RESPONSE_ENVELOPE`, and pack URL
> shape recorded in `docs/superpowers/research/m3c-endfield-update-protocol.md`. This
> file currently captures scope, the task list, and the verbatim mirror sources so the
> plan can be filled in quickly post-smoke.

> **For agentic workers:** REQUIRED SUB-SKILL: superpowers:subagent-driven-development.

**Goal:** Add in-app update download + atomic apply to the `hypergryph` provider so an
out-of-date Endfield install can be brought to the latest launchable version from
within Omnigate, mirroring M3.A `kurogames`.

**Architecture:** Implement `core.Updater` (`CheckForUpdate` + `RunUpdate`) +
`core.CheckForUpdateProgress` + mandatory `core.ProcessChecker` on the hypergryph
Provider. 4-worker byte-range download of full `pkg.packs[]` → MD5 verify →
WAL-guarded apply (concat split zips, extract, atomic rename into the game dir, write
back local version) → cleanup. Reuses Phase A's `get_latest` client + version
detection. The App-layer RPC, Pinia store, and BottomBar/SidebarRow/bell UI are reused
unchanged.

**Spec:** `docs/superpowers/specs/2026-06-02-omnigate-m3c-endfield-update-design.md` (§1, §3–§9).
**Prereq:** Phase A merged/validated; protocol doc's open-question outputs filled in.
**Verbatim mirror sources (read these when filling in tasks):**
- `internal/providers/kurogames/update_download.go` → download worker pool + retry + progress.
- `internal/providers/kurogames/update_apply.go` → WAL + validateSameVolume + atomicRename + version writeback + cleanup.
- `internal/providers/kurogames/update_progress.go` → progressStore.
- `internal/providers/kurogames/apply_lock_windows.go` / `process_check_windows.go` → lock + process check.
- `internal/providers/kurogames/kurogames.go` → Updater integration (CheckForUpdate/RunUpdate dispatch, interface assertions).
- `internal/providers/hoyoverse/` → `update_preflight.go` (disk space) only if Phase A didn't inline it.

---

## File Structure (Phase B)

| File | New/Mod | Responsibility | Mirror |
|---|---|---|---|
| `internal/app/settings.go` + `app.go` + `hypergryph.go` Settings | mod | `HypergryphSettings.TempDir` plumbing (field + `LoadSettings` projection + `constructProviders` wiring + `tempDirFor` `case "hypergryph"`) | — |
| `update_manifest.go` | mod | add `packsToFileTasks` + `filterChangedFiles` (MD5 compare) + disk/same-volume preflight | kuro `update_manifest.go` |
| `update_progress.go` | new | `progressStore` (Init/MarkComplete/dir/writeAtomic) | kuro `update_progress.go` |
| `apply_lock.go` + `apply_lock_windows.go` + `apply_lock_other.go` | new | LockFileEx apply lock / noop | kuro |
| `process_check_windows.go` + `process_check_other.go` | new | `Endfield.exe` detection | kuro |
| `update_download.go` | new | 4-worker byte-range download + MD5 verify + retry + cancel + progress | kuro |
| `update_apply.go` | new | WAL + validateSameVolume + concat split zips + extract + atomic rename + version writeback + cleanup | kuro (+ `archive/zip` for split-concat extract) |
| `hypergryph.go` | mod | `CheckForUpdate` + `RunUpdate` + `CheckForUpdateWithProgress` + `IsGameRunning` + interface assertions; predl suppression | kuro `kurogames.go` |
| `errcode_coverage_test.go` | new | every emitted §7 code referenced | kuro |
| Frontend | mod | i18n parity for any new key; predl-hidden Vitest assertion | — |

---

## Task list (verbatim steps filled post-Phase-A-smoke)

- **Task B1 — Settings.TempDir plumbing.** (settings.go field + `temp_dir,omitempty` tag + `LoadSettings` projection; app.go `constructProviders` passes TempDir; `tempDirFor` `case "hypergryph"`; provider `Settings.TempDir`.) Test: `settings_test.go` round-trip. *(Verbatim drafted already in the earlier combined draft — reuse.)*
- **Task B2 — `packsToFileTasks` + `filterChangedFiles` + preflight.** Map full packs → `[]core.FileTask` (Path = pack basename); MD5-compare against already-staged packs to enable resume; disk-space (`disk_full`) + same-volume (`cross_volume_temp`) preflight inlined kuro-style. Pack-URL validated against the TEST_ANCHOR regex.
- **Task B3 — `progressStore`.** Mirror kuro `update_progress.go` verbatim (package rename only): `newProgressStore`, `dir()`, `Init`, `MarkComplete` (lock-held write), `writeAtomic`. Reuses `core.ProgressFile`/`LoadProgressFromPath`.
- **Task B4 — apply lock.** Mirror kuro `apply_lock*.go` verbatim; lock file `apply.lock` under `tempRoot`.
- **Task B5 — process check + ProcessChecker.** Mirror kuro `process_check_windows.go`; `Endfield.exe` via `CreateToolhelp32Snapshot`. Add `IsGameRunning(gid)` + `var _ core.ProcessChecker`.
- **Task B6 — download phase.** Mirror kuro `update_download.go` verbatim (4 workers, `netRetries`/`hashRetries`, `RetryClock` seam, byte-range `.part` resume, MD5 `corrupt`, throttled progress). Files are 1 GiB packs.
- **Task B7 — apply phase.** Mirror kuro `update_apply.go` (WAL, `validateSameVolume`→`cross_volume_midrun`, atomicRename, cleanup) **plus** the Endfield-specific apply: concat `<name>.zip.001..NNN` → `archive/zip` extract into staging → atomic rename into game dir; write back local version to `LOCAL_VERSION_SOURCE` (conditional — no-op if degrade). Unzip-only, no hpatchz.
- **Task B8 — Provider integration (`hypergryph.go`).** `CheckForUpdate` (local ver vs `rsp.version` using `UPTODATE_DISCRIMINATOR`; build plan from `filterChangedFiles`; `ReasonVersionChanged`; `manifest_not_found`/`protocol_unsupported` per `PACKAGE_PATH_LIVE`); `RunUpdate` (re-check `rsp.version`==`plan.ManifestETag`→`manifest_changed`; preflight; process-check→`process_blocked`; download phase; apply phase); `CheckForUpdateWithProgress`; interface assertions `var _ core.Updater/CheckForUpdateProgress/ProcessChecker`. Predl suppression: do NOT implement the predl-exposer interface so the App hides the predl button (verify the guard in `update_handler.go` first).
- **Task B9 — errcode coverage + crash-recovery resume wiring.** `errcode_coverage_test.go`; confirm `core` recovery + the existing bell-drawer `interrupted_resume_*` flow fires for a hypergryph crash (App `scanForRecovery` already generalized by m3-refactor — verify `knownBackendIDs` includes hypergryph).
- **Task B10 — Frontend.** i18n: only `protocol_unsupported` if Phase A/B introduced it (add to en/zh-TW/zh-CN + both `i18n_parity.test.ts` required-lists). Vitest: assert predl button not rendered for a Hypergryph game.
- **Task B11 (USER) — full smoke + ship.** M3.A-shaped 7-point smoke on a real Endfield install (start at latest → fake-stale → [更新遊戲] → progress → cancel → real pack download+unzip+apply+version writeback → i18n → crash-recovery resume) + Program-Files admin-writeback check. Then tag `v0.5.0-m3c` + `git checkout main && git merge --no-ff m3c/spec` (`feedback_commits`: no Co-Authored-By). Mark M3.C SHIPPED in memory.

---

## Open items inherited from Phase A spike (must be filled before B is executable)

- `LOCAL_VERSION_SOURCE` → Task B7 version writeback sink + Task B8 staleness compare.
- `UPTODATE_DISCRIMINATOR` → Task B8 `CheckForUpdate` up-to-date branch.
- `RESPONSE_ENVELOPE` → Task B2 pack extraction (envelope unwrap if nested).
- `PACKAGE_PATH_LIVE` → Task B8 `protocol_unsupported` fallback (if stranded, B becomes a no-op and M3.C ships as Phase A only).
- Whether `pkg.packs` split parts need concat-before-unzip vs each `.zip.NNN` is independently valid → confirm during B7 by inspecting a real downloaded pack set in the smoke.

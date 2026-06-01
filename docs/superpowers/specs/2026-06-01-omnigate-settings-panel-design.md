# Omnigate Settings Panel — Design Spec

Date: 2026-06-01
Branch: `settings-ui/spec`
Status: APPROVED (brainstorm) — pending plan

## §0 Goal

Wire the Topbar gear button (inert since M2) to a working settings panel that lets
the user edit the only settings that currently drive real behavior: each backend's
install **path** and (where applicable) its **temp_dir** override. The backend
already exposes `App.GetSettings()` / `App.UpdateSettings()`; the gap is purely the
frontend panel + one folder-picker bridge method.

## §1 Locked decisions

| # | Decision |
|---|---|
| 1 | **Presentation**: right slide-over drawer (`Teleport` to body, mirrors existing `notif-panel`). Backdrop click-away + ESC close. Bottom Save/Cancel bar. |
| 2 | **Content (lean)**: per-backend install path + temp_dir override only. |
| 3 | **Save model**: explicit. Edits are staged in a local draft; **Save** calls `UpdateSettings` once (→ re-detect); **Cancel**/close/ESC discards. |
| 4 | **Path editing**: native `OpenDirectoryDialog` via a new `App.BrowseForDirectory`, plus the field stays manually editable as fallback. |
| 5 | **temp_dir**: empty = runtime default (`<TEMP>/omnigate/<backend>/…`). Field clearable; hint explains empty=default. |

### Out of scope (excluded by design)
- **Language** — already toggled on the Topbar; not duplicated here. (Known: it is runtime-only / not persisted on startup; unchanged by this work.)
- **Region** (hoyoverse) — single value `global`; nothing to choose. Stored value preserved untouched.
- **`banner_animation_pref`, `show_technical_info`** — defined in `Settings` but not consumed anywhere; not surfaced (would be dead controls). Preserved untouched.
- In-panel "detected N games" feedback — re-detection is reflected by the sidebar; no extra in-panel status.

## §2 Backend settings shape (existing, unchanged)

`internal/app/settings.go`:

```go
Settings{
  Version int
  App     AppSettings{ Language, BannerAnimationPref string; ShowTechnicalInfo bool }
  Backends BackendSettings{
    Hoyoverse  HoyoverseSettings{ Path, Region, TempDir string }
    Kurogames  KurogamesSettings{ Path, TempDir string }
    Hypergryph HypergryphSettings{ Path string }   // no TempDir
  }
}
```

- `App.GetSettings() Settings` and `App.UpdateSettings(s Settings) error` are already
  Wails-bound. `UpdateSettings` saves TOML, replaces in-memory settings,
  `invalidateDetect()`, and `constructProviders()`.
- Editable fields in the panel: `Backends.Hoyoverse.{Path,TempDir}`,
  `Backends.Kurogames.{Path,TempDir}`, `Backends.Hypergryph.Path`.
- **All other fields are preserved**: the draft is a full copy of `GetSettings()`;
  the panel mutates only the five fields above; Save writes the whole struct back.

## §3 New backend method

```go
// BrowseForDirectory opens the native folder picker seeded at `current`
// (if it exists) and returns the chosen absolute path, or "" if cancelled.
func (a *App) BrowseForDirectory(current string) (string, error)
```

- Implementation: `wruntime.OpenDirectoryDialog(a.ctx, wruntime.OpenDialogOptions{Title: "...", DefaultDirectory: current})`.
- Guard: if `a.ctx == nil` (no window, e.g. tests) return `("", nil)` rather than panicking.
- Wails-bound; consumed by the panel's "Browse…" buttons.

## §4 Frontend component — `SettingsPanel.vue`

Mirrors `Topbar.vue`'s `notif-panel` overlay pattern (`Teleport to body`, backdrop +
panel, z-index above content).

**Open/close state**: a boolean in the `view` store (e.g. `settingsOpen`) so the
Topbar gear can toggle it and the panel can close itself. ESC + backdrop click close
(close == discard).

**Lifecycle**:
1. On open: `const draft = structuredClone(await GetSettings())`. Keep a pristine copy
   for dirty-checking (optional) and to discard on cancel.
2. Render sections (one per backend) binding to `draft.Backends.*`.
3. "Browse…" button → `path = await BrowseForDirectory(currentField); if (path) field = path`.
4. **Save**: `await UpdateSettings(draft)`; on success → trigger a games refresh
   (`games.load()` + version/asset refresh, same chain as Topbar `onRefresh`) so the
   sidebar reflects new detection; then close. On error → show inline error, keep open.
5. **Cancel/ESC/backdrop**: discard draft, close. (No write.)

**Sections** (each = backend label + fields):
- HoYoverse: Path (text + Browse), TempDir (text + Browse + Clear, hint)
- Kuro: Path (text + Browse), TempDir (text + Browse + Clear, hint)
- Hypergryph: Path (text + Browse)

**Topbar wiring**: gear `<button>` gets `@click` to set `view.settingsOpen = true`.

## §5 i18n (new `settings.*` keys, ×3 locales: zh-TW / zh-CN / en)

- `settings.title` — panel header ("設定")
- `settings.save`, `settings.cancel`, `settings.browse`, `settings.clear`
- `settings.path_label`, `settings.tempdir_label`
- `settings.tempdir_hint` — "留空 = 使用預設暫存資料夾"
- `settings.save_error` — inline save failure (with `{detail}`)
- `settings.backend.hoyoverse`, `settings.backend.kurogames`, `settings.backend.hypergryph` — section labels

i18n parity test must cover the new keys across all three locales.

## §6 Error handling

| Case | Behavior |
|---|---|
| `UpdateSettings` returns error | Inline error in panel (`settings.save_error` + detail); panel stays open; draft kept. |
| `BrowseForDirectory` cancelled ("") | No field change. |
| `BrowseForDirectory` error | Ignore / no change (best-effort; field stays manually editable). |
| Path points to a non-existent / wrong folder | Saved as-is; re-detection simply finds nothing for that backend. No pre-validation gate. |

## §7 Testing

**Vitest** (`SettingsPanel` + i18n):
- Panel loads `GetSettings()` into the draft and renders current path values.
- Editing a path then Save calls `UpdateSettings` with the edited draft (other fields preserved).
- Cancel/ESC closes without calling `UpdateSettings`.
- Save error keeps the panel open and shows the error label.
- i18n parity: all new `settings.*` keys present + non-empty in zh-TW / zh-CN / en.

**Go**:
- `BrowseForDirectory` with `a.ctx == nil` returns `("", nil)` (guard). The real
  dialog path needs a live Wails window and is exercised by manual smoke, not unit tests.
- `UpdateSettings` round-trip already covered by `settings_test.go`.

## §8 Manual smoke (post-implementation, USER)

1. Click gear → drawer slides in from right; shows current paths.
2. Browse… → native folder dialog opens; pick a folder → field updates.
3. Edit a temp_dir to a folder on a roomier drive; Save → no error; drawer closes.
4. Sidebar re-detects (game list reflects any path change).
5. Reopen settings → values persisted (TOML written). Restart app → still persisted.
6. Cancel/ESC discards edits.

## §9 Files touched

- `internal/app/app.go` (or new `internal/app/dialog.go`) — `BrowseForDirectory`.
- `frontend/src/components/SettingsPanel.vue` — new.
- `frontend/src/components/Topbar.vue` — wire gear `@click`.
- `frontend/src/stores/view.ts` — `settingsOpen` flag (if not already present).
- `frontend/src/styles/theme.css` — drawer styles (reuse notif-panel patterns).
- `frontend/src/locales/{zh-TW,zh-CN,en}.json` — `settings.*` keys.
- Tests: `frontend/src/__tests__/settings_panel.test.ts`, i18n parity update, `internal/app` guard test.
- Wails bindings regenerated for `BrowseForDirectory` (`wails generate module` / build).

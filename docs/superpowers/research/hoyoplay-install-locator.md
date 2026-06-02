# HoYoPlay install-locator research (Task 11)

Date: 2026-06-03 (live-machine inspection + Collapse-reference posture)

## Finding: HoYoPlay records each game's real install path in the registry

Per-game key (REG_SZ values), one subkey per game biz:

```
HKEY_CURRENT_USER\Software\Cognosphere\HYP\1_0\<biz>
    GameInstallPath  (REG_SZ)  → the actual install folder
    GameBiz          (REG_SZ)  → the biz code (== subkey name)
    GameIconPath     (REG_SZ)  → launcher icon (out of scope)
```

Observed on the live machine:

| biz subkey      | GameInstallPath                                          | our GameID            |
|-----------------|----------------------------------------------------------|-----------------------|
| `hk4e_global`   | `C:\Program Files\HoYoPlay\games\Genshin Impact game`    | `hoyoverse/genshin`   |
| `hkrpg_global`  | `C:\Program Files\HoYoPlay\games\Star Rail Games`        | `hoyoverse/starrail`  |
| `nap_global`    | `C:\Program Files\HoYoPlay\games\ZenlessZoneZero Game`   | `hoyoverse/zzz`       |

`GameInstallPath` is the authoritative location regardless of where the user
installed the game — exactly what the layer-2 `InstallLocator` needs. The biz
codes match the hoyoverse provider's `games` table `Biz` field (used elsewhere,
e.g. `GetIcon` → `g.Biz`).

## Implementation shape

- Registry root: `HKCU\Software\Cognosphere\HYP\1_0`. For each known game with a
  non-empty `Biz`, open `<root>\<biz>` and read `GameInstallPath`; if present and
  non-empty, map `g.ID → GameInstallPath`.
- **Injectable source for testability** (no live registry in CI): the locator
  takes a `hoyoplayInstallReader func(biz string) (string, bool)`. Production
  reader uses `golang.org/x/sys/windows/registry` (the project already depends on
  `golang.org/x/sys/windows`). Tests pass a fixture reader backed by a sanitized
  biz→path map.
- `Provider.LocateInstalls(ctx)` delegates to a locator built with the registry
  reader, satisfying `core.InstallLocator` so App resolution auto-uses it
  (stat-validated at layer 2).

## Notes / caveats

- Windows-only (registry). Match the project's existing `*_windows.go` build-tag
  pattern (see `launch_windows.go`) — put the registry reader behind the windows
  tag, keep the locator logic (reader injection + biz→ID mapping) platform-neutral
  so it's testable on any OS via the fixture reader.
- HKCU (per-user). HoYoPlay is a per-user launcher; HKCU is correct. No HKLM
  fallback observed/needed.
- The older `HKCU\Software\miHoYo\<Game>` keys also exist but are per-game Unity
  player prefs, NOT install paths — do not use them.
- This is local-registry inspection, not a HoYoverse server probe (consistent
  with the Collapse-reference posture).

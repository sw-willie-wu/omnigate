# GRYPHLINK (Endfield) install-locator research (Task 13)

Date: 2026-06-03 (live-machine inspection). Lowest-confidence of the three.

## Finding: no per-game install path is recorded; only the LAUNCHER root is

What exists on the machine:
- **Uninstall entry** (the usable signal):
  ```
  HKLM\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\<hash>
      DisplayName     = GRYPHLINK
      InstallLocation = C:\Program Files\GRYPHLINK          ← launcher root (populated!)
      DisplayIcon     = C:\Program Files\GRYPHLINK\Launcher.exe
      UninstallString = C:\Program Files\GRYPHLINK\Uninstall.exe
  ```
  (`<hash>` = `d9f71f86…`, the per-install GRYPHLINK fingerprint.)
- `HKCU\Software\GRYPHLINK\Launcher\<hash>` — launcher config (language, wid, …),
  **no install path**.
- `HKCU\Software\Gryphline\Endfield` — Unity PlayerPrefs (graphics/audio/game
  progress), **no install path**.

So unlike HoYoPlay/KRLauncher, GRYPHLINK does not expose a per-game install path.
The only readable record is the **launcher root** (`InstallLocation`).

## Implementation shape

The game lives under `<launcher root>\games\EndField Game` — which is exactly how
detection already joins (`hypergryph.FolderNames()["hypergryph/endfield"]` =
`games/EndField Game`, joined onto the root). So the locator:
1. Find the uninstall entry with `DisplayName == "GRYPHLINK"` across the three
   uninstall roots; take `InstallLocation` (fallback: `filepath.Dir(DisplayIcon
   or UninstallString)`) = the launcher root.
2. For each game in `hypergryph.FolderNames()`, map `gid → filepath.Join(root,
   folder)`.

**Value-add:** only when GRYPHLINK is installed to a non-default location — then
the uninstall root differs from `DefaultRoot` and the game is found anyway. At the
default location it equals DefaultScan (harmless). **Assumption:** the game sits
under the launcher's `games/` folder (no evidence GRYPHLINK supports a separate
per-game install dir; no per-game record exists to honor one if it did).

- Injectable source (testable): `func() (launcherRoot string, ok bool)`; windows
  impl scans the uninstall registry, test passes a fixture root. Registry behind
  `//go:build windows`; the join + `FolderNames()` mapping is platform-neutral.
- `Provider.LocateInstalls` delegates → satisfies `core.InstallLocator`.

## Decision: IMPLEMENT (not defer)

The uninstall `InstallLocation` is clean and populated, so the locator is cheap and
adds real robustness for custom GRYPHLINK install locations. (The plan permits
default-scan-only fallback; we don't need it here.)

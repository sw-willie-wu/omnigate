# KRLauncher (Wuthering Waves) install-locator research (Task 12)

Date: 2026-06-03 (live-machine inspection)

## Finding: the install path is in the Windows uninstall registry (KR installer)

KRLauncher/the Kuro installer writes a standard uninstall entry whose
`UninstallString` / `DisplayIcon` point into the game install folder.

Observed:
```
HKLM\SOFTWARE\WOW6432Node\Microsoft\Windows\CurrentVersion\Uninstall\KRInstall Wuthering Waves Overseas
    DisplayName     = Wuthering Waves
    InstallLocation = (empty — do NOT use)
    DisplayIcon     = C:\Program Files\Wuthering Waves\launcher.exe
    UninstallString = C:\Program Files\Wuthering Waves\uninst.exe
```
`InstallLocation` is blank, but `filepath.Dir(UninstallString)` (or
`filepath.Dir(DisplayIcon)`) = `C:\Program Files\Wuthering Waves` — the
**launcher root**, authoritative wherever the user installed it. NOTE: this is
the launcher root, NOT the game folder — the actual game (with
`launcherDownloadConfig.json`) lives in the `FolderName` subfolder
(`Wuthering Waves Game`). The locator MUST join `FolderNames()` onto this root
(like the GRYPHLINK locator), not return the root directly.

- The subkey name is `KRInstall Wuthering Waves Overseas` (the `KRInstall`
  prefix is the Kuro installer; `Overseas` is the global region — a CN build
  would differ, e.g. `... Wuthering Waves`/no "Overseas").
- It lives in the **WOW6432Node** uninstall root here, but to be robust the
  locator should scan all three uninstall roots: `HKLM\…\Uninstall`,
  `HKLM\…\WOW6432Node\…\Uninstall`, `HKCU\…\Uninstall`.
- No `HKCU\Software\KRLauncher`/`Kuro` registry path; `%APPDATA%\KRLauncher\…`
  holds only the WebView2 cache + a 48-byte `kr_starter_cached.json` (no path).

## Implementation shape (mirror the HoYoPlay locator, Task 11)

- Match a subkey under any uninstall root whose name starts with `KRInstall` and
  contains `Wuthering Waves`; read `UninstallString` (fallback `DisplayIcon`);
  `dir = filepath.Dir(value)`; map `kurogames/wutheringwaves → dir`.
- **Injectable source for testability:** the locator takes a function that yields
  the raw uninstall string (or the resolved dir); the windows impl does the
  registry scan, the test passes a fixture. Registry reader behind `//go:build
  windows`; logic (dir extraction + GameID mapping) platform-neutral + tested.
- `Provider.LocateInstalls(ctx)` delegates, satisfying `core.InstallLocator` so
  App resolution auto-uses it (stat-validated at layer 2).

## Notes
- Local-registry inspection, not a server probe (Collapse-reference posture).
- Confidence: medium-high — the uninstall entry is reliably written by the Kuro
  installer. If a future build drops it, default-scan + manual override still
  cover WuWa.

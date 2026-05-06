# M3.B HoYoverse Genshin Update Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Implement Genshin Impact update / predownload / crash recovery on the HoYoverse backend, mirroring M3.A WuWa parity, distributing HPatchZ as embedded binary for delta patches.

**Architecture:** New `internal/providers/hoyoverse/` files extend the M2 Provider with `core.Updater` + `core.ProcessChecker` impls. PlanPatch path uses staging dir + hpatchz + atomic rename; PlanFull path extracts directly into gameDir Collapse-style. Predownload writes `predl_ready.json` (kurogames-pattern); resume decision table inspects sidecars in priority order (apply.wal > extract_progress.json > progress.json > predl_ready.json). Frontend reuses M3.A's BottomBar / SidebarRow / ConfirmDialog / ToastHost / bell drawer; only i18n + utility helpers + render-logic tweaks needed.

**Tech Stack:** Go (module `omnigate`) / Wails v2.12.0 / Vue 3 + Pinia + vue-i18n / TOML settings via `github.com/pelletier/go-toml/v2` / `go:embed` for hpatchz binary / `golang.org/x/sys/windows` for free-space + process enumeration.

**Spec:** `docs/superpowers/specs/2026-05-06-omnigate-m3b-hoyoverse-update-design.md` (629 lines). Cross-references appear as `(spec §1.2)` etc.

---

## Plan status (2026-05-06 checkpoint)

**Tasks 1-10**: drafted + reviewer-approved through 2 review rounds each.

**Tasks 11-13**: drafted but **review caught 3 BLOCKING type-correctness issues** that need rework before execution:
1. The plan uses `core.PlanNone`/`core.PlanPatch`/`core.PlanFull` constants, but `core.PlanKind` only defines `PlanUpdate` + `PlanPredownload`. Need to introduce a hoyoverse-local `planFlavor` enum (none/patch/full/audio-only) + `genshinPlan` wrapper struct (embeds `*core.UpdatePlan`); the public `Kind` stays `PlanUpdate` for all hoyoverse update flows. Provider keeps a per-game manifestCache so RunUpdate can dispatch on flavor.
2. `core.FileTask.Hash` is the existing field name (algorithm-agnostic per docstring); plan uses `MD5:` — must rename all instances to `Hash:`.
3. `core.UpdatePlan` lacks a `PredownloadAvailable` field; `buildPlan` must return `(*core.UpdatePlan, predlAvailable bool, error)` instead. App-layer `GameUpdateState.PredownloadAvailable` is the canonical surface (already exists in M3.A).

These rework Tasks 12 + 13 substantially (a new `plan_internal.go` file + signature changes). **Implementer should pause and apply these corrections before executing Tasks 11-13.**

**Tasks 14-22**: not yet drafted. Coverage:
- **Task 14**: `update_download.go` — 4-worker pool, byte-range resume, MD5 verify (~5 tests)
- **Task 15**: `update_patch.go` — zip extract / hdiffmap parse / sourceMD5 verify / hpatchz invocation / target verify (~5 tests)
- **Task 16**: `update_apply.go` — applyWAL / atomic rename / cross-volume terminal / deletefiles / config writeback / cleanup (~10 tests)
- **Task 17**: `hoyoverse.go` extension — Provider Updater impl + ProcessChecker impl + resume decision dispatch + self-heal (~4 tests)
- **Task 18**: Frontend i18n + `format.ts` + BottomBar/Topbar/SidebarRow updates
- **Task 19**: Frontend tests (i18n_parity / BottomBar / updates_store / format)
- **Task 20**: Integration tests (9 end-to-end scenarios)
- **Task 21**: Fuzz + bench tests
- **Task 22**: Manual smoke (USER) + tag `v0.4.0-m3b` + merge `--no-ff`

These follow the same TDD bite-sized step pattern. Verbatim code blocks total ~3000-4000 lines of plan content remaining; each task gets per-batch subagent review per the established pattern.

**Recommended resume strategy**:
1. Apply Tasks 11-13 type-correctness rework (1-2 review rounds).
2. Continue writing Tasks 14-22 in batches with iter review (Tasks 14-15 / 16 / 17 / 18-19 / 20-22).
3. Final whole-plan review pass before shipping (M3.A pattern: 4 iterations on the full plan).

---

## Common conventions

These apply across all tasks unless overridden.

### Branching & commits

- Branch: `m3b/spec` (already created when spec was committed). All tasks land on this branch.
- One feature/file per task; commit after each step's tests pass.
- Commit message format: `feat(m3b): <short summary>` for impl; `test(m3b): <short summary>` for test-only commits; `refactor(m3b): ...` for cross-cutting touches.
- No `Co-Authored-By` trailer (per memory `feedback_commits.md`).
- Final merge to `main` uses `--no-ff` (Task 22).

### Testing toolchain

- Go: `go test -count=1 ./...` from repo root. **Never use `-race`** (no CGO on this Windows host; memory `feedback_no_cgo_race.md`).
- Frontend: `cd frontend && npm test` (Vitest). Build sanity: `cd frontend && npm run build`.
- Wails build: `wails build` from repo root. Output: `build/bin/omnigate.exe`.
- Python (only for ad-hoc parsing): `uv run python -c '...'` (memory `feedback_uv_run.md`).

### Shell selection for Task 1 commands

Run Task 1 commands via the **Bash tool** (Git Bash backed), not raw PowerShell. The plan uses `curl`, `unzip`, `shasum`, `head -c` — Unix conventions available through Git Bash. PowerShell equivalents work too but the plan provides the bash form for brevity. Other tasks are pure `go test` / `git` and are shell-agnostic.

### Code review during execution

If using `subagent-driven-development`, each task runs through implementer (haiku) → spec reviewer (haiku) → code-quality reviewer (haiku) loop. Override pattern from M3.A: when reviewers flag plan-verbatim code as issues, override (the reviewer didn't have the plan as context).

### Logger conventions

- Use `slog` via `a.logger` (App-level) or `p.logger` (Provider-level, M2 hoyoverse already wires this).
- Log levels: `Debug` for trace flow, `Info` for state transitions, `Warn` for non-fatal anomalies, `Error` for terminal errors.
- Don't log secrets or tokens. HoYoverse API URLs are tokenless; OK to log.

### Path conventions in tests

- Use `t.TempDir()` for ephemeral test dirs.
- Hardcode game IDs: `core.GameID("hoyoverse/genshin")`; flatten via `strings.Replace(string(gid), "/", "-", 1)`.

### Spec deviation summary (vs original spec text)

The spec mentions "registry init-time panic" for gid format invariant; the actual `internal/app/app.go::registerProvider` (line 112) already returns errors on invalid gids via `core.ParseGameID`. **The plan tightens the existing error path rather than introducing panics** — this is a one-line spec deviation made during plan review (semantically equivalent; panic vs error differs only in severity). Spec §1 wording will be updated post-merge to reflect the implementation.

---

## Task 1: Protocol research + hpatchz binary

**Spec refs:** §0 locked decisions row 3, §7 plan task 1 protocol research dependencies.

**Why first:** Tasks 8 (hpatchz), 11 (version.go extension), 12 (update_manifest.go), 15 (update_patch.go) all assume specific protocol values. This task verifies them once and pins the hpatchz binary.

**Files:**
- Create: `docs/superpowers/research/2026-05-06-m3b-genshin-protocol-validation.md`
- Create: `internal/providers/hoyoverse/third_party_hpatchz/hpatchz.exe` (~250-900KB; sisong/HDiffPatch v4.x)
- Create: `internal/providers/hoyoverse/third_party_hpatchz/LICENSE` (BSD-3)
- Create: `internal/providers/hoyoverse/third_party_hpatchz/README.md` (source URL + tag + SHA-256)

**Note**: binary lives INSIDE the hoyoverse package (not at repo-root `third_party/`) because Go's `go:embed` directive cannot use parent-relative paths. Task 8 embeds via `//go:embed third_party_hpatchz/hpatchz.exe`.

**No TDD discipline** for this task: it produces research artifacts and a binary, not Go code. Sentinel test coverage arrives in Task 8 (verifies embedded binary is non-empty + runs `--help`).

### Step 1.1: Verify byte-range CDN support on a real Genshin blob

- [ ] Query `getGamePackages` to extract a real `game_pkgs[].package` URL:

```bash
curl -s 'https://sg-hyp-api.hoyoverse.com/hyp/hyp-connect/api/getGamePackages?launcher_id=VYTpXlbWo8&game_ids[]=gopR6Cufr3' \
  | uv run python -c 'import json, sys; d = json.load(sys.stdin); pkg = d["data"]["game_packages"][0]["main"]["major"]["game_pkgs"][0]; print(pkg["package"]); print("size", pkg["size"]); print("md5", pkg["md5"])'
```

Expected: prints URL + size in bytes + MD5 hex. Save URL into shell variable `URL` for next steps.

- [ ] HEAD request to confirm `Accept-Ranges: bytes`:

```bash
curl -sI "$URL" | grep -iE 'accept-ranges|content-length|etag'
```

Expected: `Accept-Ranges: bytes` present; `Content-Length: <size>` matches manifest; record whether `ETag:` is present (used by §2 manifest stability check).

- [ ] Range request smoke test:

```bash
curl -s -H 'Range: bytes=0-1023' -o /tmp/genshin-blob-head -D /tmp/genshin-blob-head.headers "$URL"
grep -iE '206|content-range' /tmp/genshin-blob-head.headers
ls -la /tmp/genshin-blob-head
```

Expected: `HTTP/1.1 206 Partial Content`; `Content-Range: bytes 0-1023/<total>`; file size = 1024 bytes.

- [ ] Record results in research doc Step 1 section (template provided in Step 1.7 below).

### Step 1.2: Observe hdiff format on a real patch zip

- [ ] Extract a `main.patches[0].game_pkgs[0].package` URL (FROM-version patch zip):

```bash
curl -s 'https://sg-hyp-api.hoyoverse.com/hyp/hyp-connect/api/getGamePackages?launcher_id=VYTpXlbWo8&game_ids[]=gopR6Cufr3' \
  | uv run python -c 'import json, sys; d = json.load(sys.stdin); ps = d["data"]["game_packages"][0]["main"].get("patches", []); print(ps[0]["version"], ps[0]["game_pkgs"][0]["package"]) if ps else print("NO PATCHES")'
```

If output is `NO PATCHES` (no delta available between current latest and previous version), document this in the research doc and skip Step 1.2 — plan task 15 will validate against fixture data only. M3.B v1 still supports both formats per locked decision.

If a patch URL is printed:
- [ ] Download the first 1MB to inspect zip table-of-contents:

```bash
curl -s -o /tmp/genshin-patch-sample.zip -H 'Range: bytes=0-1048575' '<patch URL from above>'
file /tmp/genshin-patch-sample.zip
unzip -l /tmp/genshin-patch-sample.zip 2>/dev/null | head -50
```

Expected: zip file identifies; listing shows files like `hdiffmap.json` OR `hdifffiles.txt` (one or both), plus `*.hdiff` patch files and `deletefiles.txt`.

- [ ] Record format(s) observed in research doc Step 2 section.

### Step 1.3: Observe audio_pkgs[].language values

```bash
curl -s 'https://sg-hyp-api.hoyoverse.com/hyp/hyp-connect/api/getGamePackages?launcher_id=VYTpXlbWo8&game_ids[]=gopR6Cufr3' \
  | uv run python -c 'import json, sys; d = json.load(sys.stdin); audios = d["data"]["game_packages"][0]["main"]["major"].get("audio_pkgs", []); [print(a["language"], a["size"]) for a in audios]'
```

Expected: printed list of language codes (`zh-cn`, `en-us`, `ja-jp`, `ko-kr` per pre-brainstorm; verify actual values).

- [ ] Record exact strings in research doc Step 3 section.

### Step 1.4: Verify config.ini format on local Genshin install (best effort)

If a local Genshin install exists at `C:\Program Files\Genshin Impact\Genshin Impact game\` (or similar):

- [ ] Read first 200 bytes:

```bash
head -c 200 "/c/Program Files/Genshin Impact/Genshin Impact game/config.ini" \
  | uv run python -c 'import sys; b = sys.stdin.buffer.read(); print(repr(b))'
```

Expected output: contains `[General]` header; `game_version=X.Y.Z` key. Watch for BOM (`\xef\xbb\xbf` prefix) and CRLF (`\r\n`) line endings.

- [ ] Record findings in research doc Step 4 section. If no local install, mark as "deferred to smoke checklist Task 22 / Task 6 fixtures".

### Step 1.5: Pin hpatchz binary

- [ ] Visit https://github.com/sisong/HDiffPatch/releases. Pick the latest stable Windows release. Look for `hpatchz_windows64_v4.X.X.zip` (or current naming). Note the tag (e.g. `v4.10.0`) and download URL.

- [ ] Download release zip:

```bash
mkdir -p internal/providers/hoyoverse/third_party_hpatchz
curl -L -o /tmp/hpatchz-windows.zip '<release zip URL>'
shasum -a 256 /tmp/hpatchz-windows.zip
```

Save the zip's SHA-256 for the README.

- [ ] Extract `hpatchz.exe`:

```bash
unzip -j /tmp/hpatchz-windows.zip 'hpatchz.exe' -d internal/providers/hoyoverse/third_party_hpatchz/
ls -la internal/providers/hoyoverse/third_party_hpatchz/hpatchz.exe
shasum -a 256 internal/providers/hoyoverse/third_party_hpatchz/hpatchz.exe
```

Note actual file size — sisong/HDiffPatch v4.x Windows builds typically run 250-900KB depending on flags. Record actual value for README.

- [ ] Copy upstream `LICENSE` to `internal/providers/hoyoverse/third_party_hpatchz/LICENSE`:

```bash
curl -s -o internal/providers/hoyoverse/third_party_hpatchz/LICENSE 'https://raw.githubusercontent.com/sisong/HDiffPatch/master/LICENSE'
head -5 internal/providers/hoyoverse/third_party_hpatchz/LICENSE
```

Expected: starts with "BSD 3-Clause License" or "Copyright (c) ...".

### Step 1.6: Confirm `.gitignore` doesn't exclude the binary

- [ ] Run:

```bash
git check-ignore -v internal/providers/hoyoverse/third_party_hpatchz/hpatchz.exe; echo "exit=$?"
```

Exit code `1` (no match) is the desired outcome. Exit code `0` would mean the file is ignored; verified that current `.gitignore` does not match this path, so this step is normally a no-op confirmation.

### Step 1.7: Write research doc and README

- [ ] Create `docs/superpowers/research/2026-05-06-m3b-genshin-protocol-validation.md`:

```markdown
# M3.B Genshin Protocol Validation

**Date:** 2026-05-06
**Source:** Live `getGamePackages` probes + a local Genshin install (where available).

## Step 1 — byte-range CDN support

- Probe URL: `<URL from Step 1.1>`
- HEAD `Accept-Ranges: bytes`: ✓ / ✗
- HEAD `Content-Length` matches manifest size: ✓ / ✗
- HEAD `ETag` header: present (value: `<value>`) / absent
- Range `bytes=0-1023` returns `206 Partial Content` + correct `Content-Range`: ✓ / ✗
- **Conclusion:** byte-range resume IS / IS NOT safe to use. Manifest stability comparison uses ETag value when present (per spec §2 sidecar `manifest_etag` field), fingerprint hash fallback otherwise.

## Step 2 — hdiff format

- patches[] available at observation time: yes / no
- (if yes) Zip TOC observed: `hdiffmap.json` ✓ / ✗, `hdifffiles.txt` ✓ / ✗, `deletefiles.txt` ✓ / ✗, `*.hdiff` files ✓ / ✗
- **Conclusion:** v1 supports BOTH formats per locked decision (spec §0). Currently observed: `<modern|legacy|both|unable to verify>`.

## Step 3 — audio_pkgs[].language values

- Observed values: `[<lang1>, <lang2>, ...]`
- **Conclusion:** lang code → AudioAssets folder name mapping table (used in `update_manifest.go::audioLanguageIntersect`):
  | API value | Folder name (Genshin 5.x) |
  |---|---|
  | `<api1>` | `<folder1>` |
  | ... | ... |

## Step 4 — config.ini format

- BOM bytes (`\xef\xbb\xbf` prefix): present / absent
- Line endings: CRLF / LF
- `[General]` header at offset 0 (or after BOM): yes / no
- `game_version=X.Y.Z` line format: matches Collapse `IniConfig.cs` convention ✓ / ✗
- **Conclusion:** `config_ini.go` parser must / must not handle BOM; line ending parsing is `bufio.Scanner` default (handles both LF and CRLF).

## Step 5 — hpatchz binary

See `internal/providers/hoyoverse/third_party_hpatchz/README.md` for tag + SHA-256 + size.
```

Replace `<URL ...>` placeholders with values observed in Steps 1.1-1.4.

- [ ] Create `internal/providers/hoyoverse/third_party_hpatchz/README.md`:

```markdown
# hpatchz binary

This directory ships a pre-built `hpatchz.exe` from [sisong/HDiffPatch](https://github.com/sisong/HDiffPatch), used to apply HoYoverse delta patches in Omnigate's M3.B HoYoverse updater.

## Provenance

- **Upstream:** https://github.com/sisong/HDiffPatch
- **Release tag:** `<TAG_FROM_STEP_1.5>`
- **Release zip URL:** `<ZIP_URL>`
- **Release zip SHA-256:** `<ZIP_SHA256>`
- **`hpatchz.exe` SHA-256:** `<EXE_SHA256>`
- **`hpatchz.exe` size:** `<SIZE>` bytes

## License

BSD-3-Clause. See `LICENSE`.

## How Omnigate uses this binary

`internal/providers/hoyoverse/hpatchz.go` embeds this file via `go:embed`. At first use, Omnigate writes the binary to `<TEMP>/omnigate/hpatchz-<sha8>.exe` (where `<sha8>` is the first 8 hex chars of `sha256.Sum256(embeddedBytes)`) and invokes it via `exec.CommandContext`. The cache is shared across all backends and persists across runs; OS temp cleanup eventually removes orphans.

## Updating

To bump the binary version: replace `hpatchz.exe`, update this README's tag/SHA-256/size, run `go test ./internal/providers/hoyoverse/...` (the SHA-keyed cache filename auto-derives from the new bytes), and commit.
```

Replace placeholder values with actual observations.

### Step 1.8: Commit

```bash
git add internal/providers/hoyoverse/third_party_hpatchz/ docs/superpowers/research/2026-05-06-m3b-genshin-protocol-validation.md
git commit -m "feat(m3b): pin hpatchz binary + protocol validation research

- internal/providers/hoyoverse/third_party_hpatchz/{hpatchz.exe, LICENSE, README.md}
  (~<SIZE>KB; pinned to sisong/HDiffPatch release <TAG>)
- binary lives inside hoyoverse package because go:embed forbids
  parent-relative paths
- protocol validation doc: byte-range CDN, hdiff format, audio lang
  codes, config.ini format observations
- enables Tasks 8/11/12/15 to proceed without re-querying live API"
```

### Step 1.9: Verify

```bash
ls -la internal/providers/hoyoverse/third_party_hpatchz/
go test -count=1 ./... 2>&1 | tail -10
```

Expected: 3 files (`hpatchz.exe` + `LICENSE` + `README.md`); existing tests still GREEN.

---

## Task 2: core.UpdatePlan.Reason enum + kurogames touch

**Spec refs:** §3 update reason tooltip, §4 cross-cutting touches table, §5 backward compat row 1.

**Files:**
- Modify: `internal/core/updater.go` (add ReasonCode enum + UpdatePlan.Reason field)
- Test: `internal/core/updater_test.go` (new, 3 tests)
- Modify: `internal/providers/kurogames/kurogames.go::CheckForUpdateWithProgress` (1-line: set plan.Reason)

### Step 2.1: Read existing core.UpdatePlan shape

- [ ] Read `internal/core/updater.go` to locate the `UpdatePlan` struct and `PlanKind` constants. Identify insertion site (typically end of struct).

### Step 2.2: Write failing tests

- [ ] Create `internal/core/updater_test.go`:

```go
package core

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestReasonCode_Constants(t *testing.T) {
	cases := []struct {
		got, want ReasonCode
	}{
		{ReasonUnspecified, ""},
		{ReasonVersionChanged, "version_changed"},
		{ReasonAudioPackAdded, "audio_pack_added"},
		{ReasonVersionAndAudio, "version_and_audio"},
		{ReasonPredownload, "predownload"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("ReasonCode mismatch: got %q want %q", c.got, c.want)
		}
	}
}

func TestUpdatePlan_Reason_OmitemptyJSON(t *testing.T) {
	// Unspecified reason (zero value "") must not appear in JSON output.
	p := UpdatePlan{Version: "1.0.0", Kind: PlanFull}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := string(b)
	if strings.Contains(out, `"reason"`) {
		t.Errorf("zero-value Reason should be omitted; got %s", out)
	}
}

func TestUpdatePlan_Reason_PopulatedJSON(t *testing.T) {
	p := UpdatePlan{Version: "1.0.0", Kind: PlanFull, Reason: ReasonVersionChanged}
	b, err := json.Marshal(p)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	out := string(b)
	if !strings.Contains(out, `"reason":"version_changed"`) {
		t.Errorf("populated Reason should serialize; got %s", out)
	}
}
```

### Step 2.3: Run; verify FAIL

```bash
go test -count=1 ./internal/core/... 2>&1 | head -20
```

Expected: `undefined: ReasonCode` / `undefined: ReasonUnspecified` etc. Compile errors.

### Step 2.4: Implement

- [ ] Edit `internal/core/updater.go`. Above the existing `UpdatePlan` struct definition, add:

```go
// ReasonCode identifies WHY an update plan was constructed. Used by frontend
// to render appropriate tooltips on the [Update] button. M3.B introduced.
type ReasonCode string

const (
	ReasonUnspecified     ReasonCode = ""
	ReasonVersionChanged  ReasonCode = "version_changed"
	ReasonAudioPackAdded  ReasonCode = "audio_pack_added"
	ReasonVersionAndAudio ReasonCode = "version_and_audio"
	ReasonPredownload     ReasonCode = "predownload"
)
```

- [ ] In the `UpdatePlan` struct, add `Reason` field at the END (preserving existing field order):

```go
type UpdatePlan struct {
	// ... existing fields preserved verbatim ...

	// Reason identifies why this plan was constructed. Frontend renders
	// it as a tooltip on the [Update] button. Empty string (ReasonUnspecified)
	// is the M3.A-era zero value; frontend renders no tooltip in that case.
	Reason ReasonCode `json:"reason,omitempty"`
}
```

### Step 2.5: Run; verify PASS

```bash
go test -count=1 ./internal/core/... 2>&1 | tail -10
```

Expected: all 3 new tests PASS plus existing core tests still PASS.

### Step 2.6: Touch kurogames CheckForUpdateWithProgress

The actual gid → plan construction lives in `internal/providers/kurogames/kurogames.go::CheckForUpdateWithProgress` (line ~184). The function builds `plan := core.UpdatePlan{...}` near the end and returns it.

- [ ] Read `internal/providers/kurogames/kurogames.go` and locate the `plan := core.UpdatePlan{...}` literal in `CheckForUpdateWithProgress`. Add one line setting `plan.Reason` AFTER the literal, BEFORE the `return plan, nil`:

```go
	plan := core.UpdatePlan{
		// ... all existing fields preserved ...
	}
	plan.Reason = core.ReasonVersionChanged // M3.B forward-consistency: kurogames is always version-change driven
	return plan, nil
```

(If the literal is `&core.UpdatePlan{...}` returned through a pointer, set via `plan.Reason = ...` against the pointer same way.)

### Step 2.7: Verify kurogames tests still pass

```bash
go test -count=1 ./internal/providers/kurogames/... 2>&1 | tail -10
```

Expected: ALL existing kurogames tests PASS (the field addition is forward-compatible; adding a value to a previously-zero field doesn't break existing assertions unless they specifically check `Reason == ""`).

### Step 2.8: Whole-repo build check

```bash
go build ./...
```

Expected: clean build.

### Step 2.9: Commit

```bash
git add internal/core/updater.go internal/core/updater_test.go internal/providers/kurogames/kurogames.go
git commit -m "feat(core): add UpdatePlan.Reason ReasonCode for M3.B tooltips

- 5 const ReasonCode values (Unspecified/VersionChanged/AudioPackAdded/
  VersionAndAudio/Predownload)
- omitempty JSON tag preserves M3.A wire compat
- kurogames CheckForUpdateWithProgress populates ReasonVersionChanged
  for forward-consistency
- 3 unit tests: const values + omitempty + populated marshaling"
```

---

## Task 3: HoyoverseSettings.TempDir + tempDirFor + ParseGameID strengthening

**Spec refs:** §1 app-layer changes (last 4 rows), §4 cross-cutting touches.

**Plan deviation from spec:** The spec says "registry init-time panic" for gid format invariant; the actual `internal/app/app.go::registerProvider` already returns errors on invalid gids (line 112 onward). Rather than introduce panics, we **tighten the existing `core.ParseGameID` to reject multi-slash suffixes** (currently allows `"hoyoverse/genshin/cn"`) and rely on registerProvider's existing error path. Semantically equivalent to spec; cheaper.

**Files:**
- Modify: `internal/app/settings.go` (HoyoverseSettings: add TempDir field)
- Modify: `internal/app/settings.go` (defaultSettings + load migration: ensure TempDir defaults to "")
- Modify: `internal/app/app.go::tempDirFor` (add hoyoverse case + comment update)
- Modify: `internal/core/provider.go::ParseGameID` (reject multi-slash suffix)
- Test: `internal/core/provider_test.go` (extend with multi-slash rejection cases)
- Test: `internal/app/settings_test.go` (extend with HoyoverseSettings.TempDir round-trip + omitempty)

### Step 3.1: Add HoyoverseSettings.TempDir field

- [ ] Read `internal/app/settings.go` lines 30-33 (current `HoyoverseSettings` shape: `Path` + `Region`).

- [ ] Edit `internal/app/settings.go` to add `TempDir`:

```go
type HoyoverseSettings struct {
	Path    string `toml:"path"`
	Region  string `toml:"region"`
	TempDir string `toml:"temp_dir,omitempty"` // M3.B: empty → runtime default <TEMP>/omnigate/hoyoverse/
}
```

### Step 3.2: Verify hoyoverseRawTOML migration shim doesn't need TempDir

- [ ] Read lines 41-48 (current `hoyoverseRawTOML` migration shim, used for legacy `hoyoplay_path` → `path`). M3.B does NOT need TempDir migration (M2 settings.toml has no `temp_dir` under `[backends.hoyoverse]`; on Load it defaults to ""). No change to shim.

### Step 3.3: Write failing tests for TempDir

- [ ] In `internal/app/settings_test.go` (extend), add:

```go
func TestSettings_HoyoverseSettings_TempDir_RoundTrip(t *testing.T) {
	s := Settings{
		Version: 1,
		Backends: BackendSettings{
			Hoyoverse: HoyoverseSettings{
				Path:    `C:\Program Files\HoYoPlay`,
				Region:  "global",
				TempDir: `D:\genshin-temp`,
			},
		},
	}
	data, err := toml.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var s2 Settings
	if err := toml.Unmarshal(data, &s2); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if s2.Backends.Hoyoverse.TempDir != `D:\genshin-temp` {
		t.Errorf("TempDir round-trip lost: %q", s2.Backends.Hoyoverse.TempDir)
	}
}

func TestSettings_HoyoverseSettings_TempDir_Omitempty(t *testing.T) {
	s := Settings{
		Version: 1,
		Backends: BackendSettings{
			Hoyoverse: HoyoverseSettings{Path: `C:\Program Files\HoYoPlay`, Region: "global"},
			// TempDir omitted → zero value ""
		},
	}
	data, err := toml.Marshal(s)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "temp_dir") {
		t.Errorf("zero-value TempDir should be omitted; got:\n%s", string(data))
	}
}
```

Add `"github.com/pelletier/go-toml/v2"` and `"strings"` to imports if not already present.

### Step 3.4: Run; verify PASS

```bash
go test -count=1 -run 'TestSettings_HoyoverseSettings_TempDir' ./internal/app/... 2>&1 | tail -10
```

Expected: both tests PASS.

### Step 3.5: Add hoyoverse case to tempDirFor

- [ ] Read `internal/app/app.go` lines 420-441 (current `tempDirFor` shape).

- [ ] Edit to add hoyoverse case + update the outdated `unreachable in v0.3.1` comment:

```go
// tempDirFor resolves the per-backend temp root for sidecar/staging files.
//
// kurogames returns <TEMP>/omnigate (flat, bit-exact preservation per
// the legacy kurogamesTempDir helper).
// hoyoverse returns <TEMP>/omnigate/hoyoverse (subdir-per-backend; M3.B).
// Future backends (M3.C hypergryph, M3.D HSR/ZZZ) follow the default
// branch unless they add a settings TempDir field.
func (a *App) tempDirFor(backend core.BackendID, gid core.GameID) string {
	switch backend {
	case kurogames.BackendID:
		if td := a.settings.Backends.Kurogames.TempDir; td != "" {
			return td
		}
		return filepath.Join(osTempDir(), "omnigate")
	case hoyoverse.BackendID:
		if td := a.settings.Backends.Hoyoverse.TempDir; td != "" {
			return td
		}
		return filepath.Join(osTempDir(), "omnigate", "hoyoverse")
	}
	// Default for backends without a settings TempDir field: per-backend subdir
	// to avoid collisions.
	return filepath.Join(osTempDir(), "omnigate", string(backend))
}
```

Verify the `hoyoverse` import is present (M2 should have wired it; check `grep -n "providers/hoyoverse" internal/app/app.go`). If absent, add to imports:

```go
import (
	// ... existing imports ...
	"omnigate/internal/providers/hoyoverse"
)
```

### Step 3.6: Build sanity

```bash
go build ./...
```

Expected: clean build.

### Step 3.7: Strengthen core.ParseGameID

- [ ] Read `internal/core/provider.go` lines 156-168 (current `ParseGameID`).

- [ ] Edit to reject multi-slash suffixes (the existing `SplitN(s, "/", 2)` allows `"hoyoverse/genshin/cn"` because parts[1] = `"genshin/cn"` is non-empty):

```go
// ParseGameID splits a GameID of the form "<backend>/<suffix>" into its
// components. Returns an error if the format is invalid (missing slash,
// empty backend, empty suffix, OR suffix contains '/' — gids must be
// exactly two segments separated by exactly one slash).
//
// The format is part of the contract: front-end stores, App routing,
// asset URLs, and scanForRecovery's flatten/unflatten logic all depend
// on it.
func ParseGameID(s GameID) (BackendID, string, error) {
	parts := strings.SplitN(string(s), "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid game id %q (want <backend>/<suffix>)", s)
	}
	if strings.ContainsRune(parts[1], '/') {
		return "", "", fmt.Errorf("invalid game id %q (suffix must not contain '/')", s)
	}
	return BackendID(parts[0]), parts[1], nil
}
```

### Step 3.8: Write failing test for strengthened ParseGameID

- [ ] In `internal/core/provider_test.go` (create if absent; otherwise extend), add:

```go
func TestParseGameID_RejectsMultiSlashSuffix(t *testing.T) {
	cases := []struct {
		gid     GameID
		wantErr bool
	}{
		{"hoyoverse/genshin", false},                   // valid
		{"hoyoverse/genshin/cn", true},                 // multi-slash → reject
		{"hoyoverse", true},                            // no slash → reject
		{"hoyoverse/", true},                           // empty suffix → reject
		{"/genshin", true},                             // empty backend → reject
		{"hoyoverse/genshin-impact-cn-rev1", false},    // dashes OK
	}
	for _, c := range cases {
		t.Run(string(c.gid), func(t *testing.T) {
			_, _, err := ParseGameID(c.gid)
			gotErr := err != nil
			if gotErr != c.wantErr {
				t.Errorf("ParseGameID(%q): err=%v wantErr=%v", c.gid, err, c.wantErr)
			}
		})
	}
}
```

If `provider_test.go` doesn't exist, create it with the package declaration:

```go
package core

import "testing"
```

### Step 3.9: Run; verify PASS

```bash
go test -count=1 -run TestParseGameID_RejectsMultiSlashSuffix ./internal/core/... 2>&1 | tail -10
```

Expected: PASS for all 6 sub-cases.

### Step 3.10: Verify registerProvider error path catches strengthened gids

`registerProvider` (app.go:112) calls `core.ParseGameID` for each `g.ID` in `p.Games()`. Tightening ParseGameID automatically tightens registerProvider's invariant — no further app.go changes needed.

- [ ] Add a regression test in `internal/app/app_test.go`:

```go
func TestRegisterProvider_RejectsMultiSlashGID(t *testing.T) {
	a := &App{
		settings: Settings{Version: 1},
		logger:   slog.Default(),
	}
	bad := &fakeProviderForRegister{
		id:    "hoyoverse",
		games: []core.GameDescriptor{{ID: "hoyoverse/genshin/cn"}}, // multi-slash → invalid
	}
	err := a.registerProvider(bad)
	if err == nil {
		t.Errorf("expected error for multi-slash gid; got nil")
	}
}

// fakeProviderForRegister is a minimal core.Provider stub for invariant tests.
type fakeProviderForRegister struct {
	id    core.BackendID
	games []core.GameDescriptor
}

func (f *fakeProviderForRegister) ID() core.BackendID                                                   { return f.id }
func (f *fakeProviderForRegister) DisplayName() core.LocalizedString                                    { return core.LocalizedString{} }
func (f *fakeProviderForRegister) Games() []core.GameDescriptor                                         { return f.games }
func (f *fakeProviderForRegister) SettingsSchema() []core.SettingField                                  { return nil }
func (f *fakeProviderForRegister) DetectInstall(ctx context.Context) ([]core.InstalledGame, error)      { return nil, nil }
func (f *fakeProviderForRegister) GetIcon(ctx context.Context, gid core.GameID) (string, error)         { return "", nil }
func (f *fakeProviderForRegister) GetBackgrounds(ctx context.Context, gid core.GameID) ([]core.Background, error) { return nil, nil }
func (f *fakeProviderForRegister) CheckVersion(ctx context.Context, gid core.GameID) (core.VersionInfo, error)     { return core.VersionInfo{}, nil }
func (f *fakeProviderForRegister) Launch(ctx context.Context, gid core.GameID, opts core.LaunchOptions) (int, error) { return 0, nil }
```

Add `"context"` and `"log/slog"` to test imports if not already present.

### Step 3.11: Run; verify PASS

```bash
go test -count=1 -run 'TestRegisterProvider_RejectsMultiSlashGID|TestSettings_HoyoverseSettings_TempDir|TestParseGameID_RejectsMultiSlash' ./... 2>&1 | tail -20
```

Expected: all new tests PASS.

### Step 3.12: Whole-repo regression check

```bash
go test -count=1 ./... 2>&1 | tail -30
```

Expected: every existing test still PASSES. The strengthened ParseGameID rejects strings none of the M2/M3.A providers actually use (`kurogames/wuthering-waves`, `hoyoverse/genshin`, etc., are all single-slash).

### Step 3.13: Commit

```bash
git add internal/app/settings.go internal/app/app.go internal/app/settings_test.go internal/app/app_test.go internal/core/provider.go internal/core/provider_test.go
git commit -m "feat(m3b): HoyoverseSettings.TempDir + tempDirFor case + ParseGameID strengthening

- HoyoverseSettings.TempDir field (omitempty, no schema bump)
- tempDirFor: hoyoverse arm returns <TEMP>/omnigate/hoyoverse
- core.ParseGameID rejects multi-slash suffix (e.g. hoyoverse/x/y)
- registerProvider's existing error path inherits the strengthening
- 2 settings tests + 6 ParseGameID cases + 1 registerProvider test
- spec deviation: panic→error (functional equivalence; spec wording will be
  updated post-merge)"
```

---

## Task 4: scanForRecovery generalization

**Spec refs:** §1 app-layer changes (scanForRecovery row), §2 resume decision table.

**Files:**
- Modify: `internal/app/update_handler_windows.go` (convert osTempDir/osReadDir from func to var for test seam)
- Modify: `internal/app/update_handler_other.go` (same)
- Modify: `internal/app/update_handler.go::scanForRecovery` (generalize to per-backend) + add `applyRecoveryStateOverride` test seam + `knownBackendIDs` helper
- Test: `internal/app/update_handler_test.go` (new file or extension; add cross-backend scan test + kurogames-prefix-survives regression)

### Step 4.1: Convert osTempDir/osReadDir to vars (test seam)

- [ ] Read `internal/app/update_handler_windows.go` lines 14-17 + `update_handler_other.go` lines 7-9 (current func declarations).

- [ ] Edit `update_handler_windows.go`:

```go
var osTempDir = func() string { return os.TempDir() }
var osReadDir = func(path string) ([]os.DirEntry, error) { return os.ReadDir(path) }
```

- [ ] Edit `update_handler_other.go`:

```go
var osTempDir = func() string { return os.TempDir() }
var osReadDir = func(path string) ([]os.DirEntry, error) { return os.ReadDir(path) }
```

(Both files keep the build tag `//go:build windows` / `//go:build !windows` at top; only the kind-of-declaration changes from `func` to `var`.)

### Step 4.2: Verify build still passes

```bash
go build ./...
go test -count=1 ./internal/app/... 2>&1 | tail -10
```

Expected: clean build; existing tests still PASS (callers of `osTempDir()` / `osReadDir(p)` work unchanged because Go calls function-typed vars with the same syntax).

### Step 4.3: Add knownBackendIDs helper + applyRecoveryStateOverride test seam

- [ ] Edit `internal/app/update_handler.go`. Near `applyRecoveryState` (around line 682), add the test seam variable:

```go
// applyRecoveryStateOverride is a test seam used by scanForRecovery tests
// to assert which sidecar dirs the walker visits without exercising the
// full RecoveryPhase routing logic. Production code never sets this.
var applyRecoveryStateOverride func(gid core.GameID, sidecarDir string)
```

- [ ] Modify `applyRecoveryState` to consult the override:

```go
func (a *App) applyRecoveryState(gid core.GameID, sidecarDir string) {
	if applyRecoveryStateOverride != nil {
		applyRecoveryStateOverride(gid, sidecarDir)
		return
	}
	// ... existing M3.A body verbatim ...
}
```

- [ ] Add `knownBackendIDs` helper near `scanForRecovery`:

```go
// knownBackendIDs returns a set of registered backend IDs as plain strings.
// Used by scanForRecovery to skip <TEMP>/omnigate/<otherBackend>/ subdirs
// during the kurogames flat-root walk: kurogames root <TEMP>/omnigate/
// happens to be a parent of hoyoverse's <TEMP>/omnigate/hoyoverse/ subdir,
// so a naive walker would treat "hoyoverse" as a candidate game directory.
// Cross-backend gid collision is structurally impossible by ParseGameID
// strengthening (Task 3); this filter eliminates noise.
func (a *App) knownBackendIDs() map[string]struct{} {
	out := make(map[string]struct{}, len(a.providers))
	for _, p := range a.providers {
		out[string(p.ID())] = struct{}{}
	}
	return out
}
```

### Step 4.4: Generalize scanForRecovery

- [ ] Replace the existing `scanForRecovery` body (around line 648). New shape:

```go
// scanForRecovery walks every registered backend's per-backend temp root,
// scanning each <root>/<gameIDFlat>/<version>/ for sidecars and seeding
// per-game state (interrupted_resume / predl_ready) via applyRecoveryState.
//
// Tree shape per backend: <tempDirFor(backend, "")>/<gameIDFlat>/<version>/
// where <gameIDFlat> = strings.Replace(string(gid), "/", "-", 1).
//
// kurogames flat root <TEMP>/omnigate/ may contain sibling backend
// subdirs (e.g. <TEMP>/omnigate/hoyoverse/); knownBackendIDs filter
// skips them to suppress noise.
func (a *App) scanForRecovery() {
	skipNames := a.knownBackendIDs()
	for _, p := range a.providers {
		root := a.tempDirFor(p.ID(), "")
		a.scanForRecoveryRoot(p.ID(), root, skipNames)
	}
}

func (a *App) scanForRecoveryRoot(backend core.BackendID, root string, skipNames map[string]struct{}) {
	a.logger.Debug("scanForRecovery: enter", "backend", backend, "root", root)
	gameDirs, err := osReadDir(root)
	if err != nil {
		a.logger.Debug("scanForRecovery: no temp dir (first-run normal)", "backend", backend, "err", err)
		return
	}
	for _, gameDir := range gameDirs {
		if !gameDir.IsDir() {
			continue
		}
		gameIDFlat := gameDir.Name()
		// Skip sibling backends' subdirs (kurogames flat root case only).
		if _, isBackendName := skipNames[gameIDFlat]; isBackendName {
			continue
		}
		gid := core.GameID(strings.Replace(gameIDFlat, "-", "/", 1))
		p, err := a.provider(gid)
		if err != nil {
			a.logger.Debug("scanForRecovery: skip unknown game dir", "backend", backend, "dir", gameIDFlat, "err", err)
			continue
		}
		// Defense-in-depth: the resolved provider must own this root.
		if p.ID() != backend {
			a.logger.Debug("scanForRecovery: cross-backend dir; skipping", "backend", backend, "gid", gid, "owner", p.ID())
			continue
		}
		gameDirPath := filepath.Join(root, gameIDFlat)
		versionDirs, err := osReadDir(gameDirPath)
		if err != nil {
			continue
		}
		for _, vDir := range versionDirs {
			if !vDir.IsDir() {
				continue
			}
			sidecarDir := filepath.Join(gameDirPath, vDir.Name())
			a.logger.Debug("scanForRecovery: scanning sidecar dir", "backend", backend, "game", gid, "dir", sidecarDir)
			a.applyRecoveryState(gid, sidecarDir)
		}
	}
}
```

### Step 4.5: Build + run M3.A regression

```bash
go build ./...
go test -count=1 ./internal/app/... 2>&1 | tail -20
```

Expected: clean build; ALL existing app tests PASS. The kurogames-only behavior is preserved (single-provider walk produces identical output).

### Step 4.6: Add buildAppForTest helper

- [ ] If `internal/app/update_handler_test.go` doesn't exist, create it. Add this helper (the helper supports the new tests in Step 4.7):

```go
package app

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"omnigate/internal/core"
)

// buildAppForTest creates an *App with both kurogames and hoyoverse
// providers registered (each with one canonical game ID). Used by
// scanForRecovery cross-backend tests.
func buildAppForTest(t *testing.T) *App {
	t.Helper()
	a := &App{
		settings: Settings{Version: 1},
		logger:   slog.New(slog.NewTextHandler(os.Stderr, nil)),
	}
	if err := a.registerProvider(&minimalProviderForScan{
		id:   "kurogames",
		gids: []core.GameID{"kurogames/wutheringwaves"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := a.registerProvider(&minimalProviderForScan{
		id:   "hoyoverse",
		gids: []core.GameID{"hoyoverse/genshin"},
	}); err != nil {
		t.Fatal(err)
	}
	return a
}

// minimalProviderForScan is the Provider stub for buildAppForTest.
type minimalProviderForScan struct {
	id   core.BackendID
	gids []core.GameID
}

func (m *minimalProviderForScan) ID() core.BackendID            { return m.id }
func (m *minimalProviderForScan) DisplayName() core.LocalizedString { return core.LocalizedString{} }
func (m *minimalProviderForScan) Games() []core.GameDescriptor {
	out := make([]core.GameDescriptor, len(m.gids))
	for i, g := range m.gids {
		out[i] = core.GameDescriptor{ID: g}
	}
	return out
}
func (m *minimalProviderForScan) SettingsSchema() []core.SettingField                                       { return nil }
func (m *minimalProviderForScan) DetectInstall(ctx context.Context) ([]core.InstalledGame, error)           { return nil, nil }
func (m *minimalProviderForScan) GetIcon(ctx context.Context, gid core.GameID) (string, error)              { return "", nil }
func (m *minimalProviderForScan) GetBackgrounds(ctx context.Context, gid core.GameID) ([]core.Background, error) { return nil, nil }
func (m *minimalProviderForScan) CheckVersion(ctx context.Context, gid core.GameID) (core.VersionInfo, error)     { return core.VersionInfo{}, nil }
func (m *minimalProviderForScan) Launch(ctx context.Context, gid core.GameID, opts core.LaunchOptions) (int, error) { return 0, nil }
```

(The minimal stub is similar to Task 3's `fakeProviderForRegister` but with multi-game support.)

### Step 4.7: Write tests

- [ ] Add to `internal/app/update_handler_test.go`:

```go
func TestScanForRecovery_CrossBackend_FiltersBackendNames(t *testing.T) {
	tmp := t.TempDir()

	// Layout simulating real:
	//   <tmp>/omnigate/                                          ← kurogames root (flat)
	//   <tmp>/omnigate/kurogames-wutheringwaves/3.0.0/progress.json  (kuro game)
	//   <tmp>/omnigate/hoyoverse/                                ← hoyoverse root (subdir)
	//   <tmp>/omnigate/hoyoverse/hoyoverse-genshin/5.6.0/progress.json (hoyo game)
	for _, p := range []string{
		filepath.Join(tmp, "omnigate", "kurogames-wutheringwaves", "3.0.0"),
		filepath.Join(tmp, "omnigate", "hoyoverse", "hoyoverse-genshin", "5.6.0"),
	} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "progress.json"),
			[]byte(`{"game_id":"","version":"","etag":"","entries":{}}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	prevTempDir := osTempDir
	osTempDir = func() string { return tmp }
	defer func() { osTempDir = prevTempDir }()

	a := buildAppForTest(t)

	visited := []string{}
	prevApply := applyRecoveryStateOverride
	applyRecoveryStateOverride = func(gid core.GameID, dir string) {
		visited = append(visited, string(gid)+":"+filepath.Base(dir))
	}
	defer func() { applyRecoveryStateOverride = prevApply }()

	a.scanForRecovery()

	// Expect exactly 2 visited dirs: one kuro, one hoyo. NOT 3 (no extra
	// "hoyoverse" treated as kurogames-flat gameDir).
	if len(visited) != 2 {
		t.Fatalf("expected 2 visited dirs, got %d: %v", len(visited), visited)
	}
}

func TestScanForRecovery_KurogamesPrefixDirSurvives(t *testing.T) {
	tmp := t.TempDir()

	// Layout: only a kurogames-prefixed dir exists; hoyoverse provider
	// is registered but no hoyoverse temp tree. The scan must still
	// visit the kurogames game dir.
	for _, p := range []string{
		filepath.Join(tmp, "omnigate", "kurogames-wutheringwaves", "3.0.0"),
	} {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "progress.json"),
			[]byte(`{"game_id":"","version":"","etag":"","entries":{}}`), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	prevTempDir := osTempDir
	osTempDir = func() string { return tmp }
	defer func() { osTempDir = prevTempDir }()

	a := buildAppForTest(t)

	visited := []string{}
	prevApply := applyRecoveryStateOverride
	applyRecoveryStateOverride = func(gid core.GameID, dir string) {
		visited = append(visited, string(gid))
	}
	defer func() { applyRecoveryStateOverride = prevApply }()

	a.scanForRecovery()

	if len(visited) != 1 || visited[0] != "kurogames/wutheringwaves" {
		t.Fatalf("expected 1 visit to kurogames/wutheringwaves, got %v", visited)
	}
}
```

### Step 4.8: Run; verify PASS

```bash
go test -count=1 -run 'TestScanForRecovery' ./internal/app/... 2>&1 | tail -10
```

Expected: both tests PASS.

### Step 4.9: Whole-repo regression

```bash
go test -count=1 ./... 2>&1 | tail -20
```

Expected: every existing test PASSES.

### Step 4.10: Commit

```bash
git add internal/app/update_handler_windows.go internal/app/update_handler_other.go internal/app/update_handler.go internal/app/update_handler_test.go
git commit -m "refactor(app): generalize scanForRecovery to per-backend roots

- iterate a.providers, scan each tempDirFor root independently
- knownBackendIDs filter avoids walking sibling backend subdirs from
  kurogames flat root (<TEMP>/omnigate/{hoyoverse,hypergryph,...})
- defense-in-depth: cross-check resolved provider matches backend root
- osTempDir / osReadDir converted func→var for test seam (M3.A pattern
  preserved; callers unchanged)
- applyRecoveryStateOverride test seam allows asserting walker visits
- 2 new tests: cross-backend dir filtering + kurogames-prefix survives
- M3.A kurogames recovery behavior preserved (single-provider walk
  produces identical output)"
```

---

## Task 5: Path helpers + loadJSONSidecar generic

**Spec refs:** §1 path helpers, §2 sidecar field naming convention + loadJSONSidecar.

**Files:**
- Create: `internal/providers/hoyoverse/sidecar_paths.go`
- Test: `internal/providers/hoyoverse/sidecar_paths_test.go`

### Step 5.1: Write failing tests

- [ ] Create `internal/providers/hoyoverse/sidecar_paths_test.go`:

```go
package hoyoverse

import (
	"os"
	"path/filepath"
	"testing"

	"omnigate/internal/core"
)

// testSample is hoisted to package level to keep test functions tidy.
type testSample struct {
	X int `json:"x"`
}

func TestGameSidecarDir(t *testing.T) {
	got := gameSidecarDir(`C:\temp\omnigate\hoyoverse`, core.GameID("hoyoverse/genshin"))
	want := filepath.Join(`C:\temp\omnigate\hoyoverse`, "hoyoverse-genshin")
	if got != want {
		t.Errorf("gameSidecarDir: got %q want %q", got, want)
	}
}

func TestVersionSidecarDir(t *testing.T) {
	got := versionSidecarDir(`C:\temp\omnigate\hoyoverse`, core.GameID("hoyoverse/genshin"), "5.7.0")
	want := filepath.Join(`C:\temp\omnigate\hoyoverse`, "hoyoverse-genshin", "5.7.0")
	if got != want {
		t.Errorf("versionSidecarDir: got %q want %q", got, want)
	}
}

func TestLoadJSONSidecar_Missing(t *testing.T) {
	got, err := loadJSONSidecar[testSample](filepath.Join(t.TempDir(), "nonexistent.json"))
	if err != nil {
		t.Fatalf("expected nil err for ENOENT, got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil pointer, got %v", got)
	}
}

func TestLoadJSONSidecar_Corrupt(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "corrupt.json")
	if err := os.WriteFile(path, []byte(`{not json`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := loadJSONSidecar[testSample](path)
	if err != nil {
		t.Fatalf("expected nil err for corrupt (warn+remove), got %v", err)
	}
	if got != nil {
		t.Errorf("expected nil pointer, got %v", got)
	}
	// File should have been os.Remove'd.
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Errorf("expected corrupt file removed, stat err: %v", err)
	}
}

func TestLoadJSONSidecar_Valid(t *testing.T) {
	tmp := t.TempDir()
	path := filepath.Join(tmp, "valid.json")
	if err := os.WriteFile(path, []byte(`{"x":42}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := loadJSONSidecar[testSample](path)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if got == nil || got.X != 42 {
		t.Errorf("expected X=42, got %+v", got)
	}
}
```

### Step 5.2: Run; verify FAIL

```bash
go test -count=1 ./internal/providers/hoyoverse/... 2>&1 | head -10
```

Expected: compile errors (`undefined: gameSidecarDir`, `undefined: loadJSONSidecar`).

### Step 5.3: Implement

- [ ] Create `internal/providers/hoyoverse/sidecar_paths.go`:

```go
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
```

### Step 5.4: Run; verify PASS

```bash
go test -count=1 ./internal/providers/hoyoverse/... 2>&1 | tail -10
```

Expected: 5 tests PASS.

### Step 5.5: Commit

```bash
git add internal/providers/hoyoverse/sidecar_paths.go internal/providers/hoyoverse/sidecar_paths_test.go
git commit -m "feat(m3b/hoyoverse): sidecar path helpers + loadJSONSidecar generic

- gameSidecarDir / versionSidecarDir / flatGameID per spec §1
- loadJSONSidecar[T any] with corrupt-file warn+remove (matches
  recovery.go pattern)
- 5 unit tests covering missing / corrupt / valid + path resolution
- M3.C will promote to core/sidecar_paths.go when 3rd consumer arrives"
```

---

## Task 6: config_ini.go (Genshin config.ini reader/writer)

**Spec refs:** §1 file table row `config_ini.go`, §2 Stage A step 3 (`ReadGameVersion` for currentVer), §2 Stage F step 7 PlanPatch / Stage F step 5 PlanFull (`WriteGameVersion` post-apply).

**Files:**
- Create: `internal/providers/hoyoverse/config_ini.go`
- Test: `internal/providers/hoyoverse/config_ini_test.go`

### Step 6.1: Write failing tests (8 cases)

- [ ] Create `internal/providers/hoyoverse/config_ini_test.go`:

```go
package hoyoverse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadGameVersion_Happy(t *testing.T) {
	dir := t.TempDir()
	body := "[General]\nchannel=1\nsub_channel=1\ncps=mihoyo\ngame_version=5.6.0\n"
	if err := os.WriteFile(filepath.Join(dir, "config.ini"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadGameVersion(dir)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "5.6.0" {
		t.Errorf("got %q want 5.6.0", got)
	}
}

func TestReadGameVersion_MissingFile(t *testing.T) {
	_, err := ReadGameVersion(t.TempDir())
	if err == nil {
		t.Error("expected error for missing config.ini")
	}
}

func TestReadGameVersion_MissingGeneralSection(t *testing.T) {
	dir := t.TempDir()
	body := "[Other]\ngame_version=5.6.0\n"
	if err := os.WriteFile(filepath.Join(dir, "config.ini"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadGameVersion(dir)
	if err == nil {
		t.Error("expected error when [General] is absent")
	}
}

func TestReadGameVersion_MissingKey(t *testing.T) {
	dir := t.TempDir()
	body := "[General]\nchannel=1\n"
	if err := os.WriteFile(filepath.Join(dir, "config.ini"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadGameVersion(dir)
	if err == nil {
		t.Error("expected error when game_version key absent")
	}
}

func TestReadGameVersion_BOM(t *testing.T) {
	dir := t.TempDir()
	body := "\xef\xbb\xbf[General]\ngame_version=5.6.0\n"
	if err := os.WriteFile(filepath.Join(dir, "config.ini"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadGameVersion(dir)
	if err != nil {
		t.Fatalf("err with BOM: %v", err)
	}
	if got != "5.6.0" {
		t.Errorf("got %q want 5.6.0", got)
	}
}

func TestReadGameVersion_CRLF(t *testing.T) {
	dir := t.TempDir()
	body := "[General]\r\ngame_version=5.6.0\r\n"
	if err := os.WriteFile(filepath.Join(dir, "config.ini"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadGameVersion(dir)
	if err != nil {
		t.Fatalf("err with CRLF: %v", err)
	}
	if got != "5.6.0" {
		t.Errorf("got %q want 5.6.0", got)
	}
}

func TestReadGameVersion_CommentsAndKeyOnlyLines(t *testing.T) {
	dir := t.TempDir()
	body := "; comment line\n[General]\n# another comment\nflag_only_no_equals\ngame_version=5.6.0\n"
	if err := os.WriteFile(filepath.Join(dir, "config.ini"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadGameVersion(dir)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "5.6.0" {
		t.Errorf("got %q want 5.6.0", got)
	}
}

func TestWriteGameVersion_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	body := "[General]\nchannel=1\nsub_channel=1\ncps=mihoyo\ngame_version=5.6.0\n"
	if err := os.WriteFile(filepath.Join(dir, "config.ini"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteGameVersion(dir, "5.7.0"); err != nil {
		t.Fatalf("write err: %v", err)
	}
	got, err := ReadGameVersion(dir)
	if err != nil {
		t.Fatalf("read err: %v", err)
	}
	if got != "5.7.0" {
		t.Errorf("got %q want 5.7.0", got)
	}
	// Other keys preserved
	data, err := os.ReadFile(filepath.Join(dir, "config.ini"))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"channel=1", "sub_channel=1", "cps=mihoyo"} {
		if !strings.Contains(string(data), key) {
			t.Errorf("expected key %q preserved; got:\n%s", key, string(data))
		}
	}
}

func TestWriteGameVersion_PreservesCRLFLineEndings(t *testing.T) {
	dir := t.TempDir()
	body := "[General]\r\nchannel=1\r\ngame_version=5.6.0\r\n"
	if err := os.WriteFile(filepath.Join(dir, "config.ini"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteGameVersion(dir, "5.7.0"); err != nil {
		t.Fatalf("write err: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.ini"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\r\n") {
		t.Errorf("expected CRLF line endings preserved; got:\n%q", string(data))
	}
}

func TestWriteGameVersion_MissingFileError(t *testing.T) {
	if err := WriteGameVersion(t.TempDir(), "5.7.0"); err == nil {
		t.Error("expected error when writing to missing config.ini")
	}
}
```

10 tests total covering the spec's 8-10 minimum.

### Step 6.2: Run; verify FAIL

```bash
go test -count=1 ./internal/providers/hoyoverse/... 2>&1 | head -10
```

Expected: compile errors (`undefined: ReadGameVersion`, `undefined: WriteGameVersion`).

### Step 6.3: Implement

- [ ] Create `internal/providers/hoyoverse/config_ini.go`:

```go
package hoyoverse

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// configININame is the filename Genshin's launcher writes to the game directory.
const configININame = "config.ini"

// utf8BOM is the 3-byte UTF-8 BOM that some Windows tools prepend.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// ReadGameVersion reads the [General].game_version value from
// <gameDir>/config.ini. Errors:
//   - file missing → fmt.Errorf wrapping fs.ErrNotExist
//   - [General] section absent → error
//   - game_version key absent within [General] → error
//
// Tolerates UTF-8 BOM, CRLF / LF line endings, comments (;/# prefixes),
// and lines without `=` (skipped silently).
func ReadGameVersion(gameDir string) (string, error) {
	path := filepath.Join(gameDir, configININame)
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read %s: %w", path, err)
	}
	data = bytes.TrimPrefix(data, utf8BOM)

	inGeneral := false
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ";") || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]") {
			inGeneral = strings.EqualFold(line, "[General]")
			continue
		}
		if !inGeneral {
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq < 0 {
			continue
		}
		key := strings.TrimSpace(line[:eq])
		val := strings.TrimSpace(line[eq+1:])
		if key == "game_version" {
			return val, nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("scan %s: %w", path, err)
	}
	if !inGeneral {
		// We never entered [General] (file may have other sections only).
		return "", fmt.Errorf("config.ini at %s: [General] section not found", gameDir)
	}
	return "", fmt.Errorf("config.ini at %s: game_version key not found in [General]", gameDir)
}

// WriteGameVersion replaces the [General].game_version value in
// <gameDir>/config.ini, preserving all other lines verbatim. The file
// must already exist (read-modify-write semantics; we don't synthesize
// a new config.ini from scratch — risk of clobbering launcher state).
//
// Atomic write: write to <path>.tmp + os.Rename. Line endings are
// preserved from the source (split by LF; if any source line ends with
// '\r', the entire file is written CRLF).
func WriteGameVersion(gameDir, version string) error {
	path := filepath.Join(gameDir, configININame)
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", path, err)
	}
	bom := []byte{}
	if bytes.HasPrefix(data, utf8BOM) {
		bom = utf8BOM
		data = data[len(utf8BOM):]
	}

	// Detect line ending convention by sampling first occurrence.
	useCRLF := bytes.Contains(data, []byte("\r\n"))
	lineSep := "\n"
	if useCRLF {
		lineSep = "\r\n"
	}

	// Split, modify, re-join.
	rawLines := bytes.Split(data, []byte("\n"))
	inGeneral := false
	replaced := false
	out := make([][]byte, 0, len(rawLines))
	for _, raw := range rawLines {
		line := raw
		// Strip trailing \r if CRLF; we'll re-add via lineSep.
		if len(line) > 0 && line[len(line)-1] == '\r' {
			line = line[:len(line)-1]
		}
		trimmed := strings.TrimSpace(string(line))
		if strings.HasPrefix(trimmed, "[") && strings.HasSuffix(trimmed, "]") {
			inGeneral = strings.EqualFold(trimmed, "[General]")
			out = append(out, line)
			continue
		}
		if inGeneral && !replaced {
			eq := strings.IndexByte(trimmed, '=')
			if eq > 0 && strings.TrimSpace(trimmed[:eq]) == "game_version" {
				// Preserve original indent (if any) — Genshin's writer doesn't indent
				// but be conservative.
				out = append(out, []byte("game_version="+version))
				replaced = true
				continue
			}
		}
		out = append(out, line)
	}
	if !replaced {
		return fmt.Errorf("config.ini at %s: game_version key not found in [General]; refusing to synthesize", gameDir)
	}

	// Rejoin with detected separator. Preserve trailing newline if source had one
	// (last element of rawLines is empty when source ended with \n).
	body := bytes.Join(out, []byte(lineSep))

	tmpPath := path + ".tmp"
	w := bytes.Buffer{}
	w.Write(bom)
	w.Write(body)
	if err := os.WriteFile(tmpPath, w.Bytes(), 0o644); err != nil {
		return fmt.Errorf("write tmp %s: %w", tmpPath, err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("rename %s → %s: %w", tmpPath, path, err)
	}
	return nil
}
```

### Step 6.4: Run; verify PASS

```bash
go test -count=1 ./internal/providers/hoyoverse/... 2>&1 | tail -15
```

Expected: 10 config_ini tests PASS plus existing sidecar_paths tests still PASS.

### Step 6.5: Commit

```bash
git add internal/providers/hoyoverse/config_ini.go internal/providers/hoyoverse/config_ini_test.go
git commit -m "feat(m3b/hoyoverse): config_ini.go reader+writer with BOM/CRLF tolerance

- ReadGameVersion / WriteGameVersion per spec §2 Stage A + Stage F
- bufio.Scanner-based parser; tolerates UTF-8 BOM, LF/CRLF, ;# comments
- WriteGameVersion: read-modify-write with atomic rename; preserves
  line endings + BOM + all non-target keys
- 10 unit tests covering happy path / missing file/section/key /
  BOM / CRLF / comments / round-trip / line-ending preservation"
```

---

## Task 7: audio_packs.go (lang folder detection)

**Spec refs:** §1 file table `audio_packs.go`, §2 Stage B audio language intersect.

**Files:**
- Create: `internal/providers/hoyoverse/audio_packs.go`
- Test: `internal/providers/hoyoverse/audio_packs_test.go`

### Step 7.1: Write failing tests (4 cases)

- [ ] Create `internal/providers/hoyoverse/audio_packs_test.go`:

```go
package hoyoverse

import (
	"os"
	"path/filepath"
	"testing"
)

// audioAssetsRel mirrors what the impl uses; fixture builder echoes it.
const testAudioAssetsRel = "GenshinImpact_Data/StreamingAssets/AudioAssets"

func TestDetectInstalledLanguages_None(t *testing.T) {
	dir := t.TempDir()
	got, err := DetectInstalledLanguages(dir)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 0 {
		t.Errorf("expected empty slice, got %v", got)
	}
}

func TestDetectInstalledLanguages_OneLang(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, testAudioAssetsRel, "Chinese"), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := DetectInstalledLanguages(dir)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 1 || got[0] != "Chinese" {
		t.Errorf("expected [Chinese], got %v", got)
	}
}

func TestDetectInstalledLanguages_MultipleLangs(t *testing.T) {
	dir := t.TempDir()
	for _, lang := range []string{"Chinese", "English(US)", "Japanese", "Korean"} {
		if err := os.MkdirAll(filepath.Join(dir, testAudioAssetsRel, lang), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	got, err := DetectInstalledLanguages(dir)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 4 {
		t.Errorf("expected 4 langs, got %d: %v", len(got), got)
	}
	// Result should be sorted alphabetically (per spec).
	for i := 1; i < len(got); i++ {
		if got[i-1] > got[i] {
			t.Errorf("not sorted: %v", got)
		}
	}
}

func TestDetectInstalledLanguages_IgnoresFiles(t *testing.T) {
	dir := t.TempDir()
	audioDir := filepath.Join(dir, testAudioAssetsRel)
	if err := os.MkdirAll(audioDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Add a real lang dir + a stray file (e.g. audio_lang_14 indirection file).
	if err := os.MkdirAll(filepath.Join(audioDir, "Chinese"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(audioDir, "audio_lang_14"), []byte("Chinese\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := DetectInstalledLanguages(dir)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(got) != 1 || got[0] != "Chinese" {
		t.Errorf("expected files ignored, got %v", got)
	}
}
```

### Step 7.2: Run; verify FAIL

```bash
go test -count=1 ./internal/providers/hoyoverse/... 2>&1 | head -10
```

Expected: `undefined: DetectInstalledLanguages`.

### Step 7.3: Implement

- [ ] Create `internal/providers/hoyoverse/audio_packs.go`:

```go
package hoyoverse

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
)

// audioAssetsRel is the path (relative to gameDir) where Genshin keeps voice
// pack subfolders. One subfolder per installed language. The literal subfolder
// names (e.g. "Chinese", "English(US)") are observed at runtime; M3.B does
// NOT lock specific names — DetectInstalledLanguages returns whatever the
// filesystem shows. Mapping to manifest audio_pkgs[].language values lives
// in update_manifest.go::audioLanguageIntersect.
const audioAssetsRel = "GenshinImpact_Data/StreamingAssets/AudioAssets"

// DetectInstalledLanguages enumerates installed voice pack folders under
// <gameDir>/<audioAssetsRel>/. Returns sorted slice of subfolder names
// (used by spec §2 last_apply_target.json drift detection + audio_pkg
// intersect). Empty slice if the parent dir doesn't exist (no voice
// packs installed) — NOT an error.
func DetectInstalledLanguages(gameDir string) ([]string, error) {
	root := filepath.Join(gameDir, audioAssetsRel)
	entries, err := os.ReadDir(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return []string{}, nil
		}
		return nil, err
	}
	out := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out, nil
}
```

### Step 7.4: Run; verify PASS

```bash
go test -count=1 ./internal/providers/hoyoverse/... 2>&1 | tail -10
```

Expected: 4 audio_packs tests PASS.

### Step 7.5: Commit

```bash
git add internal/providers/hoyoverse/audio_packs.go internal/providers/hoyoverse/audio_packs_test.go
git commit -m "feat(m3b/hoyoverse): audio_packs.go DetectInstalledLanguages

- list subfolders under <gameDir>/GenshinImpact_Data/StreamingAssets/AudioAssets/
- sorted alphabetically; ignores files (e.g. audio_lang_14 indirection)
- empty slice (no error) if parent dir absent
- 4 unit tests: none / one / multiple / files-ignored"
```

---

## Task 8: hpatchz.go (embed + sync.Once extract + Run)

**Spec refs:** §1 file table `hpatchz.go`, §2 Stage E patching loop, §6 cross-backend cache placement.

**Depends on:** Task 1 (binary committed at `internal/providers/hoyoverse/third_party_hpatchz/hpatchz.exe`).

**Files:**
- Create: `internal/providers/hoyoverse/hpatchz.go`
- Test: `internal/providers/hoyoverse/hpatchz_test.go`

### Step 8.1: Write failing tests

- [ ] Create `internal/providers/hoyoverse/hpatchz_test.go`:

```go
package hoyoverse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestEmbeddedHpatchzNonEmpty(t *testing.T) {
	if len(embeddedHpatchz) == 0 {
		t.Fatal("embeddedHpatchz is empty; Task 1 may not have committed the binary")
	}
	if len(embeddedHpatchz) < 100*1024 {
		t.Errorf("embeddedHpatchz size %d bytes is suspiciously small (<100KB); expected ~250-900KB", len(embeddedHpatchz))
	}
}

func TestEmbeddedSHAComputed(t *testing.T) {
	want := sha256.Sum256(embeddedHpatchz)
	got := embeddedHpatchzSHA()
	if hex.EncodeToString(want[:]) != got {
		t.Errorf("SHA mismatch: want %s got %s", hex.EncodeToString(want[:]), got)
	}
	// First 8 hex chars used as cache filename suffix.
	if len(got) < 8 {
		t.Fatal("sha256 hex too short")
	}
}

func TestExtractHpatchzOnce_CachesAndReuses(t *testing.T) {
	tmp := t.TempDir()
	prevTempDir := osTempDirHpatchz
	osTempDirHpatchz = func() string { return tmp }
	defer func() { osTempDirHpatchz = prevTempDir }()
	resetHpatchzExtractOnce()

	path1, err := extractHpatchzOnce(context.Background())
	if err != nil {
		t.Fatalf("first extract: %v", err)
	}
	if !strings.Contains(path1, embeddedHpatchzSHA()[:8]) {
		t.Errorf("path %q should contain SHA-8 prefix %q", path1, embeddedHpatchzSHA()[:8])
	}
	stat1, err := os.Stat(path1)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	mtime1 := stat1.ModTime()

	// Wait a tick to detect any rewrite.
	time.Sleep(20 * time.Millisecond)

	path2, err := extractHpatchzOnce(context.Background())
	if err != nil {
		t.Fatalf("second extract: %v", err)
	}
	if path1 != path2 {
		t.Errorf("expected same path, got %q vs %q", path1, path2)
	}
	stat2, err := os.Stat(path2)
	if err != nil {
		t.Fatal(err)
	}
	if !stat2.ModTime().Equal(mtime1) {
		t.Errorf("file rewrite suspected; mtime changed: %v → %v", mtime1, stat2.ModTime())
	}
}

func TestRunHpatchz_BogusFiles(t *testing.T) {
	tmp := t.TempDir()
	prevTempDir := osTempDirHpatchz
	osTempDirHpatchz = func() string { return tmp }
	defer func() { osTempDirHpatchz = prevTempDir }()
	resetHpatchzExtractOnce()

	// Run with non-existent input paths to exercise the full Run pipeline:
	//   1. ctx not canceled → reaches extract
	//   2. extractHpatchzOnce succeeds (writes binary)
	//   3. exec.CommandContext(... -f <bogus> <bogus> <bogus>) runs
	//   4. hpatchz exits non-zero (cannot open input)
	//   5. Run returns error wrapping ExitError + stderr
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	err := Run(ctx,
		filepath.Join(tmp, "nonexistent_old"),
		filepath.Join(tmp, "nonexistent_diff"),
		filepath.Join(tmp, "nonexistent_new"),
	)
	if err == nil {
		t.Fatal("expected error from hpatchz with bogus input paths")
	}
	// Verify error wraps stderr / exit-code info (loose check; substring may
	// vary by hpatchz version but "hpatchz" should appear in our wrap).
	if !strings.Contains(err.Error(), "hpatchz") {
		t.Errorf("expected error mentions hpatchz; got %v", err)
	}
}
```

`Run` is the public entry; `extractHpatchzOnce` + `embeddedHpatchz` + `embeddedHpatchzSHA` + `osTempDirHpatchz` + `resetHpatchzExtractOnce` + `newHpatchzCmd` are unexported / test-only seams declared in `hpatchz.go`.

### Step 8.2: Run; verify FAIL

```bash
go test -count=1 -run 'TestEmbeddedHpatchz|TestExtractHpatchz|TestRunHpatchz' ./internal/providers/hoyoverse/... 2>&1 | head -10
```

Expected: compile errors.

### Step 8.3: Implement

- [ ] Create `internal/providers/hoyoverse/hpatchz.go`:

```go
package hoyoverse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	_ "embed"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

//go:embed third_party_hpatchz/hpatchz.exe
var embeddedHpatchz []byte

// embeddedHpatchzSHA returns the SHA-256 hex of the embedded binary.
// Computed once at package init via sync.Once-guarded var below.
var (
	hpatchzSHAOnce sync.Once
	hpatchzSHA     string
)

func embeddedHpatchzSHA() string {
	hpatchzSHAOnce.Do(func() {
		sum := sha256.Sum256(embeddedHpatchz)
		hpatchzSHA = hex.EncodeToString(sum[:])
	})
	return hpatchzSHA
}

// osTempDirHpatchz is a test seam returning os.TempDir() in production.
var osTempDirHpatchz = func() string { return os.TempDir() }

// extractOnce gates first-use binary extraction. resetHpatchzExtractOnce
// resets it for tests; production code never calls reset.
var (
	hpatchzExtractOnce *sync.Once = &sync.Once{}
	hpatchzExtractPath string
	hpatchzExtractErr  error
)

func resetHpatchzExtractOnce() {
	hpatchzExtractOnce = &sync.Once{}
	hpatchzExtractPath = ""
	hpatchzExtractErr = nil
}

// extractHpatchzOnce writes embeddedHpatchz to <TEMP>/omnigate/hpatchz-<sha8>.exe
// (cross-backend shared cache; spec §1 hpatchz.go row, §2 sidecar tree). The
// SHA-keyed filename means a binary upgrade auto-invalidates the cache.
//
// Idempotent: if the target file already exists with matching size, reuse;
// else write atomically (tmpfile + rename). Returns the absolute path.
func extractHpatchzOnce(ctx context.Context) (string, error) {
	hpatchzExtractOnce.Do(func() {
		sha8 := embeddedHpatchzSHA()[:8]
		dir := filepath.Join(osTempDirHpatchz(), "omnigate")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			hpatchzExtractErr = fmt.Errorf("mkdir %s: %w", dir, err)
			return
		}
		path := filepath.Join(dir, "hpatchz-"+sha8+".exe")
		// Reuse if already present + correct size.
		if stat, err := os.Stat(path); err == nil && stat.Size() == int64(len(embeddedHpatchz)) {
			hpatchzExtractPath = path
			return
		}
		// Atomic write.
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, embeddedHpatchz, 0o755); err != nil {
			hpatchzExtractErr = fmt.Errorf("write %s: %w", tmp, err)
			return
		}
		if err := os.Rename(tmp, path); err != nil {
			_ = os.Remove(tmp)
			hpatchzExtractErr = fmt.Errorf("rename %s → %s: %w", tmp, path, err)
			return
		}
		hpatchzExtractPath = path
	})
	if hpatchzExtractErr != nil {
		return "", hpatchzExtractErr
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return hpatchzExtractPath, nil
}

// newHpatchzCmd is a test seam for constructing the exec.Cmd. Keeps tests
// from binding to specific CommandContext flags.
func newHpatchzCmd(ctx context.Context, hpatchzPath string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, hpatchzPath, args...)
}

// Run invokes hpatchz to apply <diffFile> to <oldFile>, producing <newFile>.
// Cancel propagates via ctx (exec.CommandContext machinery kills the process
// on ctx.Done()).
//
// hpatchz argument order: hpatchz [options] <oldFile> <diffFile> <outNewFile>
// Per HDiffPatch v4 docs (and Collapse Launcher's invocation pattern).
//
// Returns nil on zero exit code; otherwise error wrapping exec.ExitError
// with stderr.
func Run(ctx context.Context, oldFile, diffFile, newFile string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	hpatchzPath, err := extractHpatchzOnce(ctx)
	if err != nil {
		return err
	}
	cmd := newHpatchzCmd(ctx, hpatchzPath, "-f", oldFile, diffFile, newFile)
	out, err := cmd.CombinedOutput()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return fmt.Errorf("hpatchz exit %d: %w; output: %s", ee.ExitCode(), err, string(out))
		}
		return fmt.Errorf("hpatchz exec: %w; output: %s", err, string(out))
	}
	return nil
}
```

### Step 8.4: Verify embed path matches Task 1 binary location

`go:embed third_party_hpatchz/hpatchz.exe` is interpreted relative to the package directory containing `hpatchz.go`. Task 1 placed the binary at `internal/providers/hoyoverse/third_party_hpatchz/hpatchz.exe`. Confirm:

```bash
ls -la internal/providers/hoyoverse/third_party_hpatchz/hpatchz.exe
```

Expected: file exists. If absent, Task 1 must be re-run with the correct path before Task 8 can compile.

### Step 8.5: Run; verify PASS

```bash
go test -count=1 -run 'TestEmbeddedHpatchz|TestExtractHpatchz|TestRunHpatchz' ./internal/providers/hoyoverse/... 2>&1 | tail -15
```

Expected: 4 hpatchz tests PASS (`TestEmbeddedHpatchzNonEmpty`, `TestEmbeddedSHAComputed`, `TestExtractHpatchzOnce_CachesAndReuses`, `TestRunHpatchz_BogusFiles`).

### Step 8.6: Commit

```bash
git add internal/providers/hoyoverse/hpatchz.go internal/providers/hoyoverse/hpatchz_test.go
git commit -m "feat(m3b/hoyoverse): hpatchz.go embed + sync.Once cache + Run

- go:embed third_party_hpatchz/hpatchz.exe (cached as embeddedHpatchz)
- init-time SHA-256 → first-8-hex cache filename
- extractHpatchzOnce: idempotent write to <TEMP>/omnigate/hpatchz-<sha8>.exe
- Run(ctx, old, diff, new): exec.CommandContext-wrapped invocation
- 4 unit tests: non-empty embed / SHA computation / extract idempotent /
  bogus-files runs (exercises argv ordering + stderr capture)"
```

---

## Task 9: apply_lock.go (per-version exclusive lock; mirrors kurogames pattern)

**Spec refs:** §1 file table `apply_lock.go`. Spec says `<tempRoot>/<gid>/<version>/apply.lock`.

**Mirror kurogames pattern verbatim**: lowercase `applyLock` interface with `Acquire(versionDir string) error` + `Release() error` methods; `newApplyLock()` constructor + `platformApplyLock()` per-OS factory; `windows.UnlockFileEx` before close.

**Plan deviation from spec wording**: spec text says "lockfile name `apply.lock`"; kurogames uses `.lc_update.lock`. M3.B uses `apply.lock` (per spec) — the filename divergence between backends is acceptable since each backend has its own version dir.

**Files:**
- Create: `internal/providers/hoyoverse/apply_lock.go` (interface + factory)
- Create: `internal/providers/hoyoverse/apply_lock_windows.go` (LockFileEx impl)
- Create: `internal/providers/hoyoverse/apply_lock_other.go` (POSIX stub)
- Test: `internal/providers/hoyoverse/apply_lock_test.go`

### Step 9.1: Read kurogames apply_lock pattern

- [ ] Read these files for verbatim reference:
  - `internal/providers/kurogames/apply_lock.go`
  - `internal/providers/kurogames/apply_lock_windows.go`
  - `internal/providers/kurogames/apply_lock_other.go`

### Step 9.2: Write failing tests

- [ ] Create `internal/providers/hoyoverse/apply_lock_test.go`:

```go
package hoyoverse

import (
	"testing"
)

func TestApplyLock_AcquireRelease(t *testing.T) {
	versionDir := t.TempDir()
	lock := newApplyLock()
	if err := lock.Acquire(versionDir); err != nil {
		t.Fatalf("acquire: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Errorf("release: %v", err)
	}
}

func TestApplyLock_DoubleAcquireFails(t *testing.T) {
	versionDir := t.TempDir()
	lock1 := newApplyLock()
	if err := lock1.Acquire(versionDir); err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	defer lock1.Release()

	lock2 := newApplyLock()
	err := lock2.Acquire(versionDir)
	if err == nil {
		t.Error("expected error on double acquire against same versionDir")
	}
}

func TestApplyLock_ReleaseAndReacquire(t *testing.T) {
	versionDir := t.TempDir()
	lock1 := newApplyLock()
	if err := lock1.Acquire(versionDir); err != nil {
		t.Fatalf("first: %v", err)
	}
	if err := lock1.Release(); err != nil {
		t.Fatalf("release: %v", err)
	}
	lock2 := newApplyLock()
	if err := lock2.Acquire(versionDir); err != nil {
		t.Fatalf("re-acquire: %v", err)
	}
	if err := lock2.Release(); err != nil {
		t.Errorf("re-release: %v", err)
	}
}
```

### Step 9.3: Run; verify FAIL

```bash
go test -count=1 -run TestApplyLock ./internal/providers/hoyoverse/... 2>&1 | head -5
```

Expected: `undefined: newApplyLock`.

### Step 9.4: Implement interface + factory

- [ ] Create `internal/providers/hoyoverse/apply_lock.go`:

```go
package hoyoverse

// applyLock guards Stage F's per-version apply phase against concurrent
// runs (e.g. a second Omnigate instance, or stale state after crash).
//
// Windows: file lock on `<versionDir>/apply.lock` via LockFileEx with
// LOCKFILE_EXCLUSIVE_LOCK | LOCKFILE_FAIL_IMMEDIATELY.
// Non-Windows: stub for tests on CI Linux runners (returns nil error;
// real Genshin updates only run on Windows production hosts).
//
// Mirrors internal/providers/kurogames/apply_lock.go.
type applyLock interface {
	Acquire(versionDir string) error
	Release() error
}

func newApplyLock() applyLock {
	return platformApplyLock()
}
```

### Step 9.5: Implement Windows + other

- [ ] Create `internal/providers/hoyoverse/apply_lock_windows.go`:

```go
//go:build windows

package hoyoverse

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/windows"
)

const lockFileName = "apply.lock"

func platformApplyLock() applyLock {
	return &windowsApplyLock{}
}

type windowsApplyLock struct {
	file *os.File
}

func (w *windowsApplyLock) Acquire(versionDir string) error {
	if w.file != nil {
		return errors.New("applyLock already acquired")
	}
	lockPath := filepath.Join(versionDir, lockFileName)
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return fmt.Errorf("open lock file: %w", err)
	}
	var overlapped windows.Overlapped
	flags := uint32(windows.LOCKFILE_EXCLUSIVE_LOCK | windows.LOCKFILE_FAIL_IMMEDIATELY)
	if err := windows.LockFileEx(windows.Handle(f.Fd()), flags, 0, 1, 0, &overlapped); err != nil {
		_ = f.Close()
		return fmt.Errorf("acquire apply lock: %w", err)
	}
	w.file = f
	return nil
}

func (w *windowsApplyLock) Release() error {
	if w.file == nil {
		return nil
	}
	var overlapped windows.Overlapped
	_ = windows.UnlockFileEx(windows.Handle(w.file.Fd()), 0, 1, 0, &overlapped)
	err := w.file.Close()
	w.file = nil
	return err
}
```

- [ ] Create `internal/providers/hoyoverse/apply_lock_other.go`:

```go
//go:build !windows

package hoyoverse

func platformApplyLock() applyLock {
	return &otherApplyLock{}
}

type otherApplyLock struct{}

// Acquire is a no-op on non-Windows. Hoyoverse runs on Windows production
// hosts; this stub exists to keep `go build ./...` clean on Linux/macOS
// dev machines.
func (l *otherApplyLock) Acquire(versionDir string) error { return nil }
func (l *otherApplyLock) Release() error                  { return nil }
```

### Step 9.6: Verify

```bash
go build ./...
go test -count=1 -run TestApplyLock ./internal/providers/hoyoverse/... 2>&1 | tail -10
```

Expected: build clean; 3 tests PASS on Windows.

### Step 9.7: Commit

```bash
git add internal/providers/hoyoverse/apply_lock.go internal/providers/hoyoverse/apply_lock_windows.go internal/providers/hoyoverse/apply_lock_other.go internal/providers/hoyoverse/apply_lock_test.go
git commit -m "feat(m3b/hoyoverse): apply_lock per-version exclusive lock (kurogames mirror)

- applyLock interface (lowercase, internal) + newApplyLock() factory
- platformApplyLock() per-OS dispatch
- LockFileEx with EXCLUSIVE | FAIL_IMMEDIATELY; UnlockFileEx in Release
- lockFileName 'apply.lock' (vs kurogames '.lc_update.lock')
- 3 unit tests: acquire+release / double-acquire fails / reacquire after release
- mirrors internal/providers/kurogames/apply_lock*.go pattern verbatim"
```

---

## Task 10: process_check (GenshinImpact.exe detection)

**Spec refs:** §1 file table `process_check_*.go`, §3 process_blocked error.

**Files:**
- Create: `internal/providers/hoyoverse/process_check_windows.go`
- Create: `internal/providers/hoyoverse/process_check_other.go`
- Test: `internal/providers/hoyoverse/process_check_test.go` (Windows-only build tag)

### Step 10.1: Read kurogames process_check pattern

- [ ] Read `internal/providers/kurogames/process_check_windows.go` and `process_check_other.go` to understand the EnumProcesses pattern.

### Step 10.2: Write failing test

- [ ] Create `internal/providers/hoyoverse/process_check_test.go`:

```go
//go:build windows

package hoyoverse

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlatformIsProcessRunning_NotRunning(t *testing.T) {
	if platformIsProcessRunning("definitely_not_a_real_exe_name_xyz123.exe") {
		t.Error("expected false for nonexistent process name")
	}
}

func TestPlatformIsProcessRunning_SelfPID(t *testing.T) {
	// The currently-running test binary should be detected.
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	if !platformIsProcessRunning(filepath.Base(exe)) {
		t.Errorf("expected true for self process name (%s); enumeration may have failed", filepath.Base(exe))
	}
}
```

### Step 10.3: Run; verify FAIL

```bash
go test -count=1 -run 'TestPlatformIsProcessRunning' ./internal/providers/hoyoverse/... 2>&1 | head -10
```

Expected: `undefined: platformIsProcessRunning`.

### Step 10.4: Implement (mirrors kurogames CreateToolhelp32Snapshot pattern)

- [ ] Create `internal/providers/hoyoverse/process_check_windows.go`:

```go
//go:build windows

package hoyoverse

import (
	"strings"
	"unsafe"

	"golang.org/x/sys/windows"
)

// platformIsProcessRunning enumerates Windows processes via
// CreateToolhelp32Snapshot + Process32First/Next, comparing the executable
// basename case-insensitively to `exeName`. Mirrors
// internal/providers/kurogames/process_check_windows.go pattern verbatim.
//
// Returns true if any matching process is found; false on no match OR
// any enumeration error (errors are swallowed — used as a best-effort
// gate, not a security check).
func platformIsProcessRunning(exeName string) bool {
	snapshot, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return false
	}
	defer windows.CloseHandle(snapshot)

	var entry windows.ProcessEntry32
	entry.Size = uint32(unsafe.Sizeof(entry))
	if err := windows.Process32First(snapshot, &entry); err != nil {
		return false
	}
	for {
		exe := windows.UTF16ToString(entry.ExeFile[:])
		if strings.EqualFold(exe, exeName) {
			return true
		}
		if err := windows.Process32Next(snapshot, &entry); err != nil {
			break
		}
	}
	return false
}
```

- [ ] Create `internal/providers/hoyoverse/process_check_other.go`:

```go
//go:build !windows

package hoyoverse

func platformIsProcessRunning(exeName string) bool {
	return false
}
```

### Step 10.5: Run; verify PASS

```bash
go test -count=1 -run 'TestPlatformIsProcessRunning' ./internal/providers/hoyoverse/... 2>&1 | tail -10
```

Expected: 2 tests PASS on Windows.

### Step 10.6: Commit

```bash
git add internal/providers/hoyoverse/process_check_windows.go internal/providers/hoyoverse/process_check_other.go internal/providers/hoyoverse/process_check_test.go
git commit -m "feat(m3b/hoyoverse): process_check GenshinImpact.exe detection

- platformIsProcessRunning via CreateToolhelp32Snapshot + Process32First/Next
  (mirrors kurogames pattern verbatim — same API choice rationale: no
  privileges needed, reliable basename, broad Win10/11 compatibility)
- case-insensitive basename comparison
- 2 unit tests: nonexistent name → false / self process name → true
- non-Windows stub returns false"
```

---

## Task 11: version.go extension (full manifest parser)

**Spec refs:** §1 file table `version.go (extended)` row, §2 Stage A step 2 manifest parse.

**Depends on:** Task 1 (research observed which manifest fields are present).

**Files:**
- Modify: `internal/providers/hoyoverse/version.go` (extend `rawGamePackages` shape)
- Test: `internal/providers/hoyoverse/version_test.go` (extend with new field assertions)
- Create: `internal/providers/hoyoverse/testdata/manifest-sample.json` (sanitized fixture)

### Step 11.1: Read current version.go shape

- [ ] Read `internal/providers/hoyoverse/version.go` lines 14-72 to see the current `rawGamePackages` parser. M3.B extends — does NOT replace.

### Step 11.2: Write fixture

- [ ] Create `internal/providers/hoyoverse/testdata/manifest-sample.json` (synthesized from Task 1 protocol research; sanitize URLs to `https://example.invalid/...`):

```json
{
  "retcode": 0,
  "message": "OK",
  "data": {
    "game_packages": [
      {
        "game": { "id": "gopR6Cufr3", "biz": "hk4e_global" },
        "main": {
          "major": {
            "version": "5.7.0",
            "game_pkgs": [
              {
                "url": "https://example.invalid/genshin_5.7.0_main_1.zip",
                "md5": "abcdef0123456789abcdef0123456789",
                "size": "12345678900",
                "decompressed_size": "30000000000"
              }
            ],
            "audio_pkgs": [
              {
                "language": "zh-cn",
                "url": "https://example.invalid/genshin_5.7.0_audio_zh-cn.zip",
                "md5": "1111111111111111aaaaaaaaaaaaaaaa",
                "size": "5000000000",
                "decompressed_size": "8000000000"
              },
              {
                "language": "en-us",
                "url": "https://example.invalid/genshin_5.7.0_audio_en-us.zip",
                "md5": "2222222222222222bbbbbbbbbbbbbbbb",
                "size": "5500000000",
                "decompressed_size": "8500000000"
              }
            ],
            "res_list_url": "https://example.invalid/genshin_5.7.0_reslist.json"
          },
          "patches": [
            {
              "version": "5.6.0",
              "game_pkgs": [
                {
                  "url": "https://example.invalid/genshin_5.6.0_to_5.7.0_main.zip",
                  "md5": "ccccccccccccccccdddddddddddddddd",
                  "size": "2500000000",
                  "decompressed_size": "3500000000"
                }
              ],
              "audio_pkgs": [
                {
                  "language": "zh-cn",
                  "url": "https://example.invalid/genshin_5.6.0_to_5.7.0_audio_zh-cn.zip",
                  "md5": "eeeeeeeeeeeeeeeeffffffffffffffff",
                  "size": "300000000",
                  "decompressed_size": "500000000"
                }
              ],
              "res_list_url": "https://example.invalid/genshin_5.6.0_to_5.7.0_reslist.json"
            }
          ]
        },
        "pre_download": {
          "major": {
            "version": "5.8.0",
            "game_pkgs": [
              {
                "url": "https://example.invalid/genshin_5.8.0_main_1.zip",
                "md5": "0000000000000000111111111111111122",
                "size": "13000000000",
                "decompressed_size": "31000000000"
              }
            ],
            "audio_pkgs": [],
            "res_list_url": ""
          },
          "patches": []
        }
      }
    ]
  }
}
```

Replace placeholder URLs with `example.invalid` (RFC 6761 reserved); md5/size values are synthetic but plausible.

### Step 11.3: Write failing tests

- [ ] In `internal/providers/hoyoverse/version_test.go` (extend existing), add:

```go
func TestParseGamePackages_WithPatchesAndPreDownload(t *testing.T) {
	data, err := os.ReadFile("testdata/manifest-sample.json")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := parseGamePackagesResponse(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(resp.Data.GamePackages) != 1 {
		t.Fatalf("expected 1 game_package; got %d", len(resp.Data.GamePackages))
	}
	gp := resp.Data.GamePackages[0]

	// main.major
	if gp.Main.Major.Version != "5.7.0" {
		t.Errorf("main.major.version = %q want 5.7.0", gp.Main.Major.Version)
	}
	if len(gp.Main.Major.GamePkgs) != 1 {
		t.Errorf("main.major.game_pkgs len = %d want 1", len(gp.Main.Major.GamePkgs))
	}
	if len(gp.Main.Major.AudioPkgs) != 2 {
		t.Errorf("main.major.audio_pkgs len = %d want 2", len(gp.Main.Major.AudioPkgs))
	}
	// Audio nested check
	if gp.Main.Major.AudioPkgs[0].Language != "zh-cn" {
		t.Errorf("audio[0].language = %q want zh-cn", gp.Main.Major.AudioPkgs[0].Language)
	}

	// main.patches[]
	if len(gp.Main.Patches) != 1 {
		t.Fatalf("main.patches len = %d want 1", len(gp.Main.Patches))
	}
	if gp.Main.Patches[0].Version != "5.6.0" {
		t.Errorf("patches[0].version = %q want 5.6.0 (FROM)", gp.Main.Patches[0].Version)
	}

	// pre_download.major
	if gp.PreDownload == nil {
		t.Fatal("expected pre_download non-nil")
	}
	if gp.PreDownload.Major.Version != "5.8.0" {
		t.Errorf("pre_download.major.version = %q want 5.8.0", gp.PreDownload.Major.Version)
	}
}

func TestParseGamePackages_NoPreDownload(t *testing.T) {
	// Manifest without pre_download field — common between patch days.
	body := `{"retcode":0,"message":"OK","data":{"game_packages":[{"game":{"id":"gopR6Cufr3","biz":"hk4e_global"},"main":{"major":{"version":"5.6.0","game_pkgs":[],"audio_pkgs":[]},"patches":[]}}]}}`
	resp, err := parseGamePackagesResponse([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.Data.GamePackages[0].PreDownload != nil {
		t.Error("expected pre_download nil when absent")
	}
}

func TestParseGamePackages_DecompressedSizeStringToInt64(t *testing.T) {
	// HoYoverse API returns size fields as JSON strings (e.g. "12345678900").
	// Verify our parser converts to int64 correctly.
	data, err := os.ReadFile("testdata/manifest-sample.json")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := parseGamePackagesResponse(data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg := resp.Data.GamePackages[0].Main.Major.GamePkgs[0]
	if pkg.DecompressedSize != 30000000000 {
		t.Errorf("decompressed_size = %d want 30000000000", pkg.DecompressedSize)
	}
}
```

### Step 11.4: Run; verify FAIL

```bash
go test -count=1 -run 'TestParseGamePackages_With|TestParseGamePackages_NoPre|TestParseGamePackages_Decompressed' ./internal/providers/hoyoverse/... 2>&1 | head -10
```

Expected: compile or assertion errors (fields not yet defined).

### Step 11.5: Implement (extend version.go)

- [ ] Read existing `version.go` shape (`rawGamePackages` struct around line 14). The current shape likely parses only `main.major.{version,game_pkgs}`.

- [ ] Replace the response-shape types with the full M3.B set. The structure parses HoYoverse's String-typed numerics via `json.Number` or via custom UnmarshalJSON. M3.B uses a simple approach: decode size fields as `string` first, then convert in `parseGamePackagesResponse`. Add to `version.go`:

```go
// HypPackageData represents one zip blob in either game_pkgs[] or audio_pkgs[].
// Field types follow Collapse's HypPackageData mapping. Note: HoYoverse API
// returns size + decompressed_size as JSON strings (not numbers); we decode
// to string first and convert in parseGamePackagesResponse.
type HypPackageData struct {
	Language         string `json:"language,omitempty"`         // audio_pkgs only
	URL              string `json:"url"`                        // package download URL
	MD5              string `json:"md5"`
	Size             int64  `json:"-"`                          // populated post-decode
	DecompressedSize int64  `json:"-"`
	SizeRaw             string `json:"size"`
	DecompressedSizeRaw string `json:"decompressed_size"`
}

// HypPackageInfo is one entry in main.major / main.patches[i] / pre_download.major.
type HypPackageInfo struct {
	Version    string           `json:"version"`
	GamePkgs   []HypPackageData `json:"game_pkgs"`
	AudioPkgs  []HypPackageData `json:"audio_pkgs"`
	ResListURL string           `json:"res_list_url,omitempty"`
}

// HypGamePackagesMain wraps the {major, patches[]} pair.
type HypGamePackagesMain struct {
	Major   HypPackageInfo   `json:"major"`
	Patches []HypPackageInfo `json:"patches"`
}

// HypGameEntry corresponds to one `data.game_packages[i]`.
type HypGameEntry struct {
	Game struct {
		ID  string `json:"id"`
		Biz string `json:"biz"`
	} `json:"game"`
	Main        HypGamePackagesMain  `json:"main"`
	PreDownload *HypGamePackagesMain `json:"pre_download,omitempty"`
}

// HypGetGamePackagesResponse is the top-level shape of the
// `getGamePackages` response.
type HypGetGamePackagesResponse struct {
	Retcode int    `json:"retcode"`
	Message string `json:"message"`
	Data    struct {
		GamePackages []HypGameEntry `json:"game_packages"`
	} `json:"data"`
	// ManifestETag is populated by the caller from HTTP ETag header (not part
	// of the JSON wire format).
	ManifestETag string `json:"-"`
}

// parseGamePackagesResponse decodes JSON + post-converts string-typed
// numerics to int64. Used by tests; production fetcher (fetchGamePackages
// below) calls this with the HTTP response body.
func parseGamePackagesResponse(body []byte) (*HypGetGamePackagesResponse, error) {
	var resp HypGetGamePackagesResponse
	if err := json.Unmarshal(body, &resp); err != nil {
		return nil, fmt.Errorf("unmarshal getGamePackages: %w", err)
	}
	// Convert string sizes to int64 across all HypPackageData entries.
	convert := func(p *HypPackageData) error {
		if p.SizeRaw != "" {
			n, err := strconv.ParseInt(p.SizeRaw, 10, 64)
			if err != nil {
				return fmt.Errorf("size %q: %w", p.SizeRaw, err)
			}
			p.Size = n
		}
		if p.DecompressedSizeRaw != "" {
			n, err := strconv.ParseInt(p.DecompressedSizeRaw, 10, 64)
			if err != nil {
				return fmt.Errorf("decompressed_size %q: %w", p.DecompressedSizeRaw, err)
			}
			p.DecompressedSize = n
		}
		return nil
	}
	convertInfo := func(pi *HypPackageInfo) error {
		for i := range pi.GamePkgs {
			if err := convert(&pi.GamePkgs[i]); err != nil {
				return err
			}
		}
		for i := range pi.AudioPkgs {
			if err := convert(&pi.AudioPkgs[i]); err != nil {
				return err
			}
		}
		return nil
	}
	for i := range resp.Data.GamePackages {
		entry := &resp.Data.GamePackages[i]
		if err := convertInfo(&entry.Main.Major); err != nil {
			return nil, fmt.Errorf("entry %d main.major: %w", i, err)
		}
		for j := range entry.Main.Patches {
			if err := convertInfo(&entry.Main.Patches[j]); err != nil {
				return nil, fmt.Errorf("entry %d patches[%d]: %w", i, j, err)
			}
		}
		if entry.PreDownload != nil {
			if err := convertInfo(&entry.PreDownload.Major); err != nil {
				return nil, fmt.Errorf("entry %d pre_download.major: %w", i, err)
			}
			for j := range entry.PreDownload.Patches {
				if err := convertInfo(&entry.PreDownload.Patches[j]); err != nil {
					return nil, fmt.Errorf("entry %d pre_download.patches[%d]: %w", i, j, err)
				}
			}
		}
	}
	return &resp, nil
}
```

Imports add: `"strconv"`, `"fmt"`, `"encoding/json"`.

The existing `rawGamePackages` struct + `fetchVersion` should be REPLACED to use the new `HypGetGamePackagesResponse`. Existing `CheckVersion(ctx, gid)` impl uses just the `version` field; rewire it to read `resp.Data.GamePackages[0].Main.Major.Version` (path equivalent). Verify all M2 tests still pass after rewiring.

If the existing tests assert against the legacy rawGamePackages shape, update them to use the new types. M2 `version_test.go` tests likely just verify CheckVersion returns the right version string — should pass unchanged.

### Step 11.6: Run; verify PASS

```bash
go test -count=1 ./internal/providers/hoyoverse/... 2>&1 | tail -15
```

Expected: 3 new tests + all M2 hoyoverse tests still PASS.

### Step 11.7: Commit

```bash
git add internal/providers/hoyoverse/version.go internal/providers/hoyoverse/version_test.go internal/providers/hoyoverse/testdata/manifest-sample.json
git commit -m "feat(m3b/hoyoverse): extend version.go to parse patches[] + audio_pkgs + pre_download

- HypPackageData / HypPackageInfo / HypGamePackagesMain / HypGameEntry types
- parseGamePackagesResponse: post-decode string→int64 for size + decompressed_size
- ManifestETag field for caller to populate from HTTP ETag header
- testdata/manifest-sample.json (synthesized; URLs sanitized to example.invalid)
- 3 new tests + M2 CheckVersion still PASSES (rewired to Main.Major.Version)"
```

---

## Task 12: update_manifest.go + update_preflight.go (branch decide + plan construction + disk preflight)

**Spec refs:** §1 file table rows, §2 Stage A branch decide + Stage B plan construction.

**Depends on:** Tasks 2 (ReasonCode), 7 (audio_packs), 11 (manifest types), 6 (config_ini Read).

**Files:**
- Create: `internal/providers/hoyoverse/update_manifest.go`
- Create: `internal/providers/hoyoverse/update_preflight.go`
- Test: `internal/providers/hoyoverse/update_manifest_test.go`
- Test: `internal/providers/hoyoverse/update_preflight_test.go`

### Step 12.1: Write failing tests for branch decide

- [ ] Create `internal/providers/hoyoverse/update_manifest_test.go`:

```go
package hoyoverse

import (
	"os"
	"path/filepath"
	"testing"

	"omnigate/internal/core"
)

func loadSampleManifest(t *testing.T) *HypGetGamePackagesResponse {
	t.Helper()
	data, err := os.ReadFile("testdata/manifest-sample.json")
	if err != nil {
		t.Fatal(err)
	}
	resp, err := parseGamePackagesResponse(data)
	if err != nil {
		t.Fatal(err)
	}
	resp.ManifestETag = "test-etag-1234"
	return resp
}

// stubFreeSpace lets preflight tests inject a known free-space value.
type stubFreeSpace struct{ bytes uint64 }

func (s *stubFreeSpace) FreeBytes(path string) (uint64, error) { return s.bytes, nil }

func TestBuildPlan_PlanNone_NoVersionChange_NoAudioDrift(t *testing.T) {
	// currentVer = mainMajor.version, last_apply_target shows same audio langs
	// → PlanNone.
	tmpRoot := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")

	// Pre-write last_apply_target.json with installed audio langs.
	if err := os.MkdirAll(gameSidecarDir(tmpRoot, gid), 0o755); err != nil {
		t.Fatal(err)
	}
	lat := lastApplyTarget{
		TargetVersion:  "5.7.0",
		AudioLanguages: []string{"Chinese"},
	}
	if err := writeLastApplyTarget(tmpRoot, gid, &lat); err != nil {
		t.Fatal(err)
	}

	// Build a fake gameDir with Chinese audio folder.
	gameDir := filepath.Join(t.TempDir(), "GenshinInstall")
	if err := os.MkdirAll(filepath.Join(gameDir, audioAssetsRel, "Chinese"), 0o755); err != nil {
		t.Fatal(err)
	}

	resp := loadSampleManifest(t)
	plan, err := buildPlan(resp, gid, "5.7.0" /* currentVer */, tmpRoot, gameDir, &stubFreeSpace{bytes: 100 * 1024 * 1024 * 1024})
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}
	if plan.Kind != core.PlanNone {
		t.Errorf("kind = %v want PlanNone", plan.Kind)
	}
}

func TestBuildPlan_PlanNone_FreshInstall_NoBaseline(t *testing.T) {
	// last_apply_target.json absent → no audio drift detection → PlanNone
	// even though audio langs may have shifted.
	tmpRoot := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")
	gameDir := filepath.Join(t.TempDir(), "GenshinInstall")
	if err := os.MkdirAll(filepath.Join(gameDir, audioAssetsRel, "Korean"), 0o755); err != nil {
		t.Fatal(err)
	}

	resp := loadSampleManifest(t)
	plan, err := buildPlan(resp, gid, "5.7.0", tmpRoot, gameDir, &stubFreeSpace{bytes: 100 * 1024 * 1024 * 1024})
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}
	if plan.Kind != core.PlanNone {
		t.Errorf("kind = %v want PlanNone (fresh install, no baseline)", plan.Kind)
	}
}

func TestBuildPlan_PlanPatch_VersionMatchesPatchEntry(t *testing.T) {
	tmpRoot := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")
	gameDir := filepath.Join(t.TempDir(), "GenshinInstall")
	if err := os.MkdirAll(filepath.Join(gameDir, audioAssetsRel, "Chinese"), 0o755); err != nil {
		t.Fatal(err)
	}

	resp := loadSampleManifest(t)
	// currentVer = 5.6.0 matches manifest patches[0].version
	plan, err := buildPlan(resp, gid, "5.6.0", tmpRoot, gameDir, &stubFreeSpace{bytes: 100 * 1024 * 1024 * 1024})
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}
	if plan.Kind != core.PlanPatch {
		t.Errorf("kind = %v want PlanPatch", plan.Kind)
	}
	if plan.Reason != core.ReasonVersionChanged {
		t.Errorf("reason = %v want ReasonVersionChanged", plan.Reason)
	}
	// FileTask should include the patch zip blob + selected audio pkg(s).
	if len(plan.Files) == 0 {
		t.Error("expected non-empty Files")
	}
}

func TestBuildPlan_PlanFull_NoMatchingPatch(t *testing.T) {
	tmpRoot := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")
	gameDir := filepath.Join(t.TempDir(), "GenshinInstall")
	if err := os.MkdirAll(gameDir, 0o755); err != nil {
		t.Fatal(err)
	}

	resp := loadSampleManifest(t)
	// currentVer = 3.0.0 doesn't match any patches[].version → PlanFull
	plan, err := buildPlan(resp, gid, "3.0.0", tmpRoot, gameDir, &stubFreeSpace{bytes: 100 * 1024 * 1024 * 1024})
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}
	if plan.Kind != core.PlanFull {
		t.Errorf("kind = %v want PlanFull", plan.Kind)
	}
}

func TestBuildPlan_AudioOnlyPatch(t *testing.T) {
	tmpRoot := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")

	// last_apply_target shows old audio set [Chinese]; current install adds Korean.
	if err := os.MkdirAll(gameSidecarDir(tmpRoot, gid), 0o755); err != nil {
		t.Fatal(err)
	}
	lat := lastApplyTarget{
		TargetVersion:  "5.7.0",
		AudioLanguages: []string{"Chinese"},
	}
	if err := writeLastApplyTarget(tmpRoot, gid, &lat); err != nil {
		t.Fatal(err)
	}

	gameDir := filepath.Join(t.TempDir(), "GenshinInstall")
	for _, lang := range []string{"Chinese", "Korean"} {
		if err := os.MkdirAll(filepath.Join(gameDir, audioAssetsRel, lang), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	resp := loadSampleManifest(t)
	plan, err := buildPlan(resp, gid, "5.7.0", tmpRoot, gameDir, &stubFreeSpace{bytes: 100 * 1024 * 1024 * 1024})
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}
	if plan.Kind != core.PlanPatch {
		t.Errorf("kind = %v want PlanPatch (audio-only)", plan.Kind)
	}
	if plan.Reason != core.ReasonAudioPackAdded {
		t.Errorf("reason = %v want ReasonAudioPackAdded", plan.Reason)
	}
}

func TestBuildPlan_PreDownloadAvailable(t *testing.T) {
	tmpRoot := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")

	// At latest 5.7.0 main; pre_download.major.version = 5.8.0 in fixture.
	if err := os.MkdirAll(gameSidecarDir(tmpRoot, gid), 0o755); err != nil {
		t.Fatal(err)
	}
	lat := lastApplyTarget{TargetVersion: "5.7.0", AudioLanguages: []string{"Chinese"}}
	if err := writeLastApplyTarget(tmpRoot, gid, &lat); err != nil {
		t.Fatal(err)
	}
	gameDir := filepath.Join(t.TempDir(), "GenshinInstall")
	if err := os.MkdirAll(filepath.Join(gameDir, audioAssetsRel, "Chinese"), 0o755); err != nil {
		t.Fatal(err)
	}

	resp := loadSampleManifest(t)
	plan, err := buildPlan(resp, gid, "5.7.0", tmpRoot, gameDir, &stubFreeSpace{bytes: 100 * 1024 * 1024 * 1024})
	if err != nil {
		t.Fatalf("buildPlan: %v", err)
	}
	if plan.Kind != core.PlanNone {
		t.Errorf("Plan should be PlanNone (currentVer == mainMajor); got %v", plan.Kind)
	}
	if !plan.PredownloadAvailable {
		t.Error("expected PredownloadAvailable=true (pre_download in manifest)")
	}
}
```

### Step 12.2: Write failing tests for preflight

- [ ] Create `internal/providers/hoyoverse/update_preflight_test.go`:

```go
package hoyoverse

import (
	"os"
	"path/filepath"
	"testing"

	"omnigate/internal/core"
)

func TestCheckDiskSpace_Enough(t *testing.T) {
	probe := &stubFreeSpace{bytes: 100 * 1024 * 1024 * 1024} // 100 GiB
	plan := core.UpdatePlan{
		Files: []core.FileTask{
			{Path: "blob1.zip", Size: 5 * 1024 * 1024 * 1024}, // 5 GiB
		},
	}
	err := CheckDiskSpace(plan, t.TempDir(), t.TempDir(), probe)
	if err != nil {
		t.Errorf("expected nil; got %v", err)
	}
}

func TestCheckDiskSpace_Insufficient(t *testing.T) {
	probe := &stubFreeSpace{bytes: 1 * 1024 * 1024 * 1024} // 1 GiB free
	plan := core.UpdatePlan{
		Files: []core.FileTask{
			{Path: "blob1.zip", Size: 50 * 1024 * 1024 * 1024}, // 50 GiB needed
		},
	}
	err := CheckDiskSpace(plan, t.TempDir(), t.TempDir(), probe)
	if err == nil {
		t.Fatal("expected error; got nil")
	}
	var ue *core.UpdateError
	if !errors.As(err, &ue) || ue.Code != "insufficient_space" {
		t.Errorf("expected insufficient_space; got %v", err)
	}
}

func TestCheckDiskSpace_CrossVolume(t *testing.T) {
	// On Windows, t.TempDir() always returns a path on the same volume as os.TempDir().
	// Synthesize cross-volume by passing a fake tempRoot on a different drive letter
	// IF Windows; on POSIX, skip (single root volume).
	if !crossVolumeProbeReady() {
		t.Skip("cross-volume test requires Windows multi-drive")
	}
	probe := &stubFreeSpace{bytes: 100 * 1024 * 1024 * 1024}
	plan := core.UpdatePlan{Files: []core.FileTask{{Path: "x", Size: 1024}}}
	err := CheckDiskSpace(plan, `D:\genshin-temp`, `C:\Program Files\Genshin Impact\Genshin Impact game`, probe)
	if err == nil {
		t.Fatal("expected cross_volume_setup error")
	}
	var ue *core.UpdateError
	if !errors.As(err, &ue) || ue.Code != "cross_volume_setup" {
		t.Errorf("expected cross_volume_setup; got %v", err)
	}
}

// crossVolumeProbeReady reports whether D: exists on this host (best-effort).
func crossVolumeProbeReady() bool {
	if _, err := os.Stat(`D:\`); err != nil {
		return false
	}
	return true
}
```

Add `"errors"` to imports.

### Step 12.3: Run; verify FAIL

```bash
go test -count=1 ./internal/providers/hoyoverse/... 2>&1 | head -10
```

Expected: compile errors (`undefined: buildPlan`, `undefined: CheckDiskSpace`, `undefined: lastApplyTarget`, `undefined: writeLastApplyTarget`).

### Step 12.4: Implement update_preflight.go

- [ ] Create `internal/providers/hoyoverse/update_preflight.go`:

```go
package hoyoverse

import (
	"fmt"
	"path/filepath"
	"strings"

	"omnigate/internal/core"
)

// freeSpaceProbe is the test seam for OS free-space queries.
type freeSpaceProbe interface {
	FreeBytes(path string) (uint64, error)
}

// CheckDiskSpace verifies (a) staging tempRoot and gameDir are on the same
// volume; (b) sufficient free bytes at tempRoot for `Σ FileTask.Size × 1.1`
// (download size; not decompressed which Stage F handles per-file).
//
// Returns *core.UpdateError on failure (code = cross_volume_setup or
// insufficient_space). Returns nil on success.
func CheckDiskSpace(plan core.UpdatePlan, tempRoot, gameDir string, probe freeSpaceProbe) error {
	if !sameVolumeWindows(tempRoot, gameDir) {
		return &core.UpdateError{
			Code: "cross_volume_setup",
			Params: map[string]string{
				"temp_root": tempRoot,
				"game_dir":  gameDir,
			},
			Retryable: false,
		}
	}
	var total int64
	for _, f := range plan.Files {
		total += f.Size
	}
	required := uint64(float64(total) * 1.1)

	free, err := probe.FreeBytes(tempRoot)
	if err != nil {
		return fmt.Errorf("free-space probe: %w", err)
	}
	if free < required {
		return &core.UpdateError{
			Code: "insufficient_space",
			Params: map[string]string{
				"required":  formatGiB(required),
				"available": formatGiB(free),
			},
			Retryable: false,
		}
	}
	return nil
}

// sameVolumeWindows compares drive letters (Windows) — best-effort cross-volume
// detection. Returns true on POSIX (single rooted FS; cross-mount detection
// is harder and out-of-scope for v1).
func sameVolumeWindows(a, b string) bool {
	a = filepath.VolumeName(a)
	b = filepath.VolumeName(b)
	if a == "" && b == "" {
		// POSIX
		return true
	}
	return strings.EqualFold(a, b)
}

func formatGiB(bytes uint64) string {
	const gib = 1024 * 1024 * 1024
	return fmt.Sprintf("%.1f GiB", float64(bytes)/float64(gib))
}
```

### Step 12.5: Implement update_manifest.go

- [ ] Create `internal/providers/hoyoverse/update_manifest.go`:

```go
package hoyoverse

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"

	"omnigate/internal/core"
)

// lastApplyTarget is the per-game persistent sidecar written at end of Stage F.
// Path: gameSidecarDir(tempRoot, gid) / "last_apply_target.json".
type lastApplyTarget struct {
	TargetVersion        string    `json:"target_version"`
	AudioLanguages       []string  `json:"audio_languages"`
	CompletionTS         time.Time `json:"completion_ts"`
	ConfigWritebackOK    bool      `json:"config_writeback_ok"`
	ManifestETag         string    `json:"manifest_etag"`
	LastWritebackRetryTS time.Time `json:"last_writeback_retry_ts,omitempty"`
}

func writeLastApplyTarget(tempRoot string, gid core.GameID, lat *lastApplyTarget) error {
	dir := gameSidecarDir(tempRoot, gid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "last_apply_target.json")
	data, err := json.MarshalIndent(lat, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// audioLangToFolder maps manifest audio_pkgs[].language values to AudioAssets/
// subfolder names. Per Task 1 protocol research; keep in sync with that doc.
//
// M3.B v1 hard-codes the mapping based on observed values; if Genshin's API
// shifts, update this table.
var audioLangToFolder = map[string]string{
	"zh-cn": "Chinese",
	"en-us": "English(US)",
	"ja-jp": "Japanese",
	"ko-kr": "Korean",
}

// folderToAudioLang is the inverse map (AudioAssets/<folder> → API lang code).
var folderToAudioLang = func() map[string]string {
	m := make(map[string]string, len(audioLangToFolder))
	for k, v := range audioLangToFolder {
		m[v] = k
	}
	return m
}()

// audioLanguageIntersect returns the API lang codes (subset of
// info.AudioPkgs[].Language) corresponding to AudioAssets/ subfolders
// installed at gameDir.
func audioLanguageIntersect(info *HypPackageInfo, installedFolders []string) []string {
	installedAPI := make(map[string]struct{}, len(installedFolders))
	for _, f := range installedFolders {
		if api, ok := folderToAudioLang[f]; ok {
			installedAPI[api] = struct{}{}
		}
	}
	out := make([]string, 0)
	for _, p := range info.AudioPkgs {
		if _, want := installedAPI[p.Language]; want {
			out = append(out, p.Language)
		}
	}
	sort.Strings(out)
	return out
}

// buildPlan constructs an *core.UpdatePlan from the parsed manifest and
// local state. See spec §2 Stage A branch decide.
//
// Returns one of:
//   - PlanNone (currentVer matches mainMajor and no audio drift)
//   - PlanPatch (currentVer ∈ patches[].version) — Reason=ReasonVersionChanged or ReasonVersionAndAudio
//   - PlanFull (no patch path) — Reason=ReasonVersionChanged
//   - PlanPatch with empty source (audio-only update) — Reason=ReasonAudioPackAdded
//
// PredownloadAvailable is set if pre_download is non-nil.
func buildPlan(
	resp *HypGetGamePackagesResponse,
	gid core.GameID,
	currentVer string,
	tempRoot string,
	gameDir string,
	probe freeSpaceProbe,
) (*core.UpdatePlan, error) {
	if len(resp.Data.GamePackages) == 0 {
		return nil, fmt.Errorf("manifest has no game_packages")
	}
	entry := resp.Data.GamePackages[0]
	mainMajor := entry.Main.Major

	plan := &core.UpdatePlan{
		Version: mainMajor.Version,
	}

	// Detect installed audio langs (folder names → API codes).
	installedFolders, err := DetectInstalledLanguages(gameDir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, fmt.Errorf("detect audio langs: %w", err)
	}
	installedAPI := audioLanguageIntersect(&mainMajor, installedFolders)

	// Predownload availability flag (separate from main plan kind).
	if entry.PreDownload != nil {
		plan.PredownloadAvailable = true
	}

	// Branch decide.
	if currentVer == mainMajor.Version {
		// Audio drift detection (only with last_apply_target baseline).
		latPath := filepath.Join(gameSidecarDir(tempRoot, gid), "last_apply_target.json")
		lat, _ := loadJSONSidecar[lastApplyTarget](latPath)
		if lat == nil {
			plan.Kind = core.PlanNone
			return plan, nil
		}
		baseline := append([]string{}, lat.AudioLanguages...)
		sort.Strings(baseline)
		current := append([]string{}, installedFolders...)
		sort.Strings(current)
		if slicesEqual(baseline, current) {
			plan.Kind = core.PlanNone
			return plan, nil
		}
		// Audio drift detected — synthesize a PlanPatch with audio_pkgs only.
		plan.Kind = core.PlanPatch
		plan.Reason = core.ReasonAudioPackAdded
		// FileTask only for newly-detected langs. (Drop already-installed ones.)
		baselineSet := make(map[string]struct{}, len(lat.AudioLanguages))
		for _, lang := range lat.AudioLanguages {
			if api, ok := folderToAudioLang[lang]; ok {
				baselineSet[api] = struct{}{}
			}
		}
		for _, p := range mainMajor.AudioPkgs {
			if _, want := baselineSet[p.Language]; want {
				continue // already had this lang; no need to download
			}
			if !contains(installedAPI, p.Language) {
				continue // user doesn't have this lang folder
			}
			plan.Files = append(plan.Files, core.FileTask{
				URL:  p.URL,
				MD5:  p.MD5,
				Size: p.Size,
				Path: filepath.Base(p.URL),
			})
		}
		return runPreflight(plan, tempRoot, gameDir, probe)
	}

	// currentVer != mainMajor.Version: try patches[]
	for _, patch := range entry.Main.Patches {
		if patch.Version == currentVer {
			plan.Kind = core.PlanPatch
			plan.Reason = core.ReasonVersionChanged
			audioDrift := !slicesEqual(installedAPI, audioLanguageIntersect(&patch, installedFolders))
			if audioDrift {
				plan.Reason = core.ReasonVersionAndAudio
			}
			plan.Files = make([]core.FileTask, 0, len(patch.GamePkgs)+len(patch.AudioPkgs))
			for _, p := range patch.GamePkgs {
				plan.Files = append(plan.Files, core.FileTask{
					URL: p.URL, MD5: p.MD5, Size: p.Size, Path: filepath.Base(p.URL),
				})
			}
			for _, p := range patch.AudioPkgs {
				if !contains(installedAPI, p.Language) {
					continue
				}
				plan.Files = append(plan.Files, core.FileTask{
					URL: p.URL, MD5: p.MD5, Size: p.Size, Path: filepath.Base(p.URL),
				})
			}
			return runPreflight(plan, tempRoot, gameDir, probe)
		}
	}

	// PlanFull fallback.
	plan.Kind = core.PlanFull
	plan.Reason = core.ReasonVersionChanged
	plan.Files = make([]core.FileTask, 0, len(mainMajor.GamePkgs)+len(mainMajor.AudioPkgs))
	for _, p := range mainMajor.GamePkgs {
		plan.Files = append(plan.Files, core.FileTask{
			URL: p.URL, MD5: p.MD5, Size: p.Size, Path: filepath.Base(p.URL),
		})
	}
	for _, p := range mainMajor.AudioPkgs {
		if !contains(installedAPI, p.Language) {
			continue
		}
		plan.Files = append(plan.Files, core.FileTask{
			URL: p.URL, MD5: p.MD5, Size: p.Size, Path: filepath.Base(p.URL),
		})
	}
	return runPreflight(plan, tempRoot, gameDir, probe)
}

func runPreflight(plan *core.UpdatePlan, tempRoot, gameDir string, probe freeSpaceProbe) (*core.UpdatePlan, error) {
	if err := CheckDiskSpace(*plan, tempRoot, gameDir, probe); err != nil {
		return nil, err
	}
	return plan, nil
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
```

### Step 12.6: Run; verify PASS

```bash
go test -count=1 ./internal/providers/hoyoverse/... 2>&1 | tail -20
```

Expected: 6 manifest tests + 3 preflight tests PASS plus existing tests still PASS.

### Step 12.7: Commit

```bash
git add internal/providers/hoyoverse/update_manifest.go internal/providers/hoyoverse/update_preflight.go internal/providers/hoyoverse/update_manifest_test.go internal/providers/hoyoverse/update_preflight_test.go
git commit -m "feat(m3b/hoyoverse): update_manifest.go branch decide + preflight

- buildPlan: branch decide (PlanNone / PlanPatch / PlanFull / audio-only)
- audio_lang_to_folder mapping table (per Task 1 research)
- audioLanguageIntersect installed × manifest
- last_apply_target.json read/write (drift baseline)
- update_preflight.go: same-volume + free-space check
- freeSpaceProbe interface for mocking
- 6 manifest tests + 3 preflight tests"
```

---

## Task 13: update_progress.go (progressStore wrapping core.ProgressFile + planSnapshot composite)

**Spec refs:** §2 sidecar schemas (progress.json + predl_ready.json shapes).

**Depends on:** Tasks 5 (loadJSONSidecar), 12 (last_apply_target / lastApplyTarget shape).

**Files:**
- Create: `internal/providers/hoyoverse/update_progress.go`
- Test: `internal/providers/hoyoverse/update_progress_test.go`

### Step 13.1: Write failing tests (6 cases)

- [ ] Create `internal/providers/hoyoverse/update_progress_test.go`:

```go
package hoyoverse

import (
	"path/filepath"
	"testing"
	"time"

	"omnigate/internal/core"
)

func newProgressStoreForTest(t *testing.T) *progressStore {
	t.Helper()
	tmp := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")
	ps, err := newProgressStore(tmp, gid, "5.7.0", "test-etag")
	if err != nil {
		t.Fatalf("newProgressStore: %v", err)
	}
	return ps
}

func TestProgressStore_LoadOrInit_Empty(t *testing.T) {
	ps := newProgressStoreForTest(t)
	pf := ps.snapshot()
	if pf.GameID != "hoyoverse/genshin" {
		t.Errorf("game_id = %q want hoyoverse/genshin", pf.GameID)
	}
	if pf.Version != "5.7.0" {
		t.Errorf("version = %q want 5.7.0", pf.Version)
	}
	if len(pf.Entries) != 0 {
		t.Errorf("expected 0 entries, got %d", len(pf.Entries))
	}
}

func TestProgressStore_MarkComplete(t *testing.T) {
	ps := newProgressStoreForTest(t)
	if err := ps.MarkComplete("blob1.zip", 1024, time.Now(), "abcd"); err != nil {
		t.Fatalf("MarkComplete: %v", err)
	}
	pf := ps.snapshot()
	if e, ok := pf.Entries["blob1.zip"]; !ok || e.Size != 1024 || e.Hash != "abcd" {
		t.Errorf("entry not recorded correctly: %+v", pf.Entries)
	}
}

func TestProgressStore_Persist(t *testing.T) {
	ps := newProgressStoreForTest(t)
	if err := ps.MarkComplete("blob1.zip", 1024, time.Now(), "abcd"); err != nil {
		t.Fatal(err)
	}
	if err := ps.Persist(); err != nil {
		t.Fatalf("Persist: %v", err)
	}
	// Re-load from disk and verify.
	ps2, err := newProgressStore(ps.tempRoot, ps.gid, "5.7.0", "test-etag")
	if err != nil {
		t.Fatal(err)
	}
	pf := ps2.snapshot()
	if _, ok := pf.Entries["blob1.zip"]; !ok {
		t.Errorf("entry not persisted: %+v", pf.Entries)
	}
}

func TestProgressStore_AllEntriesComplete(t *testing.T) {
	ps := newProgressStoreForTest(t)
	if ps.AllEntriesComplete([]core.FileTask{{Path: "blob1.zip", Size: 1024}}) {
		t.Error("expected false when no entries marked")
	}
	if err := ps.MarkComplete("blob1.zip", 1024, time.Now(), "abcd"); err != nil {
		t.Fatal(err)
	}
	if !ps.AllEntriesComplete([]core.FileTask{{Path: "blob1.zip", Size: 1024}}) {
		t.Error("expected true when all entries marked")
	}
}

func TestProgressStore_RenameToPredlReady(t *testing.T) {
	ps := newProgressStoreForTest(t)
	if err := ps.MarkComplete("blob1.zip", 1024, time.Now(), "abcd"); err != nil {
		t.Fatal(err)
	}
	snapshot := planSnapshot{
		SourceVersion:  "5.6.0",
		TargetVersion:  "5.7.0",
		Files:          []core.FileTask{{Path: "blob1.zip", Size: 1024, MD5: "abcd"}},
		AudioLanguages: []string{"Chinese"},
		ManifestETag:   "test-etag",
	}
	if err := ps.RenameToPredlReady(snapshot); err != nil {
		t.Fatalf("RenameToPredlReady: %v", err)
	}
	// progress.json should be GONE; predl_ready.json should exist.
	if _, err := loadJSONSidecar[core.ProgressFile](filepath.Join(ps.versionDir(), "progress.json")); err == nil {
		// loadJSONSidecar returns (nil, nil) when ENOENT — verify by re-stat.
	}
	predl, err := loadJSONSidecar[predlReadyFile](filepath.Join(ps.versionDir(), "predl_ready.json"))
	if err != nil {
		t.Fatal(err)
	}
	if predl == nil {
		t.Fatal("predl_ready.json absent")
	}
	if predl.PlanSnapshot.TargetVersion != "5.7.0" {
		t.Errorf("snapshot target_version = %q want 5.7.0", predl.PlanSnapshot.TargetVersion)
	}
}

func TestProgressStore_SetDifferenceInvalidate(t *testing.T) {
	ps := newProgressStoreForTest(t)
	if err := ps.MarkComplete("blob1.zip", 1024, time.Now(), "abcd"); err != nil {
		t.Fatal(err)
	}
	if err := ps.MarkComplete("audio_zh-cn.zip", 2048, time.Now(), "ef01"); err != nil {
		t.Fatal(err)
	}
	// New plan removes audio_zh-cn.zip, adds audio_ko-kr.zip.
	newFiles := []core.FileTask{
		{Path: "blob1.zip", Size: 1024, MD5: "abcd"},
		{Path: "audio_ko-kr.zip", Size: 3072, MD5: "ff22"},
	}
	if err := ps.InvalidateRemoved(newFiles); err != nil {
		t.Fatalf("InvalidateRemoved: %v", err)
	}
	pf := ps.snapshot()
	if _, ok := pf.Entries["blob1.zip"]; !ok {
		t.Errorf("blob1.zip should be retained")
	}
	if _, ok := pf.Entries["audio_zh-cn.zip"]; ok {
		t.Errorf("audio_zh-cn.zip should be invalidated")
	}
}
```

### Step 13.2: Run; verify FAIL

```bash
go test -count=1 ./internal/providers/hoyoverse/... 2>&1 | head -10
```

Expected: compile errors.

### Step 13.3: Implement

- [ ] Create `internal/providers/hoyoverse/update_progress.go`:

```go
package hoyoverse

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"omnigate/internal/core"
)

// planSnapshot is captured into predl_ready.json so a later StartUpdate
// (post-patch-day) can verify reuse criteria element-wise per spec §2
// Stage D predl-hit reuse check.
type planSnapshot struct {
	SourceVersion  string           `json:"source_version"`
	TargetVersion  string           `json:"target_version"`
	Files          []core.FileTask  `json:"files"`
	AudioLanguages []string         `json:"audio_languages"`
	ManifestETag   string           `json:"manifest_etag"`
}

// predlReadyFile is the on-disk shape of predl_ready.json: it embeds
// core.ProgressFile (verbatim core schema; no fork) plus a hoyoverse-local
// PlanSnapshot field. See spec §2 sidecar schemas.
type predlReadyFile struct {
	core.ProgressFile
	PlanSnapshot planSnapshot `json:"plan_snapshot"`
}

// progressStore wraps a *core.ProgressFile in memory + persists to
// versionSidecarDir/progress.json. Hoyoverse-local; granularity is per-zip-blob
// (not per-file as kurogames is — different update model).
type progressStore struct {
	mu       sync.Mutex
	tempRoot string
	gid      core.GameID
	version  string
	pf       *core.ProgressFile
}

func newProgressStore(tempRoot string, gid core.GameID, version string, etag string) (*progressStore, error) {
	dir := versionSidecarDir(tempRoot, gid, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	path := filepath.Join(dir, "progress.json")
	pf, err := loadJSONSidecar[core.ProgressFile](path)
	if err != nil {
		return nil, err
	}
	if pf == nil {
		pf = &core.ProgressFile{
			GameID:  string(gid),
			Version: version,
			ETag:    etag,
			Entries: make(map[string]core.ProgressEntry),
		}
	}
	if pf.Entries == nil {
		pf.Entries = make(map[string]core.ProgressEntry)
	}
	return &progressStore{
		tempRoot: tempRoot,
		gid:      gid,
		version:  version,
		pf:       pf,
	}, nil
}

func (ps *progressStore) versionDir() string {
	return versionSidecarDir(ps.tempRoot, ps.gid, ps.version)
}

func (ps *progressStore) snapshot() core.ProgressFile {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	out := *ps.pf
	out.Entries = make(map[string]core.ProgressEntry, len(ps.pf.Entries))
	for k, v := range ps.pf.Entries {
		out.Entries[k] = v
	}
	return out
}

// MarkComplete records that file `relPath` has been successfully downloaded
// + verified. Persists synchronously to disk.
func (ps *progressStore) MarkComplete(relPath string, size int64, mtime time.Time, hash string) error {
	ps.mu.Lock()
	ps.pf.Entries[relPath] = core.ProgressEntry{
		Size:  size,
		MTime: mtime,
		Hash:  hash,
	}
	ps.mu.Unlock()
	return ps.Persist()
}

// Persist writes pf to <versionDir>/progress.json atomically (tmp + rename).
func (ps *progressStore) Persist() error {
	ps.mu.Lock()
	data, err := json.MarshalIndent(ps.pf, "", "  ")
	ps.mu.Unlock()
	if err != nil {
		return fmt.Errorf("marshal progress.json: %w", err)
	}
	dir := ps.versionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, "progress.json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// AllEntriesComplete reports whether every FileTask path is in pf.Entries.
// Used by Stage E re-run detection (Section 2 resume table row 3).
func (ps *progressStore) AllEntriesComplete(files []core.FileTask) bool {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	for _, f := range files {
		if _, ok := ps.pf.Entries[f.Path]; !ok {
			return false
		}
	}
	return true
}

// InvalidateRemoved drops Entries for paths NOT present in newFiles AND
// removes their on-disk artifacts (the .part / completed file). See spec §2
// audio-drift removed-task disk cleanup.
func (ps *progressStore) InvalidateRemoved(newFiles []core.FileTask) error {
	wanted := make(map[string]struct{}, len(newFiles))
	for _, f := range newFiles {
		wanted[f.Path] = struct{}{}
	}
	ps.mu.Lock()
	toRemove := []string{}
	for k := range ps.pf.Entries {
		if _, ok := wanted[k]; !ok {
			toRemove = append(toRemove, k)
		}
	}
	for _, k := range toRemove {
		delete(ps.pf.Entries, k)
	}
	ps.mu.Unlock()

	dir := ps.versionDir()
	for _, k := range toRemove {
		p := filepath.Join(dir, k)
		_ = os.Remove(p)
		_ = os.Remove(p + ".part")
	}
	return ps.Persist()
}

// RenameToPredlReady renames progress.json → predl_ready.json AND embeds
// the planSnapshot. Used at end of Stage D (predownload completion) so a
// later StartUpdate can perform element-wise reuse comparison.
func (ps *progressStore) RenameToPredlReady(snap planSnapshot) error {
	dir := ps.versionDir()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	prf := predlReadyFile{
		ProgressFile: *ps.pf,
		PlanSnapshot: snap,
	}
	data, err := json.MarshalIndent(prf, "", "  ")
	if err != nil {
		return err
	}
	predlPath := filepath.Join(dir, "predl_ready.json")
	tmp := predlPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, predlPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	// Remove progress.json (now replaced by predl_ready.json).
	_ = os.Remove(filepath.Join(dir, "progress.json"))
	return nil
}
```

### Step 13.4: Run; verify PASS

```bash
go test -count=1 ./internal/providers/hoyoverse/... 2>&1 | tail -15
```

Expected: 6 progressStore tests PASS plus existing tests still PASS.

### Step 13.5: Commit

```bash
git add internal/providers/hoyoverse/update_progress.go internal/providers/hoyoverse/update_progress_test.go
git commit -m "feat(m3b/hoyoverse): update_progress.go progressStore + planSnapshot

- progressStore: in-memory ProgressFile + atomic on-disk persist
- MarkComplete / Persist / AllEntriesComplete / InvalidateRemoved /
  RenameToPredlReady operations
- planSnapshot composite (hoyoverse-local; embeds core.ProgressFile;
  no core schema fork)
- predlReadyFile composite type for predl_ready.json wire format
- 6 unit tests covering load / mark / persist / all-complete / rename-to-predl /
  set-difference invalidate"
```

---

(Tasks 14-22 forthcoming. The plan is now ~half complete; proceeding in subsequent batches.)



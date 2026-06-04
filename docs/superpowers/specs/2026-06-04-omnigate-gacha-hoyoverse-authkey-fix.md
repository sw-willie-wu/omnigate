# Spec — HoYoverse gacha authkey extraction fix (gacha-p3 smoke fix #1)

**Date:** 2026-06-04
**Branch:** gacha-p3
**Scope:** `internal/providers/hoyoverse/gacha.go` (`extractHoyoAuthQuery`, `fetchHoyoGacha`, and helpers). Genshin / Star Rail / ZZZ.
**Status:** DRAFT — pending subagent spec review.

## Background / problem

Real-machine smoke (2026-06-04) verified the getGachaLog **response shape** for all three HoYoverse games with live data (`id/gacha_type/rank_type/item_type/name/time/uid` parse correctly). It also surfaced that the **authkey extraction** in `extractHoyoAuthQuery` + `fetchHoyoGacha` is fragile and fails on a real install where a working authkey is present.

Evidence — gathered by replicating the on-machine working tool `C:\Users\willie\Repos\MiHoYoGachaAnalysis\GachaAnalysis.py` (reads `data_2`, finds lines containing `getGachaLog`, splits on `1/0/`, tries each URL, uses the first that returns non-null `data`, and keeps the **full** URL query params):

For Star Rail `…/StarRail_Data/webCaches/<newest>/Cache/Cache_Data/data_2` (8 distinct getGachaLog URLs):

| URL | timestamp | result with full params |
|---|---|---|
| [0]–[4] | up to 1780271372 | `retcode=-101 authkey timeout` |
| [5]–[7] | 1780271252 / 1780271372 | `retcode=0`, returns list[5] ✅ |

Two URLs share `timestamp=1780271372`; one is **-101**, the other **works**. So "max timestamp" does not identify the valid authkey.

For ZZZ, the same getGachaLog URLs all return data with **full** params, but with only the provider's 6-param allowlist the response is `retcode=0` with an **empty** list.

### Three concrete defects in current code

1. **Wrong anchor.** `hoyoAuthURLRe = https://…authkey=…` matches *any* URL carrying `authkey` (gacha-page init URLs, other APIs), not specifically `getGachaLog`. A non-getGachaLog authkey can authenticate (`retcode=0`) yet return an empty list → ZZZ "0 records" symptom.
2. **Single max-timestamp pick, no fallback.** `extractHoyoAuthQuery` keeps only the one URL with the greatest `timestamp` and uses it. When that one is expired (`-101`), the fetch fails even though another, valid authkey sits in the same `data_2` → Star Rail "timeout" symptom. The selection among equal timestamps is order-dependent/arbitrary (first match in `data_1`→`data_2`→`data_3` then regex order wins on a tie) rather than correctness-driven.
3. **Over-narrow param allowlist.** `fetchHoyoGacha` reconstructs the request from only `authkey, authkey_ver, sign_type, game_biz, lang, region`. Verified on this machine: with that 6-param subset ZZZ returns `retcode=0` + **empty** list, while sending the **full** param set from the cached getGachaLog URL returns data. The 6-param subset works for Star Rail. **We have not isolated which extra param ZZZ requires** (candidates include `gacha_id`, `auth_appid`, `plat_type`, `default_gacha_type`, the `region` value); the fix is therefore "preserve the full query (superset)", not "add param X".

Genshin currently "works" only because its freshest authkey happens to be a valid getGachaLog URL — luck, not correctness.

## Goals

- HoYoverse gacha fetch reliably finds and uses a **working** authkey when one exists in the webCache, for all three games, on real installs.
- No regression to the live-verified response parsing or pity/stats.

## Non-goals

- Endfield token acquisition (tracked separately — SDK 1.32.1.0 break) and Endfield weapon pool.
- Any change to gacha_type→banner mapping, pity models, or the response struct (`hoyoGachaLogResp`) — these are live-verified.
- CN region support, account login, proxy capture.

### Structural change (signatures) — normative

The fix requires changing two signatures (the plan must implement these; tests below depend on them):

- `extractHoyoAuthQuery(installDir, dataDir string) ([]url.Values, error)` — returns an **ordered, deduped candidate list** (was: single `url.Values`).
- A new unit-testable selection step — a `*Provider` method `(p *Provider) selectAuthCandidate(ctx, gid, candidates []url.Values) (url.Values, error)` (it must reach `p`'s http client + endpoint to probe). Performs the probe/fallback and returns the chosen full query, directly callable in in-package tests (like `fetchHoyoGacha` today).
- **`fetchHoyoGacha(ctx, gid, url.Values)` keeps its current signature** (takes the already-chosen single query). The slice/selection handling lives in `selectAuthCandidate` + `FetchGacha`. This keeps `TestFetchGachaPaginatesNormalizes` / `TestFetchGachaAuthkeyTimeout` unchanged, as AC5 requires.
- `FetchGacha` wires `extractHoyoAuthQuery` → (on success) `selectAuthCandidate` → pagination; (on extraction error) builds a one-element candidate slice from `cachedURL` and runs the same selection path.

### Requirements

R1. **Anchor on getGachaLog URLs.** Extraction collects candidate URLs whose string contains the getGachaLog endpoint marker (`getGachaLog`) **and** carry `authkey`. URLs without `getGachaLog` are not candidates. (Scan `data_1`,`data_2`,`data_3` of the newest webCaches dir as today; broadening to all `data_*`/`f_*` is deferred.) **Dedup**: candidates are deduped by their full encoded query string (`url.Values.Encode()`) so the same authkey appearing on many cache lines is probed once.

R2. **Preserve full query.** Each candidate retains the **complete** set of query params from its cached URL. Per-request, the fetch overrides only `gacha_type`, `page`, `size`, `end_id` via `url.Values.Set` (which replaces any existing value for those keys; other keys, incl. `default_gacha_type`, are left verbatim). The 6-param allowlist is removed. This superset approach is intentional (necessary ZZZ param not isolated — see Defect 3).

R3. **Try candidates with fallback — authoritative selection rule.** Order candidates by `timestamp` descending (missing/zero `timestamp` sorts last; ties broken by stable extraction order for deterministic tests). Probe each candidate once (first banner `gacha_type` for the game, `page=1`). The selection rule is a **two-tier scan over ALL candidates** (do not stop at the first `retcode==0`):
   - **Tier 1 (preferred):** the first candidate (in order) whose probe returns `retcode==0` **and a non-empty `data.list`**.
   - **Tier 2 (fallback):** if no candidate yields a non-empty list, the first candidate with `retcode==0` and non-nil `data`.
   - **Fail:** if no candidate reaches `retcode==0` with non-nil `data`, return `core.ErrGachaURLUnavailable`.
   Implementation note (must-do): a single pass that stops at the first `retcode==0` is **wrong** (would pick a `retcode==0`+empty wrong-anchor candidate and reintroduce the ZZZ bug). Either probe all candidates and then apply the two tiers, or scan in order while remembering the first Tier-2 hit and returning the first Tier-1 hit immediately. Respect `gachaPageDelay` between probes. The chosen candidate's full query is then used for the full multi-banner `end_id` pagination.
   - (Divergence from the on-machine Python reference, which stops at the first non-null `data`: we deliberately prefer a non-empty list first, because the ZZZ wrong-anchor URL returns `retcode==0`+empty. Do not weaken R3 to "first non-null data".)

R4. **Cached-URL fallback.** When `extractHoyoAuthQuery` returns an error (no fresh candidates), parse `cachedURL` into a single-element candidate slice and run the **same** selection path. The stored cachedURL is `endpoint + "?" + query.Encode()` (already a getGachaLog query), so it is **exempt from the R1 getGachaLog-anchor check** — accept it as a candidate as long as it carries `authkey`. When extraction **succeeds** but every candidate fails the probe, the fetch returns `core.ErrGachaURLUnavailable` and does **not** additionally try `cachedURL` (matches current behavior; this is a deliberate decision, not an oversight).

R5. **No token/authkey logging.** Authkeys must never be written to logs (current code already avoids this; preserve it). Debug logging, if any, must redact `authkey`.

R6. **Test seams preserved.** `gachaEndpoint` and `gachaPageDelay` seams keep working; tests must be able to drive multi-candidate selection against an `httptest` server without real files.

## Acceptance criteria

- AC1. Given a `data_2` fixture containing multiple getGachaLog URLs where the max-timestamp one returns `-101` and another returns `retcode=0`+data, the provider selects the working one and returns its pulls. (Star Rail scenario.)
- AC2. Given a candidate set where a non-getGachaLog `authkey` URL returns `retcode=0`+empty and a real getGachaLog URL returns data, the provider returns the data (does not get stuck on the empty one). (ZZZ wrong-anchor scenario.)
- AC3. Full query params from the chosen URL are forwarded on **every** request including subsequent `end_id` pages — a fixture URL carrying extra params (`gacha_id` etc.) has them present on page 1 and page 2. (ZZZ param scenario + pagination.)
- AC4. When all candidates return `retcode!=0` (e.g. all `-101`), the provider returns `core.ErrGachaURLUnavailable`.
- AC5. The two `TestExtractAuthQuery_*` tests (which assert the old single-`url.Values` return) are **rewritten** to the new `[]url.Values` candidate API and pass; all other HoYoverse tests — response parsing, `end_id` pagination, banner mapping, pity — are unchanged and pass.
- AC6. `go build ./...`, `go test ./internal/providers/hoyoverse/...` (no `-race`, CGO disabled host), and `wails build` green.
- AC7. Authkeys are never written to logs (assert no `authkey=` substring in any emitted log line in a test that captures the logger).

## Test plan (TDD)

- Unit: `extractHoyoAuthQuery` over a synthetic webCaches tree (`t.TempDir()`, as existing tests do) — returns ordered+deduped candidates, getGachaLog-anchored (an `authkey=` URL without `getGachaLog` is excluded), full query preserved, ordered by timestamp desc with missing-ts last.
- Unit: `selectAuthCandidate` (directly callable) against `httptest` server (via `gachaEndpoint` seam, `gachaPageDelay=0`) scripted to:
  - (AC1) candidate A (max ts) → -101, candidate B → retcode=0+data ⇒ selects B.
  - (AC2) wrong-anchor candidate → retcode=0+**empty**, real candidate → retcode=0+data ⇒ selects the real one (proves two-tier, not first-retcode-0).
  - (AC4) all candidates → -101 ⇒ `ErrGachaURLUnavailable`.
- Unit/integration: full fetch asserts forwarded query contains extra params on page 1 **and** the `end_id` follow-up page (AC3).
- Logging: capture logger, assert no `authkey=` emitted (AC7).
- Regression: rewrite the two `TestExtractAuthQuery_*` cases to the slice API; all other gacha_test.go cases unchanged and green (AC5).

## Risks

- Over-fetching during candidate probing → one probe request per **deduped** candidate (R1 dedup bounds this; a real `data_2` had ~8 distinct getGachaLog URLs), respect `gachaPageDelay` between probes, and Tier-1 returns immediately on the first non-empty hit so the common case is one probe.
- Selection-rule subtlety (retcode==0-but-empty): pinned by the two-tier rule in R3 + the "do not single-pass" implementation note. AC2 guards against regression.
- Signature change touches `FetchGacha`'s extraction-then-cachedURL flow (R4) — covered by keeping both paths through `selectAuthCandidate`.

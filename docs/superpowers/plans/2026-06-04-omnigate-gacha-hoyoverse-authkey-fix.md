# Plan — HoYoverse gacha authkey extraction fix (gacha-p3 smoke fix #1)

**Date:** 2026-06-04 · **Branch:** gacha-p3 · **Spec:** `docs/superpowers/specs/2026-06-04-omnigate-gacha-hoyoverse-authkey-fix.md`
**Status:** DRAFT — pending subagent plan review.

Single cohesive change to `internal/providers/hoyoverse/gacha.go` + its tests. Implemented TDD as **one wave** (tightly coupled), reviewed as one wave, committed once after review APPROVE.

## Target end-state (function shapes)

```go
// CHANGED: returns ordered, deduped candidate queries (was single url.Values).
func extractHoyoAuthQuery(installDir, dataDir string) ([]url.Values, error)

// NEW: probes candidates against getGachaLog, returns the chosen full query.
// Method on *Provider (needs http client + endpoint). Two-tier selection.
func (p *Provider) selectAuthCandidate(ctx context.Context, gid core.GameID, cands []url.Values) (url.Values, error)

// UNCHANGED signature: operates on the already-chosen full query.
func (p *Provider) fetchHoyoGacha(ctx, gid, auth url.Values) (core.GachaFetchResult, error)
```

`FetchGacha` flow becomes:
```
cands, err := extractHoyoAuthQuery(installDir, dataDirFor(m))
if err != nil {            // no fresh candidates → cachedURL path
    if cachedURL == "" { return ErrGachaURLUnavailable }
    cu, perr := url.Parse(cachedURL)
    if perr != nil || cu.Query().Get("authkey") == "" { return ErrGachaURLUnavailable }
    cands = []url.Values{cu.Query()}   // single-element, R4 (anchor-exempt)
}
auth, err := p.selectAuthCandidate(ctx, gid, cands)
if err != nil { return ErrGachaURLUnavailable }
return p.fetchHoyoGacha(ctx, gid, auth)
```

## Step-by-step (TDD; write test → red → implement → green, per sub-step)

### S1 — `extractHoyoAuthQuery` → candidate slice (R1, R2 preservation, dedup, ordering)

1. Add regex/marker: a candidate URL must match `hoyoAuthURLRe` (has `authkey=`) **and** contain the substring `getGachaLog`. Keep scanning `data_1/data_2/data_3` of the newest webCaches dir (unchanged dir logic at gacha.go:28-48).
2. For each match: `u.Query()`, require `authkey != ""` (drop the `game_biz != ""` hard requirement — keep `authkey` only; game_biz still naturally present on real URLs and preserved in the full query).
3. Collect into a slice; **dedup by `q.Encode()`** (map[string]bool seen-set).
4. **Order**: stable sort by `timestamp` (parsed int, missing/zero last) descending; preserve original encounter order for ties (use `sort.SliceStable` over a slice that records encounter index).
5. Return `([]url.Values, error)`; error `core.ErrGachaURLUnavailable` when the slice is empty.

**Tests (rewrite the two existing + add):**
- Rewrite `TestExtractAuthQuery_PicksFreshestByTimestamp`: fixtures MUST be getGachaLog URLs now (e.g. `https://public-operation-hkrpg-sg.hoyoverse.com/common/hkrpg_gacha_record/api/getGachaLog?authkey=OLD&...&timestamp=1000` and `...authkey=NEW...timestamp=2000`). Assert `cands[0].Get("authkey")=="NEW"` (freshest first) and full params preserved.
- Rewrite `TestExtractAuthQuery_NoneFound`: empty dir → error (slice form).
- ADD `TestExtractAuthQuery_ExcludesNonGachaLog`: a URL with `authkey=` but no `getGachaLog` (e.g. the old `index.html?authkey=...`) is **not** a candidate; only the getGachaLog one is returned.
- ADD `TestExtractAuthQuery_DedupAndOrder`: same query appearing 3× → 1 candidate; missing-timestamp URL sorts after a timestamped one.

### S2 — `selectAuthCandidate` (R3 two-tier probe/fallback)

1. New `*Provider` method. For each candidate (in given order) issue ONE probe: `q := cloneValues(cand)` (same deep-copy helper as S3 — never mutate the shared candidate map), `Set("gacha_type", firstGachaType(gid))`, `Set("size","20")`, `Set("page","1")`, `Set("end_id","0")`; GET `endpointFor(gid)+"?"+q.Encode()` with `UserAgent`.
   - `firstGachaType(gid)` = `gachaTypesToQuery[gid][0]` (verified: genshin `301`, starrail `11`, zzz `2`).
   - On the response: **always `defer`/explicitly `resp.Body.Close()` and drain** (e.g. `io.ReadAll` then close) for every probe — up to ~N probes per fetch against a keep-alive client, so a leaked body is a real connection leak.
2. Parse `hoyoGachaLogResp`. Classify probe — **a failed candidate is SKIPPED (continue to next), never a fatal return**:
   - **Skip** (do not classify, continue scanning): transport error, `resp.StatusCode != 200`, JSON unmarshal error, `retcode != 0` (e.g. -101), or `Data == nil`. (Unlike `fetchHoyoGacha`, a non-200/transport error here is NOT fatal — the whole point is to fall through to the next candidate. AC1/AC4 depend on this.)
   - Tier-1 hit: `retcode==0 && Data != nil && len(Data.List) > 0` → **return this candidate immediately** (candidates are pre-ordered, so the first encountered Tier-1 is the first-in-order Tier-1).
   - Tier-2 candidate: `retcode==0 && Data != nil` with empty list → remember the **first** such candidate, keep scanning.
   - Respect `ctx.Err()` (return ctx error) at the top of each iteration.
3. After the scan: (Tier-1 already early-returned.) Else if a Tier-2 candidate was remembered → return it. Else → `core.ErrGachaURLUnavailable`.
4. `sleep(p.gachaPageDelay)` between probes (skip after the last / after an early return).
5. Never log `authkey` (no new logging; if any debug log added, redact).

**Tests (new, via `gachaEndpoint` seam + `gachaPageDelay=0`):**
- `TestSelectAuthCandidate_FallbackOnTimeout` (AC1): candidate A (authkey "EXPIRED") → -101; candidate B ("GOOD") → retcode0+list[1]. Handler switches on `authkey`. Assert chosen `authkey=="GOOD"`.
- `TestSelectAuthCandidate_PrefersNonEmptyOverEmpty` (AC2): candidate W ("WRONG") → retcode0+empty; candidate R ("REAL") → retcode0+list[1], with W ordered FIRST. Assert chosen `authkey=="REAL"` (proves two-tier, not first-retcode0).
- `TestSelectAuthCandidate_AllFail` (AC4): all → -101 → `ErrGachaURLUnavailable`.
- `TestSelectAuthCandidate_Tier2WhenAllEmpty`: all retcode0+empty → returns the first (Tier-2), no error.

### S3 — wire `FetchGacha` + full-query forwarding in `fetchHoyoGacha` (R2)

1. Update `FetchGacha` to the flow above (slice + cachedURL one-element + selectAuthCandidate).
2. In `fetchHoyoGacha`, replace the 6-param allowlist loop (gacha.go:246-254) with: `q := cloneValues(auth)` then `q.Set("gacha_type", gt); q.Set("size","20"); q.Set("page",...); q.Set("end_id", endID)`. Everything else from `auth` is forwarded verbatim. (`cloneValues` = copy map so per-page mutation doesn't corrupt `auth`.)
3. `out.URL` caching unchanged (`endpoint+"?"+auth.Encode()`), still token-bearing/never logged.

**Tests:**
- `TestFetchGachaPaginatesNormalizes` — UNCHANGED (still calls `fetchHoyoGacha(ctx,gid,q)`); must stay green (AC5).
- `TestFetchGachaAuthkeyTimeout` — UNCHANGED; green (AC5).
- ADD `TestFetchGachaForwardsFullParams` (AC3): `auth` carries `gacha_id=ABC`; handler asserts `gacha_id==ABC` on the page-1 request AND on the `end_id` page-2 request (return list[20] then list[1] to force a 2nd page). 
- ADD `TestFetchGachaNoAuthkeyInLogs` (AC7): inject a capturing `slog` handler into `New`, run a fetch, assert no emitted record contains `authkey=` / the authkey value.

### S4 — verification (AC6)

- `go build ./...`
- `go test ./internal/providers/hoyoverse/...` (no `-race`; CGO disabled host)
- `wails build` (smoke the bundle builds)

## Out of scope (do not touch)

`hoyoGachaLogResp`, banner/gacha_type maps, pity models, Endfield, WuWa, CN region, any UI/store code. Genshin/SR/ZZZ endpoints unchanged.

## Risks / watch-items

- Rewriting `TestExtractAuthQuery_*` fixtures to getGachaLog URLs is mandatory (old fixtures use `index.html` URLs that the new anchor correctly rejects) — this is expected, not a regression.
- Probe count bounded by deduped candidate count; `gachaPageDelay` respected. Tier-1 early-return keeps the happy path at 1 probe.
- `cloneValues` must deep-enough copy (`url.Values` is `map[string][]string`; copy each slice or rebuild via `Encode`/`ParseQuery`) so concurrent banner loops don't alias.

## Commit (after wave review APPROVE only)

One commit on `gacha-p3`: `fix(gacha-p3): HoYoverse authkey extraction — getGachaLog anchor, full-query forward, multi-candidate fallback`. No `Co-Authored-By` trailer (project convention).

# Gacha Analysis P1 (Foundation + Endfield) Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Ship a working 抽卡分析 dashboard tab for **Arknights: Endfield** end-to-end (log→URL→record API→SQLite→stats→Vue board), on a fully backend-agnostic foundation that later HoYoverse/WuWa providers plug into with zero engine/frontend changes.

**Architecture:** A per-provider optional `core.GachaProvider` capability fetches normalized `GachaPull` records (no password; reads the game's local log for a short-lived history URL). A backend-agnostic `internal/store` (SQLite via `modernc.org/sqlite`) persists + dedups them. A pure-function stats engine (`internal/core/gacha`) computes the dashboard `GachaSummary` from stored pulls + a per-game `GachaConfig`/`PityModel`. App bindings `RefreshGacha`/`GetGachaSummary` orchestrate; a Vue `GachaBoard.vue` + Pinia `gacha.ts` render it. Ordering uses the monotonic record `ID`, not parsed time.

**Tech Stack:** Go 1.x (CGO_ENABLED=0), `modernc.org/sqlite` (pure-Go), Wails v2, Vue 3 `<script setup>`, Pinia, vue-i18n, vitest.

**Scope note:** This plan covers ONLY the foundation + Endfield (the fully-validated provider, reference: `C:\Users\willie\Repos\gacha-tracker`). HoYoverse (`getGachaLog` + Genshin webCaches `data_2` binary scan) and Wuthering Waves require a separate live research spike and are a follow-on **Plan 2**. WuWa kill-switch and per-game URL-source strategy live in the spec (`docs/superpowers/specs/2026-06-04-omnigate-gacha-analysis-design.md`).

---

## File Structure

**Create:**
- `internal/core/gacha.go` — gacha core types + `GachaProvider`/`PityModel` interfaces (kept separate from `provider.go` for focus).
- `internal/core/gacha_stats.go` — pure stats engine (`ComputeSummary`).
- `internal/core/gacha_stats_test.go`
- `internal/store/store.go` — `GachaStore` interface.
- `internal/store/sqlite.go` — SQLite implementation.
- `internal/store/sqlite_test.go`
- `internal/providers/hypergryph/gacha.go` — Endfield `GachaProvider` (log URL extract + record API + normalize) + `GachaConfig`/`PityModel`.
- `internal/providers/hypergryph/gacha_test.go`
- `internal/app/gacha.go` — `RefreshGacha`/`GetGachaSummary` bindings + store lifecycle.
- `internal/app/gacha_test.go`
- `frontend/src/stores/gacha.ts`
- `frontend/src/components/GachaBoard.vue`
- `frontend/src/__tests__/gacha_store.test.ts`
- `frontend/src/components/__tests__/GachaBoard.spec.ts`

**Modify:**
- `internal/core/errors.go` — add `ErrGachaURLUnavailable` sentinel + `ErrorCode` case.
- `internal/app/app.go` — App struct gets `gachaStore`; `New` opens it; add `Close()`.
- `main.go` — wire `OnShutdown: func(context.Context){ a.Close() }`.
- `frontend/src/components/DetailView.vue` — branch content on `view.homeTab`.
- `frontend/src/components/NavStrip.vue` — enable the gacha tab.
- `frontend/src/composables/useRefreshAll.ts` — call `gacha.reset()`.
- `frontend/src/locales/{en,zh-TW,zh-CN}.json` — `gacha.*` keys.
- `frontend/src/__tests__/NavStrip.test.ts` — update the now-stale "disabled" assertions.
- `go.mod` / `go.sum` — add `modernc.org/sqlite`.

---

## Task 1: Add the SQLite dependency

**Files:**
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add the dependency**

Run (from repo root):
```bash
go get modernc.org/sqlite@latest
```
Expected: `go.mod` gains a `modernc.org/sqlite vX.Y.Z` require line; `go.sum` updated.

- [ ] **Step 2: Verify it builds and is pure-Go (no CGO)**

Run:
```bash
go build ./...
```
Expected: builds clean. (The driver registers under name `"sqlite"`.)

- [ ] **Step 3: Commit**

```bash
git add go.mod go.sum
git commit -m "build(gacha-p3): add modernc.org/sqlite (pure-Go, CGO_ENABLED=0)"
```

---

## Task 2: Core gacha types + interfaces

**Files:**
- Create: `internal/core/gacha.go`
- Modify: `internal/core/errors.go`

- [ ] **Step 1: Write the core types file**

Create `internal/core/gacha.go`:
```go
package core

import "context"

// GachaPull is one normalized pull record (backend-agnostic).
// ID is the provider's monotonic unique id (HoYoverse `id`, Endfield `seqId`)
// and doubles as the dedup key AND the chronological sort key — we never parse
// the localized Time string for ordering.
type GachaPull struct {
	ID        string `json:"id"`
	BannerKey string `json:"bannerKey"` // per-game banner key (matches GachaConfig.Banners[].Key)
	ItemType  string `json:"itemType"`  // normalized display type (角色/武器…)
	Rank      int    `json:"rank"`      // star rank; range is per-game (Endfield 4/5/6)
	Name      string `json:"name"`
	Time      string `json:"time"`   // source's localized time string, display-only
	IsFree    bool   `json:"isFree"` // free pull (Endfield); excluded from pity & spend
}

// GachaFetchResult is one refresh's outcome.
type GachaFetchResult struct {
	UID   string
	Pulls []GachaPull
	URL   string // the history URL used/obtained this round (store caches it)
}

// PityHit records a headline-rank pull and how many pulls it cost.
type PityHit struct {
	Pull  GachaPull
	Count int
}

// PityModel expresses one banner's pity semantics. Implementations are pure
// (no IO). HoYoverse hard-pity+50/50 and Endfield carryover/isFree both fit.
type PityModel interface {
	HardPity() int  // hard-pity cap for the progress bar
	Has5050() bool  // does this banner have a small/large guarantee (50/50)?
	// Walk takes same-banner pulls sorted ASCENDING by ID and returns each
	// headline-rank hit (with its pull-cost) plus the trailing not-yet-hit pity.
	Walk(sortedAscByID []GachaPull, headlineRank int) (hits []PityHit, trailingPity int)
}

// BannerConfig describes one pool for UI + stats.
type BannerConfig struct {
	Key   string
	Label LocalizedString
	Pity  PityModel
}

// GachaConfig is a game's rarity/banner/pricing config for the stats engine.
type GachaConfig struct {
	HeadlineRank int                     // top rarity (Endfield 6)
	RankLabels   map[int]LocalizedString // display labels per rank
	Banners      []BannerConfig
	PullPrice    int     // estimated price per (non-free) pull
	Currency     string  // e.g. "NT$"
	ExpectedPity float64 // theoretical avg pulls-per-headline (for the luck score)
}

// BannerOf returns the BannerConfig for key, or nil.
func (c GachaConfig) BannerOf(key string) *BannerConfig {
	for i := range c.Banners {
		if c.Banners[i].Key == key {
			return &c.Banners[i]
		}
	}
	return nil
}

// GachaProvider is an optional Provider capability: fetch a game's gacha history
// with no password. URL source is per-game (text-log regex vs webCaches scan);
// FetchGacha does real file IO + network (unlike LastPlayedProbe's pure paths).
type GachaProvider interface {
	// FetchGacha obtains the history URL (from the local log/cache, or reuses
	// cachedURL if still valid), calls the record API with cursor pagination,
	// and returns normalized pulls + uid. Returns ErrGachaURLUnavailable when no
	// usable URL is found (the App turns this into a "re-open in game" prompt).
	FetchGacha(ctx context.Context, gid GameID, installDir, cachedURL string) (GachaFetchResult, error)
	GachaConfig(gid GameID) GachaConfig
}
```

- [ ] **Step 2: Add the sentinel + ErrorCode case**

In `internal/core/errors.go`, add to the `var (...)` block:
```go
	ErrGachaURLUnavailable  = errors.New("gacha history url unavailable")
```
And add a case to `ErrorCode`'s switch (before `default`):
```go
	case errors.Is(err, ErrGachaURLUnavailable):
		return "gacha_url"
```

- [ ] **Step 3: Verify it compiles**

Run:
```bash
go build ./internal/core/...
```
Expected: builds clean.

- [ ] **Step 4: Commit**

```bash
git add internal/core/gacha.go internal/core/errors.go
git commit -m "feat(gacha-p3): core gacha types + GachaProvider/PityModel interfaces + ErrGachaURLUnavailable"
```

---

## Task 3: SQLite GachaStore

**Files:**
- Create: `internal/store/store.go`, `internal/store/sqlite.go`, `internal/store/sqlite_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/store/sqlite_test.go`:
```go
package store

import (
	"path/filepath"
	"testing"

	"omnigate/internal/core"
)

func openTemp(t *testing.T) *SQLiteStore {
	t.Helper()
	db, err := OpenSQLite(filepath.Join(t.TempDir(), "gacha.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestUpsertDedupAndAll(t *testing.T) {
	s := openTemp(t)
	pulls := []core.GachaPull{
		{ID: "100", BannerKey: "special", Rank: 6, Name: "A", Time: "t1"},
		{ID: "101", BannerKey: "special", Rank: 5, Name: "B", Time: "t2"},
	}
	added, err := s.UpsertPulls("hypergryph/endfield", "u1", pulls)
	if err != nil || added != 2 {
		t.Fatalf("first upsert added=%d err=%v want 2", added, err)
	}
	// Re-insert overlapping + one new → only the new counts.
	added2, err := s.UpsertPulls("hypergryph/endfield", "u1", append(pulls,
		core.GachaPull{ID: "102", BannerKey: "special", Rank: 5, Name: "C", Time: "t3"}))
	if err != nil || added2 != 1 {
		t.Fatalf("second upsert added=%d err=%v want 1", added2, err)
	}
	all, err := s.AllPulls("hypergryph/endfield", "u1")
	if err != nil || len(all) != 3 {
		t.Fatalf("AllPulls len=%d err=%v want 3", len(all), err)
	}
}

func TestUIDIsolationAndLatest(t *testing.T) {
	s := openTemp(t)
	s.UpsertPulls("hypergryph/endfield", "u1", []core.GachaPull{{ID: "1", Rank: 6}})
	s.UpsertPulls("hypergryph/endfield", "u2", []core.GachaPull{{ID: "1", Rank: 5}})
	if got, _ := s.AllPulls("hypergryph/endfield", "u2"); len(got) != 1 || got[0].Rank != 5 {
		t.Fatalf("uid isolation broken: %+v", got)
	}
	uids, _ := s.KnownUIDs("hypergryph/endfield")
	if len(uids) != 2 {
		t.Fatalf("KnownUIDs=%v want 2", uids)
	}
	if latest, _ := s.LatestUID("hypergryph/endfield"); latest != "u2" {
		t.Fatalf("LatestUID=%q want u2", latest)
	}
}

func TestURLCacheRoundTrip(t *testing.T) {
	s := openTemp(t)
	if err := s.PutURLCache("hypergryph/endfield", "u1", "https://x/page/gacha_y?token=z"); err != nil {
		t.Fatalf("PutURLCache: %v", err)
	}
	url, _, err := s.GetURLCache("hypergryph/endfield", "u1")
	if err != nil || url == "" {
		t.Fatalf("GetURLCache url=%q err=%v", url, err)
	}
}
```

- [ ] **Step 2: Run the test to verify it fails**

Run:
```bash
go test ./internal/store/...
```
Expected: FAIL — package/types don't exist yet.

- [ ] **Step 3: Write the interface**

Create `internal/store/store.go`:
```go
package store

import (
	"time"

	"omnigate/internal/core"
)

// GachaStore persists gacha pulls keyed by (game, uid) with dedup by id, plus a
// per-(game,uid) cache of the last-known history URL. Backend implementation is
// swappable (SQLite today).
type GachaStore interface {
	UpsertPulls(game, uid string, pulls []core.GachaPull) (added int, err error)
	AllPulls(game, uid string) ([]core.GachaPull, error)
	KnownUIDs(game string) ([]string, error)
	LatestUID(game string) (string, error)
	GetURLCache(game, uid string) (url string, fetchedAt time.Time, err error)
	PutURLCache(game, uid, url string) error
	Close() error
}
```

- [ ] **Step 4: Write the SQLite implementation**

Create `internal/store/sqlite.go`:
```go
package store

import (
	"database/sql"
	"sort"
	"time"

	"omnigate/internal/core"

	_ "modernc.org/sqlite"
)

const schemaVersion = 1

type SQLiteStore struct {
	db *sql.DB
}

var _ GachaStore = (*SQLiteStore)(nil)

// OpenSQLite opens (creating if needed) the gacha DB at path and migrates schema.
func OpenSQLite(path string) (*SQLiteStore, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, err
	}
	// Single connection avoids SQLITE_BUSY on the file; writes are short txns.
	db.SetMaxOpenConns(1)
	s := &SQLiteStore{db: db}
	if err := s.migrate(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

func (s *SQLiteStore) migrate() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS meta (key TEXT PRIMARY KEY, value TEXT);
CREATE TABLE IF NOT EXISTS pulls (
  game TEXT NOT NULL, uid TEXT NOT NULL, id TEXT NOT NULL,
  banner_key TEXT, item_type TEXT, rank INTEGER, name TEXT, time TEXT, is_free INTEGER,
  PRIMARY KEY (game, uid, id)
);
CREATE INDEX IF NOT EXISTS idx_pulls_game_uid ON pulls(game, uid);
CREATE TABLE IF NOT EXISTS url_cache (
  game TEXT NOT NULL, uid TEXT NOT NULL, url TEXT, fetched_at INTEGER,
  PRIMARY KEY (game, uid)
);`)
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`INSERT OR IGNORE INTO meta(key,value) VALUES('schema_version', ?)`, schemaVersion)
	return err
}

func (s *SQLiteStore) Close() error { return s.db.Close() }

func (s *SQLiteStore) UpsertPulls(game, uid string, pulls []core.GachaPull) (int, error) {
	tx, err := s.db.Begin()
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	stmt, err := tx.Prepare(`INSERT OR IGNORE INTO pulls
		(game,uid,id,banner_key,item_type,rank,name,time,is_free)
		VALUES (?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	added := 0
	for _, p := range pulls {
		res, err := stmt.Exec(game, uid, p.ID, p.BannerKey, p.ItemType, p.Rank, p.Name, p.Time, boolInt(p.IsFree))
		if err != nil {
			return 0, err
		}
		if n, _ := res.RowsAffected(); n > 0 {
			added++
		}
	}
	if added > 0 {
		if _, err := tx.Exec(`INSERT OR REPLACE INTO meta(key,value) VALUES(?, ?)`, "latest_uid:"+game, uid); err != nil {
			return 0, err
		}
	}
	return added, tx.Commit()
}

func (s *SQLiteStore) AllPulls(game, uid string) ([]core.GachaPull, error) {
	rows, err := s.db.Query(`SELECT id,banner_key,item_type,rank,name,time,is_free
		FROM pulls WHERE game=? AND uid=?`, game, uid)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []core.GachaPull
	for rows.Next() {
		var p core.GachaPull
		var isFree int
		if err := rows.Scan(&p.ID, &p.BannerKey, &p.ItemType, &p.Rank, &p.Name, &p.Time, &isFree); err != nil {
			return nil, err
		}
		p.IsFree = isFree != 0
		out = append(out, p)
	}
	return out, rows.Err()
}

func (s *SQLiteStore) KnownUIDs(game string) ([]string, error) {
	rows, err := s.db.Query(`SELECT DISTINCT uid FROM pulls WHERE game=?`, game)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return nil, err
		}
		out = append(out, u)
	}
	sort.Strings(out)
	return out, rows.Err()
}

func (s *SQLiteStore) LatestUID(game string) (string, error) {
	var v string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key=?`, "latest_uid:"+game).Scan(&v)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return v, err
}

func (s *SQLiteStore) GetURLCache(game, uid string) (string, time.Time, error) {
	var url string
	var ts int64
	err := s.db.QueryRow(`SELECT url,fetched_at FROM url_cache WHERE game=? AND uid=?`, game, uid).Scan(&url, &ts)
	if err == sql.ErrNoRows {
		return "", time.Time{}, nil
	}
	return url, time.Unix(ts, 0), err
}

func (s *SQLiteStore) PutURLCache(game, uid, url string) error {
	_, err := s.db.Exec(`INSERT OR REPLACE INTO url_cache(game,uid,url,fetched_at) VALUES(?,?,?,?)`,
		game, uid, url, time.Now().Unix())
	return err
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}
```

> NOTE: `time.Now()` is the one allowed clock read here (store layer, not a workflow script). URL_cache holds a ~24h token — never log this URL (enforced in Task 7's binding).

- [ ] **Step 5: Run the tests to verify they pass**

Run:
```bash
go test ./internal/store/...
```
Expected: PASS (3 tests).

- [ ] **Step 6: Commit**

```bash
git add internal/store/
git commit -m "feat(gacha-p3): SQLite GachaStore — upsert-dedup, per-(game,uid) records, url cache, schema v1"
```

---

## Task 4: Stats engine (pure functions)

**Files:**
- Create: `internal/core/gacha_stats.go`, `internal/core/gacha_stats_test.go`

- [ ] **Step 1: Write the failing test**

Create `internal/core/gacha_stats_test.go`:
```go
package core

import "testing"

// stdPity: every pull counts, reset to 0 on headline (HoYoverse-standard-like).
type stdPity struct{ cap int }

func (m stdPity) HardPity() int { return m.cap }
func (m stdPity) Has5050() bool { return false }
func (m stdPity) Walk(sorted []GachaPull, headline int) ([]PityHit, int) {
	hits, pity := []PityHit{}, 0
	for _, p := range sorted {
		pity++
		if p.Rank == headline {
			hits = append(hits, PityHit{Pull: p, Count: pity})
			pity = 0
		}
	}
	return hits, pity
}

func testConfig() GachaConfig {
	return GachaConfig{
		HeadlineRank: 6,
		RankLabels:   map[int]LocalizedString{6: {"en": "6★"}},
		Banners:      []BannerConfig{{Key: "special", Label: LocalizedString{"en": "Limited"}, Pity: stdPity{cap: 80}}},
		PullPrice:    100, Currency: "NT$", ExpectedPity: 60,
	}
}

func TestComputeSummaryBasics(t *testing.T) {
	// 3 pulls then a 6★ (cost 4); then 1 pull then a 6★ (cost 2). Trailing pity 0.
	pulls := []GachaPull{
		{ID: "1", BannerKey: "special", Rank: 5},
		{ID: "2", BannerKey: "special", Rank: 5},
		{ID: "3", BannerKey: "special", Rank: 5},
		{ID: "4", BannerKey: "special", Rank: 6, Name: "Top1"},
		{ID: "5", BannerKey: "special", Rank: 5},
		{ID: "6", BannerKey: "special", Rank: 6, Name: "Top2"},
	}
	s := ComputeSummary("u1", pulls, testConfig())
	if !s.Supported || s.TotalPulls != 6 {
		t.Fatalf("total=%d supported=%v", s.TotalPulls, s.Supported)
	}
	if s.HeadlineCnt != 2 {
		t.Fatalf("headline=%d want 2", s.HeadlineCnt)
	}
	if s.SpendEst != 600 { // 6 non-free × 100
		t.Fatalf("spend=%d want 600", s.SpendEst)
	}
	if s.AvgPity != 3 { // (4+2)/2
		t.Fatalf("avgPity=%v want 3", s.AvgPity)
	}
	if s.WorstPull != 4 {
		t.Fatalf("worst=%d want 4", s.WorstPull)
	}
	if len(s.Pity) != 1 || s.Pity[0].Current != 0 || s.Pity[0].Cap != 80 {
		t.Fatalf("pity=%+v", s.Pity)
	}
	if len(s.RecentHeadline) != 2 || s.RecentHeadline[0].Name != "Top2" { // newest first
		t.Fatalf("recent=%+v", s.RecentHeadline)
	}
	// AvgPity 3 < ExpectedPity 60 → very lucky → high score.
	if s.LuckScore <= 50 {
		t.Fatalf("luck=%d want >50", s.LuckScore)
	}
}

func TestComputeSummaryFreeExcludedFromSpend(t *testing.T) {
	pulls := []GachaPull{
		{ID: "1", BannerKey: "special", Rank: 5, IsFree: true},
		{ID: "2", BannerKey: "special", Rank: 5},
	}
	s := ComputeSummary("u1", pulls, testConfig())
	if s.SpendEst != 100 { // only the 1 non-free pull
		t.Fatalf("spend=%d want 100", s.SpendEst)
	}
}

func TestComputeSummaryEmpty(t *testing.T) {
	s := ComputeSummary("", nil, testConfig())
	if !s.Supported || s.TotalPulls != 0 || len(s.RecentHeadline) != 0 {
		t.Fatalf("empty summary wrong: %+v", s)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run:
```bash
go test ./internal/core/ -run TestComputeSummary
```
Expected: FAIL — `ComputeSummary` undefined.

- [ ] **Step 3: Write the stats engine**

Create `internal/core/gacha_stats.go`:
```go
package core

import (
	"math"
	"sort"
)

// BannerPity is one banner's pity progress for the dashboard.
type BannerPity struct {
	Key      string          `json:"key"`
	Label    LocalizedString `json:"label"`
	Current  int             `json:"current"`
	Cap      int             `json:"cap"`
	NearPity bool            `json:"nearPity"` // >80% of cap
}

// HeadlineEntry is one headline-rank pull for the recent list / timeline.
type HeadlineEntry struct {
	Name      string `json:"name"`
	ItemType  string `json:"itemType"`
	BannerKey string `json:"bannerKey"`
	Time      string `json:"time"`
	Count     int    `json:"count"` // pulls spent to land this one
}

// GachaSummary is the full dashboard payload.
type GachaSummary struct {
	Supported      bool            `json:"supported"`
	UID            string          `json:"uid"`
	TotalPulls     int             `json:"totalPulls"`
	PerBanner      map[string]int  `json:"perBanner"`
	SpendEst       int             `json:"spendEst"`
	Currency       string          `json:"currency"`
	HeadlineCnt    int             `json:"headlineCnt"`
	HeadlineByType map[string]int  `json:"headlineByType"`
	AvgPity        float64         `json:"avgPity"`
	LuckScore      int             `json:"luckScore"`   // 0-100, local-derived
	WinRate5050    *float64        `json:"winRate5050"` // nil if no 50/50 mechanic
	WorstPull      int             `json:"worstPull"`
	Pity           []BannerPity    `json:"pity"`
	Distribution   []int           `json:"distribution"` // buckets: 1-9,10-19,...,70-79,80+
	RecentHeadline []HeadlineEntry `json:"recentHeadline"`
}

// ComputeSummary builds the dashboard from one (uid)'s pulls + config. Pure.
// Ordering uses the monotonic ID (string numeric compare), never parsed Time.
func ComputeSummary(uid string, pulls []GachaPull, cfg GachaConfig) GachaSummary {
	s := GachaSummary{
		Supported: true, UID: uid, Currency: cfg.Currency,
		PerBanner: map[string]int{}, HeadlineByType: map[string]int{},
		Pity: []BannerPity{}, Distribution: make([]int, 9), RecentHeadline: []HeadlineEntry{},
	}
	s.TotalPulls = len(pulls)
	nonFree := 0
	byBanner := map[string][]GachaPull{}
	for _, p := range pulls {
		s.PerBanner[p.BannerKey]++
		if !p.IsFree {
			nonFree++
		}
		if p.Rank == cfg.HeadlineRank {
			s.HeadlineCnt++
			s.HeadlineByType[p.ItemType]++
		}
		byBanner[p.BannerKey] = append(byBanner[p.BannerKey], p)
	}
	s.SpendEst = nonFree * cfg.PullPrice

	var allHits []PityHit
	for _, b := range cfg.Banners {
		group := byBanner[b.Key]
		sortByID(group)
		hits, trailing := b.Pity.Walk(group, cfg.HeadlineRank)
		allHits = append(allHits, hits...)
		near := b.Pity.HardPity() > 0 && trailing*100 >= b.Pity.HardPity()*80
		s.Pity = append(s.Pity, BannerPity{
			Key: b.Key, Label: b.Label, Current: trailing, Cap: b.Pity.HardPity(), NearPity: near,
		})
	}

	// Aggregate over all headline hits: avg, worst, distribution, recent list.
	sumCount := 0
	for _, h := range allHits {
		sumCount += h.Count
		if h.Count > s.WorstPull {
			s.WorstPull = h.Count
		}
		s.Distribution[bucket(h.Count)]++
	}
	if len(allHits) > 0 {
		s.AvgPity = float64(sumCount) / float64(len(allHits))
	}
	s.LuckScore = luckScore(s.AvgPity, cfg.ExpectedPity, len(allHits))

	// Recent headline list: newest first by ID, cap at 8.
	sort.Slice(allHits, func(i, j int) bool { return numLess(allHits[j].Pull.ID, allHits[i].Pull.ID) })
	for i, h := range allHits {
		if i >= 8 {
			break
		}
		s.RecentHeadline = append(s.RecentHeadline, HeadlineEntry{
			Name: h.Pull.Name, ItemType: h.Pull.ItemType, BannerKey: h.Pull.BannerKey,
			Time: h.Pull.Time, Count: h.Count,
		})
	}
	return s
}

// bucket maps a pull-count to a histogram index: 0=1-9,1=10-19,...,7=70-79,8=80+.
func bucket(count int) int {
	if count >= 80 {
		return 8
	}
	b := count / 10
	if b > 8 {
		b = 8
	}
	return b
}

// luckScore maps avg-pity vs expected to 0-100 (lower avg = luckier = higher).
// Returns 50 (neutral) when there is no data.
func luckScore(avg, expected float64, n int) int {
	if n == 0 || expected <= 0 {
		return 50
	}
	score := 50 + (expected-avg)/expected*100
	return int(math.Max(0, math.Min(100, math.Round(score))))
}

func sortByID(g []GachaPull) {
	sort.Slice(g, func(i, j int) bool { return numLess(g[i].ID, g[j].ID) })
}

// numLess compares numeric-string ids by (length, lexicographic) so longer
// (bigger) numbers sort higher without overflowing int64 on huge snowflake ids.
func numLess(a, b string) bool {
	if len(a) != len(b) {
		return len(a) < len(b)
	}
	return a < b
}
```

- [ ] **Step 4: Run to verify it passes**

Run:
```bash
go test ./internal/core/ -run TestComputeSummary -v
```
Expected: PASS (3 tests).

- [ ] **Step 5: Commit**

```bash
git add internal/core/gacha_stats.go internal/core/gacha_stats_test.go
git commit -m "feat(gacha-p3): pure stats engine ComputeSummary — totals/spend/pity/distribution/luck, ID-ordered"
```

---

## Task 5: Endfield PityModel + GachaConfig

**Files:**
- Create: `internal/providers/hypergryph/gacha.go` (config + pity portion only this task)
- Create/append: `internal/providers/hypergryph/gacha_test.go`

> Pity logic is ported faithfully from the user's reference repo
> `C:\Users\willie\Repos\gacha-tracker\src\stores\gachaStore.ts` (`pityCountTop`/`calculateHistory`):
> limited (special) pool counts only non-free pulls, with a milestone-60 carryover;
> standard/beginner count every pull. Headline rank = 6.

- [ ] **Step 1: Write the failing test**

Create `internal/providers/hypergryph/gacha_test.go`:
```go
package hypergryph

import (
	"testing"

	"omnigate/internal/core"
)

func TestEndfieldStandardPityWalk(t *testing.T) {
	m := endfieldStandardPity{}
	pulls := []core.GachaPull{
		{ID: "1", Rank: 5}, {ID: "2", Rank: 6, Name: "X"}, {ID: "3", Rank: 5},
	}
	hits, trailing := m.Walk(pulls, 6)
	if len(hits) != 1 || hits[0].Count != 2 {
		t.Fatalf("hits=%+v want 1 hit cost 2", hits)
	}
	if trailing != 1 {
		t.Fatalf("trailing=%d want 1", trailing)
	}
}

func TestEndfieldLimitedPityExcludesFreeUntilMilestone(t *testing.T) {
	m := endfieldLimitedPity{}
	// 2 free pulls before milestone 60 → ignored; 1 paid → pity 1.
	pulls := []core.GachaPull{
		{ID: "1", Rank: 5, IsFree: true},
		{ID: "2", Rank: 5, IsFree: true},
		{ID: "3", Rank: 5, IsFree: false},
	}
	_, trailing := m.Walk(pulls, 6)
	if trailing != 1 {
		t.Fatalf("trailing=%d want 1 (free pre-milestone ignored)", trailing)
	}
}

func TestEndfieldConfig(t *testing.T) {
	p := New(Settings{}, nil)
	cfg := p.GachaConfig("hypergryph/endfield")
	if cfg.HeadlineRank != 6 {
		t.Fatalf("headlineRank=%d want 6", cfg.HeadlineRank)
	}
	if cfg.BannerOf("special") == nil || cfg.BannerOf("standard") == nil || cfg.BannerOf("beginner") == nil {
		t.Fatalf("missing banners: %+v", cfg.Banners)
	}
}
```

- [ ] **Step 2: Run to verify it fails**

Run:
```bash
go test ./internal/providers/hypergryph/ -run TestEndfield
```
Expected: FAIL — types/method undefined.

- [ ] **Step 3: Write the config + pity models**

Create `internal/providers/hypergryph/gacha.go` (record-fetch added in Task 6; this task adds config+pity):
```go
package hypergryph

import (
	"omnigate/internal/core"
)

const endfieldHardPity = 80           // official: 6★ hard pity (char)
const endfieldExpectedPity = 62.0     // theoretical avg pulls/6★ for the luck score
const endfieldMilestone = 60          // free-pull carryover milestone (ref repo)
const endfieldPullPrice = 100         // estimated price per pull (placeholder unit, NT$)

// endfieldStandardPity: every pull counts; reset to 0 on a headline.
type endfieldStandardPity struct{}

func (endfieldStandardPity) HardPity() int { return endfieldHardPity }
func (endfieldStandardPity) Has5050() bool { return false }
func (endfieldStandardPity) Walk(sorted []core.GachaPull, headline int) ([]core.PityHit, int) {
	hits, pity := []core.PityHit{}, 0
	for _, p := range sorted {
		pity++
		if p.Rank == headline {
			hits = append(hits, core.PityHit{Pull: p, Count: pity})
			pity = 0
		}
	}
	return hits, pity
}

// endfieldLimitedPity: only non-free pulls count; free pulls add to carryover
// only after milestone≥60; on a headline, pity resets to the carried-over count.
type endfieldLimitedPity struct{}

func (endfieldLimitedPity) HardPity() int { return endfieldHardPity }
func (endfieldLimitedPity) Has5050() bool { return false }
func (endfieldLimitedPity) Walk(sorted []core.GachaPull, headline int) ([]core.PityHit, int) {
	hits := []core.PityHit{}
	pity, carry, milestone := 0, 0, 0
	for _, p := range sorted {
		if p.Rank == headline {
			hits = append(hits, core.PityHit{Pull: p, Count: pity})
			pity, carry, milestone = carry, 0, 0
			continue
		}
		if !p.IsFree {
			milestone++
			pity++
		} else if milestone >= endfieldMilestone {
			carry++
		}
	}
	return hits, pity
}

// GachaConfig implements part of core.GachaProvider (FetchGacha is in Task 6).
func (p *Provider) GachaConfig(gid core.GameID) core.GachaConfig {
	return core.GachaConfig{
		HeadlineRank: 6,
		RankLabels: map[int]core.LocalizedString{
			6: {"zh-TW": "六星", "zh-CN": "六星", "en": "6★"},
			5: {"zh-TW": "五星", "zh-CN": "五星", "en": "5★"},
		},
		Banners: []core.BannerConfig{
			{Key: "special", Label: core.LocalizedString{"zh-TW": "特許尋訪", "zh-CN": "特许寻访", "en": "Limited"}, Pity: endfieldLimitedPity{}},
			{Key: "standard", Label: core.LocalizedString{"zh-TW": "基礎尋訪", "zh-CN": "基础寻访", "en": "Standard"}, Pity: endfieldStandardPity{}},
			{Key: "beginner", Label: core.LocalizedString{"zh-TW": "啟程尋訪", "zh-CN": "启程寻访", "en": "Beginner"}, Pity: endfieldStandardPity{}},
		},
		PullPrice: endfieldPullPrice, Currency: "NT$", ExpectedPity: endfieldExpectedPity,
	}
}
```

- [ ] **Step 4: Run to verify it passes**

Run:
```bash
go test ./internal/providers/hypergryph/ -run TestEndfield
```
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add internal/providers/hypergryph/gacha.go internal/providers/hypergryph/gacha_test.go
git commit -m "feat(gacha-p3): Endfield GachaConfig + PityModels (standard + limited milestone-60 carryover, ported from ref repo)"
```

---

## Task 6: Endfield FetchGacha (log URL extract + record API + normalize)

**Files:**
- Modify: `internal/providers/hypergryph/gacha.go`
- Modify: `internal/providers/hypergryph/gacha_test.go`

> Reference: `gacha-tracker\scripts\endfield-export.ps1` (URL regex) +
> `gacha-tracker\src\services\endfieldService.ts` (API path/params/pools/pagination/fields).

- [ ] **Step 1: Write the failing test**

Append to `internal/providers/hypergryph/gacha_test.go`:
```go
import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	// keep existing "testing" + core imports
)

func TestExtractEndfieldGachaURL(t *testing.T) {
	log := "noise\nopening https://ef-webview.gryphline.com/page/gacha_index?token=AAA&server_id=2&lang=zh-tw foo\nlater https://ef-webview.gryphline.com/page/gacha_index?token=BBB&server_id=2&lang=zh-tw bar\n"
	got := extractEndfieldGachaURL([]byte(log))
	if got == "" || !contains(got, "token=BBB") { // most recent wins
		t.Fatalf("url=%q want last (BBB)", got)
	}
}

func TestFetchEndfieldPaginatesAndNormalizes(t *testing.T) {
	// Fake record API: returns 2 records page 1 (hasMore), 1 record page 2.
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/record/char" {
			w.WriteHeader(404)
			return
		}
		calls++
		// Only serve the "special" pool with data; others empty.
		if r.URL.Query().Get("pool_type") != "E_CharacterGachaPoolType_Special" {
			w.Write([]byte(`{"code":0,"msg":"","data":{"list":[],"hasMore":false}}`))
			return
		}
		if r.URL.Query().Get("seq_id") == "" {
			w.Write([]byte(`{"code":0,"msg":"","data":{"list":[
				{"poolId":"sp","poolName":"特許尋訪","charId":"c1","charName":"Alpha","rarity":6,"gachaTs":"2025-01-01 10:00:00","seqId":"200","isFree":false},
				{"poolId":"sp","poolName":"特許尋訪","charId":"c2","charName":"Beta","rarity":5,"gachaTs":"2025-01-01 09:00:00","seqId":"199","isFree":true}
			],"hasMore":true}}`))
			return
		}
		w.Write([]byte(`{"code":0,"msg":"","data":{"list":[
			{"poolId":"sp","poolName":"特許尋訪","charId":"c3","charName":"Gamma","rarity":5,"gachaTs":"2025-01-01 08:00:00","seqId":"198","isFree":false}
		],"hasMore":false}}`))
	}))
	defer srv.Close()

	p := New(Settings{}, nil)
	p.recordAPIBase = srv.URL    // test seam (added in impl)
	p.pageDelay = 0              // no sleep in tests

	url := srv.URL + "/page/gacha_index?token=T&server_id=2&lang=zh-tw"
	res, err := p.fetchEndfield(context.Background(), url)
	if err != nil {
		t.Fatalf("fetchEndfield: %v", err)
	}
	if len(res.Pulls) != 3 {
		t.Fatalf("pulls=%d want 3", len(res.Pulls))
	}
	// normalization: rarity→Rank, seqId→ID, charName→Name, isFree, banner key from pool_type.
	var top *core.GachaPull
	for i := range res.Pulls {
		if res.Pulls[i].ID == "200" {
			top = &res.Pulls[i]
		}
	}
	if top == nil || top.Rank != 6 || top.Name != "Alpha" || top.BannerKey != "special" || top.IsFree {
		t.Fatalf("normalize wrong: %+v", top)
	}
}

func TestFetchGachaMissingLogReturnsSentinel(t *testing.T) {
	p := New(Settings{}, nil)
	p.logPathFn = func() string { return filepath.Join(t.TempDir(), "nope.log") } // test seam
	_, err := p.FetchGacha(context.Background(), "hypergryph/endfield", "", "")
	if err == nil || !isURLUnavailable(err) {
		t.Fatalf("err=%v want ErrGachaURLUnavailable", err)
	}
}

// small helpers for the test
func contains(s, sub string) bool { return len(s) >= len(sub) && (func() bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
})() }
func isURLUnavailable(err error) bool { return errorsIs(err, core.ErrGachaURLUnavailable) }
```

> NOTE for implementer: replace the ad-hoc `contains`/`errorsIs` helpers with `strings.Contains` and `errors.Is` directly in the test (import `strings`, `errors`) — they are spelled out here only to make the assertions explicit. Keep the test behavior identical.

- [ ] **Step 2: Run to verify it fails**

Run:
```bash
go test ./internal/providers/hypergryph/ -run "TestExtract|TestFetch"
```
Expected: FAIL — `fetchEndfield`/`extractEndfieldGachaURL`/seams undefined.

- [ ] **Step 3: Implement FetchGacha**

Append to `internal/providers/hypergryph/gacha.go` (add imports `context`, `encoding/json`, `errors`, `fmt`, `io`, `net/http`, `net/url`, `os`, `path/filepath`, `regexp`, `time`):
```go
var _ core.GachaProvider = (*Provider)(nil)

// test seams — defaults set in New (see Step 4).
// p.recordAPIBase, p.pageDelay, p.logPathFn are added to the Provider struct.

var endfieldGachaURLRe = regexp.MustCompile(`https://ef-webview\.gryphline\.com/page/gacha_[^\s"']*`)

var endfieldPools = []struct{ poolType, bannerKey string }{
	{"E_CharacterGachaPoolType_Special", "special"},
	{"E_CharacterGachaPoolType_Standard", "standard"},
	{"E_CharacterGachaPoolType_Beginner", "beginner"},
}

func defaultEndfieldLogPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, "AppData", "LocalLow", "Gryphline", "Endfield", "sdklogs", "HGWebview.log")
}

// extractEndfieldGachaURL returns the LAST (most recent) gacha page URL in the log.
func extractEndfieldGachaURL(log []byte) string {
	m := endfieldGachaURLRe.FindAll(log, -1)
	if len(m) == 0 {
		return ""
	}
	return string(m[len(m)-1])
}

type endfieldRecordResp struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data *struct {
		List []struct {
			PoolID   string `json:"poolId"`
			PoolName string `json:"poolName"`
			CharID   string `json:"charId"`
			CharName string `json:"charName"`
			Rarity   int    `json:"rarity"`
			GachaTs  string `json:"gachaTs"`
			SeqID    string `json:"seqId"`
			IsFree   bool   `json:"isFree"`
		} `json:"list"`
		HasMore bool `json:"hasMore"`
	} `json:"data"`
}

// FetchGacha implements core.GachaProvider.
func (p *Provider) FetchGacha(ctx context.Context, gid core.GameID, _ , cachedURL string) (core.GachaFetchResult, error) {
	if findByID(gid) == nil {
		return core.GachaFetchResult{}, core.ErrUnknownGame
	}
	gachaURL := p.readGachaURL()
	if gachaURL == "" {
		gachaURL = cachedURL
	}
	if gachaURL == "" {
		return core.GachaFetchResult{}, core.ErrGachaURLUnavailable
	}
	return p.fetchEndfield(ctx, gachaURL)
}

// readGachaURL reads the local SDK log and extracts the latest gacha URL ("" if none).
func (p *Provider) readGachaURL() string {
	path := p.logPathFn()
	if path == "" {
		return ""
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return extractEndfieldGachaURL(b)
}

// fetchEndfield parses token/server_id/lang from gachaURL and paginates each pool.
func (p *Provider) fetchEndfield(ctx context.Context, gachaURL string) (core.GachaFetchResult, error) {
	u, err := url.Parse(gachaURL)
	if err != nil {
		return core.GachaFetchResult{}, core.ErrGachaURLUnavailable
	}
	token := u.Query().Get("token")
	serverID := u.Query().Get("server_id")
	lang := u.Query().Get("lang")
	if token == "" {
		return core.GachaFetchResult{}, core.ErrGachaURLUnavailable
	}
	if serverID == "" {
		serverID = "2"
	}
	if lang == "" {
		lang = "zh-tw"
	}
	hc := p.client
	if hc == nil {
		hc = http.DefaultClient
	}
	out := core.GachaFetchResult{URL: gachaURL, Pulls: []core.GachaPull{}}
	for _, pool := range endfieldPools {
		seqID := ""
		for {
			if err := ctx.Err(); err != nil {
				return out, err
			}
			q := url.Values{}
			q.Set("token", token)
			q.Set("pool_type", pool.poolType)
			q.Set("lang", lang)
			q.Set("server_id", serverID)
			if seqID != "" {
				q.Set("seq_id", seqID)
			}
			req, err := http.NewRequestWithContext(ctx, "GET", p.recordAPIBase+"/api/record/char?"+q.Encode(), nil)
			if err != nil {
				return out, err
			}
			req.Header.Set("User-Agent", UserAgent)
			resp, err := hc.Do(req)
			if err != nil {
				return out, err
			}
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			if resp.StatusCode != 200 {
				return out, fmt.Errorf("endfield record api status %d", resp.StatusCode)
			}
			var r endfieldRecordResp
			if err := json.Unmarshal(body, &r); err != nil {
				return out, err
			}
			if r.Code != 0 || r.Data == nil {
				// token expired / invalid → guide re-open.
				return out, core.ErrGachaURLUnavailable
			}
			for _, e := range r.Data.List {
				out.Pulls = append(out.Pulls, core.GachaPull{
					ID:        e.SeqID,
					BannerKey: pool.bannerKey,
					ItemType:  "char",
					Rank:      e.Rarity,
					Name:      e.CharName,
					Time:      e.GachaTs,
					IsFree:    e.IsFree,
				})
			}
			if !r.Data.HasMore || len(r.Data.List) == 0 {
				break
			}
			seqID = r.Data.List[len(r.Data.List)-1].SeqID
			if p.pageDelay > 0 {
				time.Sleep(p.pageDelay)
			}
		}
	}
	if out.UID == "" {
		out.UID = serverID + ":" + token[:minInt(len(token), 8)] // see Step 4 note on UID
	}
	return out, nil
}

// minInt avoids shadowing the Go 1.21 builtin `min` / any package-level helper.
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
```

- [ ] **Step 4: Add struct fields + seams in `New`**

In `internal/providers/hypergryph/hypergryph.go`, add fields to `Provider`:
```go
	recordAPIBase string
	pageDelay     time.Duration
	logPathFn     func() string
```
And in `New(...)` set defaults before `return`:
```go
	p := &Provider{
		settings: settings,
		logger:   logger,
		client:   &http.Client{Timeout: 5 * time.Minute},
		clock:    realRetryClock{},
	}
	p.recordAPIBase = "https://ef-webview.gryphline.com"
	p.pageDelay = 700 * time.Millisecond // rate-limit between pages (ref: 500-1000ms)
	p.logPathFn = defaultEndfieldLogPath
	return p
```
(Replace the existing `return &Provider{...}` literal accordingly.)

> **UID note:** the Endfield record API response in the ref repo does not expose a
> clean player UID; we derive a stable per-(server,token-prefix) key so multi-account
> isolation still works. If a later spike finds a real UID field, swap it in — the
> store key is the only consumer. Keep this deterministic (no time/random).

- [ ] **Step 5: Run to verify it passes**

Run:
```bash
go test ./internal/providers/hypergryph/ -run "TestExtract|TestFetch|TestEndfield" -v
```
Expected: PASS.

- [ ] **Step 6: Commit**

```bash
git add internal/providers/hypergryph/
git commit -m "feat(gacha-p3): Endfield FetchGacha — HGWebview.log URL regex + /api/record/char pagination + normalize"
```

---

## Task 7: App bindings + store lifecycle

**Files:**
- Create: `internal/app/gacha.go`, `internal/app/gacha_test.go`
- Modify: `internal/app/app.go`

- [ ] **Step 1: Write the failing test**

Create `internal/app/gacha_test.go`:
```go
package app

import (
	"context"
	"log/slog"
	"path/filepath"
	"testing"

	"omnigate/internal/core"
	"omnigate/internal/store"
)

// fakeGachaProvider embeds fakeProvider BY VALUE (exactly like fakeNewsProvider
// in app_test.go) so ID()/Games() are satisfied without a nil-pointer panic
// during provider lookup. The embedded fakeProvider is populated in the helper.
type fakeGachaProvider struct {
	fakeProvider
	res core.GachaFetchResult
	err error
}

func (f *fakeGachaProvider) FetchGacha(_ context.Context, _ core.GameID, _, _ string) (core.GachaFetchResult, error) {
	return f.res, f.err
}
func (f *fakeGachaProvider) GachaConfig(_ core.GameID) core.GachaConfig {
	return core.GachaConfig{HeadlineRank: 6, Banners: []core.BannerConfig{}, Currency: "NT$", ExpectedPity: 60}
}

// newTestAppWithGacha builds a minimal App with one provider for the Endfield
// gid, a temp-dir SQLite store, and a seeded resolved path. If gp is nil, the
// registered provider does NOT implement core.GachaProvider (unsupported case).
func newTestAppWithGacha(t *testing.T, gp *fakeGachaProvider) *App {
	t.Helper()
	gid := core.GameID("hypergryph/endfield")
	base := fakeProvider{id: "hypergryph", games: []core.GameDescriptor{{ID: gid, Backend: "hypergryph"}}}
	a := &App{settings: Settings{Version: 2}, logger: slog.Default()}
	a.ctx = context.Background()
	a.resolved = map[core.GameID]resolvedEntry{gid: {Path: t.TempDir(), Source: core.SourceDefault}}
	st, err := store.OpenSQLite(filepath.Join(t.TempDir(), "gacha.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	a.gachaStore = st
	if gp == nil {
		a.providers = []core.Provider{&base}
	} else {
		gp.fakeProvider = base
		a.providers = []core.Provider{gp}
	}
	return a
}

func TestRefreshThenGetSummary(t *testing.T) {
	a := newTestAppWithGacha(t, &fakeGachaProvider{res: core.GachaFetchResult{
		UID: "u1", URL: "https://x/page/gacha_y?token=z",
		Pulls: []core.GachaPull{{ID: "1", BannerKey: "special", Rank: 6, Name: "A"}},
	}})
	sum, err := a.RefreshGacha("hypergryph/endfield")
	if err != nil || !sum.Supported || sum.TotalPulls != 1 {
		t.Fatalf("refresh sum=%+v err=%v", sum, err)
	}
	// GetGachaSummary reads from store (no network) and returns the same data.
	got, err := a.GetGachaSummary("hypergryph/endfield")
	if err != nil || got.TotalPulls != 1 || got.UID != "u1" {
		t.Fatalf("get sum=%+v err=%v", got, err)
	}
}

func TestRefreshURLUnavailableSurfacesCode(t *testing.T) {
	a := newTestAppWithGacha(t, &fakeGachaProvider{err: core.ErrGachaURLUnavailable})
	_, err := a.RefreshGacha("hypergryph/endfield")
	if core.ErrorCode(err) != "gacha_url" {
		t.Fatalf("code=%q want gacha_url", core.ErrorCode(err))
	}
}

func TestGachaUnsupportedProvider(t *testing.T) {
	a := newTestAppWithGacha(t, nil) // provider without GachaProvider
	sum, err := a.GetGachaSummary("hypergryph/endfield")
	if err != nil || sum.Supported {
		t.Fatalf("want unsupported summary, got %+v err=%v", sum, err)
	}
}
```

> The `newTestAppWithGacha` helper and `fakeGachaProvider` are both defined in the
> code block above (mirrors the real `fakeProvider`/`fakeNewsProvider` value-embed
> pattern in `app_test.go`). If `fakeProvider`'s field names differ from `{id, games}`
> when you read `app_test.go`, adjust the literal to match — everything else holds.

- [ ] **Step 2: Run to verify it fails**

Run:
```bash
go test ./internal/app/ -run "TestRefresh|TestGacha"
```
Expected: FAIL — `RefreshGacha`/`GetGachaSummary`/`gachaStore` undefined.

- [ ] **Step 3: Wire the store into App**

In `internal/app/app.go`:
- Add import `"omnigate/internal/store"`.
- Add field to `App` struct: `gachaStore store.GachaStore`.
- In `New`, after `a.playState = ...`, open the store next to settings:
```go
	if gs, gerr := store.OpenSQLite(gachaDBPathFor(settingsPath)); gerr == nil {
		a.gachaStore = gs
	} else {
		logger.Error("gacha store open failed; gacha disabled", "err", gerr)
	}
```
- Add a helper in `internal/app/gacha.go` (next step) `gachaDBPathFor`.
- Add a close method and wire it into shutdown:
```go
// Close releases App-held resources (gacha DB). Safe to call once.
func (a *App) Close() {
	if a.gachaStore != nil {
		a.gachaStore.Close()
	}
}
```
`main.go` currently sets only `OnStartup`. Add an `OnShutdown` to the Wails `options.App` so the DB closes cleanly on exit:
```go
		OnShutdown: func(context.Context) { a.Close() },
```
(`a` is the App passed to `OnStartup: a.Startup`. With `SetMaxOpenConns(1)` + short txns the DB is crash-safe even without this, but wire it for cleanliness.)

- [ ] **Step 4: Write the bindings**

Create `internal/app/gacha.go`:
```go
package app

import (
	"context"
	"path/filepath"
	"time"

	"omnigate/internal/core"
)

// gachaDBPathFor puts gacha.db beside the settings file (same dir convention as
// playstate). Mirrors playStatePathFor.
func gachaDBPathFor(settingsPath string) string {
	dir := filepath.Dir(settingsPath)
	if dir == "." || dir == "" {
		return "gacha.db"
	}
	return filepath.Join(dir, "gacha.db")
}

// RefreshGacha extracts the local history URL, fetches the record API, upserts
// into the store (dedup), and returns the recomputed summary. Network-touching.
func (a *App) RefreshGacha(gameID string) (core.GachaSummary, error) {
	gid := core.GameID(gameID)
	p, err := a.provider(gid)
	if err != nil {
		return core.GachaSummary{}, err
	}
	gp, ok := p.(core.GachaProvider)
	if !ok {
		return core.GachaSummary{Supported: false}, nil
	}
	if a.gachaStore == nil {
		return core.GachaSummary{Supported: false}, nil
	}

	a.settingsMu.RLock()
	installDir := a.resolved[gid].Path
	a.settingsMu.RUnlock()

	game := string(gid)
	latestUID, _ := a.gachaStore.LatestUID(game)
	cachedURL := ""
	if latestUID != "" {
		cachedURL, _, _ = a.gachaStore.GetURLCache(game, latestUID)
	}

	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	ctx, cancel := context.WithTimeout(ctx, 120*time.Second)
	defer cancel()

	res, err := gp.FetchGacha(ctx, gid, installDir, cachedURL)
	if err != nil {
		// Do NOT log res.URL anywhere — it carries a live token.
		a.logger.Warn("RefreshGacha fetch failed", "gid", gameID, "code", core.ErrorCode(err))
		return core.GachaSummary{}, err
	}
	if _, err := a.gachaStore.UpsertPulls(game, res.UID, res.Pulls); err != nil {
		return core.GachaSummary{}, err
	}
	if res.URL != "" {
		_ = a.gachaStore.PutURLCache(game, res.UID, res.URL)
	}
	all, err := a.gachaStore.AllPulls(game, res.UID)
	if err != nil {
		return core.GachaSummary{}, err
	}
	return core.ComputeSummary(res.UID, all, gp.GachaConfig(gid)), nil
}

// GetGachaSummary reads the store only (no network) and computes the summary for
// the latest known uid. Empty store → supported-but-empty summary.
func (a *App) GetGachaSummary(gameID string) (core.GachaSummary, error) {
	gid := core.GameID(gameID)
	p, err := a.provider(gid)
	if err != nil {
		return core.GachaSummary{}, err
	}
	gp, ok := p.(core.GachaProvider)
	if !ok || a.gachaStore == nil {
		return core.GachaSummary{Supported: false}, nil
	}
	game := string(gid)
	uid, _ := a.gachaStore.LatestUID(game)
	if uid == "" {
		return core.GachaSummary{Supported: true, PerBanner: map[string]int{}, HeadlineByType: map[string]int{},
			Pity: []core.BannerPity{}, Distribution: make([]int, 9), RecentHeadline: []core.HeadlineEntry{}}, nil
	}
	all, err := a.gachaStore.AllPulls(game, uid)
	if err != nil {
		return core.GachaSummary{}, err
	}
	return core.ComputeSummary(uid, all, gp.GachaConfig(gid)), nil
}
```

- [ ] **Step 5: Run to verify it passes**

Run:
```bash
go test ./internal/app/ -run "TestRefresh|TestGacha" -v
```
Expected: PASS.

- [ ] **Step 6: Run the whole Go suite**

Run:
```bash
go test ./...
```
Expected: PASS (no `-race`; CGO_ENABLED=0 host).

- [ ] **Step 7: Commit**

```bash
git add internal/app/gacha.go internal/app/gacha_test.go internal/app/app.go main.go
git commit -m "feat(gacha-p3): App RefreshGacha/GetGachaSummary bindings + SQLite store lifecycle (token never logged)"
```

---

## Task 8: Frontend gacha store

**Files:**
- Create: `frontend/src/stores/gacha.ts`, `frontend/src/__tests__/gacha_store.test.ts`

> The Wails bindings `RefreshGacha`/`GetGachaSummary` are generated under
> `frontend/wailsjs/go/app/App` after the Go build. Tests mock that module.

- [ ] **Step 1: Write the failing test**

Create `frontend/src/__tests__/gacha_store.test.ts`:
```ts
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { setActivePinia, createPinia } from 'pinia';

const getSummary = vi.fn();
const refreshGacha = vi.fn();
vi.mock('../../wailsjs/go/app/App', () => ({
  GetGachaSummary: (...a: unknown[]) => getSummary(...a),
  RefreshGacha: (...a: unknown[]) => refreshGacha(...a),
}));

import { useGachaStore } from '../stores/gacha';

const blankSummary = { supported: true, uid: '', totalPulls: 0, perBanner: {}, recentHeadline: [], pity: [], distribution: [] };

describe('gacha store', () => {
  beforeEach(() => { setActivePinia(createPinia()); getSummary.mockReset(); refreshGacha.mockReset(); });

  it('load reads summary lazily and caches', async () => {
    getSummary.mockResolvedValue({ ...blankSummary, totalPulls: 5 });
    const s = useGachaStore();
    await s.load('hypergryph/endfield');
    expect(s.stateFor('hypergryph/endfield').summary?.totalPulls).toBe(5);
    await s.load('hypergryph/endfield'); // cached → no second call
    expect(getSummary).toHaveBeenCalledTimes(1);
  });

  it('refresh calls RefreshGacha and replaces summary', async () => {
    refreshGacha.mockResolvedValue({ ...blankSummary, totalPulls: 9 });
    const s = useGachaStore();
    await s.refresh('hypergryph/endfield');
    expect(s.stateFor('hypergryph/endfield').summary?.totalPulls).toBe(9);
  });

  it('refresh maps gacha_url error to errKind=url', async () => {
    refreshGacha.mockRejectedValue(new Error('gacha history url unavailable'));
    const s = useGachaStore();
    await s.refresh('hypergryph/endfield');
    expect(s.stateFor('hypergryph/endfield').errKind).toBe('url');
  });

  it('reset clears cache', async () => {
    getSummary.mockResolvedValue({ ...blankSummary, totalPulls: 1 });
    const s = useGachaStore();
    await s.load('hypergryph/endfield');
    s.reset();
    await s.load('hypergryph/endfield');
    expect(getSummary).toHaveBeenCalledTimes(2);
  });
});
```

- [ ] **Step 2: Run to verify it fails**

Run:
```bash
cd frontend && npx vitest run src/__tests__/gacha_store.test.ts
```
Expected: FAIL — store module missing.

- [ ] **Step 3: Write the store**

Create `frontend/src/stores/gacha.ts`:
```ts
import { defineStore } from 'pinia';
import { GetGachaSummary, RefreshGacha } from '../../wailsjs/go/app/App';

export interface BannerPity { key: string; label: Record<string, string>; current: number; cap: number; nearPity: boolean; }
export interface HeadlineEntry { name: string; itemType: string; bannerKey: string; time: string; count: number; }
export interface GachaSummary {
  supported: boolean; uid: string; totalPulls: number; perBanner: Record<string, number>;
  spendEst: number; currency: string; headlineCnt: number; headlineByType: Record<string, number>;
  avgPity: number; luckScore: number; winRate5050: number | null; worstPull: number;
  pity: BannerPity[]; distribution: number[]; recentHeadline: HeadlineEntry[];
}
type ErrKind = 'url' | 'other' | null;
interface GachaState { summary: GachaSummary | null; loading: boolean; errKind: ErrKind; loaded: boolean; }

function blank(): GachaState { return { summary: null, loading: false, errKind: null, loaded: false }; }

export const useGachaStore = defineStore('gacha', {
  state: () => ({ byGid: {} as Record<string, GachaState> }),
  getters: {
    stateFor: (s) => (gid: string): GachaState => s.byGid[gid] ?? blank(),
  },
  actions: {
    async load(gid: string) {
      if (!gid) return;
      const cur = this.byGid[gid];
      if (cur && (cur.loaded || cur.loading)) return;
      this.byGid[gid] = { ...blank(), loading: true };
      try {
        const summary = (await GetGachaSummary(gid)) as unknown as GachaSummary;
        this.byGid[gid] = { summary, loading: false, errKind: null, loaded: true };
      } catch {
        this.byGid[gid] = { summary: null, loading: false, errKind: 'other', loaded: true };
      }
    },
    async refresh(gid: string) {
      if (!gid) return;
      this.byGid[gid] = { ...(this.byGid[gid] ?? blank()), loading: true, errKind: null };
      try {
        const summary = (await RefreshGacha(gid)) as unknown as GachaSummary;
        this.byGid[gid] = { summary, loading: false, errKind: null, loaded: true };
      } catch (e) {
        const msg = (e instanceof Error ? e.message : String(e)) || '';
        const kind: ErrKind = msg.includes('url') ? 'url' : 'other';
        this.byGid[gid] = { ...(this.byGid[gid] ?? blank()), loading: false, errKind: kind, loaded: true };
      }
    },
    reset() { this.byGid = {}; },
  },
});
```

- [ ] **Step 4: Run to verify it passes**

Run:
```bash
cd frontend && npx vitest run src/__tests__/gacha_store.test.ts
```
Expected: PASS (4 tests).

- [ ] **Step 5: Commit**

```bash
git add frontend/src/stores/gacha.ts frontend/src/__tests__/gacha_store.test.ts
git commit -m "feat(gacha-p3): frontend gacha Pinia store — lazy load / refresh / errKind / reset"
```

---

## Task 9: GachaBoard component + DetailView/NavStrip wiring

**Files:**
- Create: `frontend/src/components/GachaBoard.vue`, `frontend/src/components/__tests__/GachaBoard.spec.ts`
- Modify: `frontend/src/components/DetailView.vue`, `frontend/src/components/NavStrip.vue`, `frontend/src/__tests__/NavStrip.test.ts`

- [ ] **Step 1: Write the failing component test**

Create `frontend/src/components/__tests__/GachaBoard.spec.ts`:
```ts
import { describe, it, expect, beforeEach, vi } from 'vitest';
import { mount, flushPromises } from '@vue/test-utils';
import { setActivePinia, createPinia } from 'pinia';
import { createI18n } from 'vue-i18n';
import en from '../../locales/en.json';

const getSummary = vi.fn();
const refreshGacha = vi.fn();
vi.mock('../../../wailsjs/go/app/App', () => ({
  GetGachaSummary: (...a: unknown[]) => getSummary(...a),
  RefreshGacha: (...a: unknown[]) => refreshGacha(...a),
}));

import GachaBoard from '../GachaBoard.vue';

function mountBoard() {
  const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } });
  return mount(GachaBoard, { props: { gid: 'hypergryph/endfield' }, global: { plugins: [i18n] } });
}
const base = { supported: true, uid: 'u1', totalPulls: 12, perBanner: {}, spendEst: 1200, currency: 'NT$',
  headlineCnt: 2, headlineByType: {}, avgPity: 6, luckScore: 80, winRate5050: null, worstPull: 9,
  pity: [{ key: 'special', label: { en: 'Limited' }, current: 10, cap: 80, nearPity: false }],
  distribution: [1,0,0,0,0,0,0,0,1], recentHeadline: [{ name: 'Alpha', itemType: 'char', bannerKey: 'special', time: 't', count: 4 }] };

describe('GachaBoard', () => {
  beforeEach(() => { setActivePinia(createPinia()); getSummary.mockReset(); refreshGacha.mockReset(); });

  it('renders stat cards from summary', async () => {
    getSummary.mockResolvedValue(base);
    const w = mountBoard(); await flushPromises();
    expect(w.text()).toContain('12');     // total pulls
    expect(w.text()).toContain('Alpha');  // recent headline name
  });

  it('shows empty state when no data', async () => {
    getSummary.mockResolvedValue({ ...base, totalPulls: 0, headlineCnt: 0, recentHeadline: [] });
    const w = mountBoard(); await flushPromises();
    expect(w.find('.gacha-empty').exists()).toBe(true);
  });

  it('shows unsupported state', async () => {
    getSummary.mockResolvedValue({ ...base, supported: false });
    const w = mountBoard(); await flushPromises();
    expect(w.find('.gacha-unsupported').exists()).toBe(true);
  });

  it('shows url-reopen guidance when refresh errKind=url', async () => {
    getSummary.mockResolvedValue(base);
    refreshGacha.mockRejectedValue(new Error('gacha history url unavailable'));
    const w = mountBoard(); await flushPromises();
    await w.find('.gacha-refresh').trigger('click'); await flushPromises();
    expect(w.find('.gacha-url-hint').exists()).toBe(true);
  });
});
```

- [ ] **Step 2: Run to verify it fails**

Run:
```bash
cd frontend && npx vitest run src/components/__tests__/GachaBoard.spec.ts
```
Expected: FAIL — component missing.

- [ ] **Step 3: Write GachaBoard.vue**

Create `frontend/src/components/GachaBoard.vue`:
```vue
<script setup lang="ts">
import { computed, onMounted, watch } from 'vue';
import { useI18n } from 'vue-i18n';
import { useGachaStore } from '../stores/gacha';

const props = defineProps<{ gid: string }>();
const { t, locale } = useI18n();
const gacha = useGachaStore();

const st = computed(() => gacha.stateFor(props.gid));
const sum = computed(() => st.value.summary);
const isEmpty = computed(() => !!sum.value && sum.value.supported && sum.value.totalPulls === 0);
const isUnsupported = computed(() => !!sum.value && !sum.value.supported);

function label(m: Record<string, string> | undefined): string {
  if (!m) return '';
  return m[locale.value] ?? m['en'] ?? Object.values(m)[0] ?? '';
}

onMounted(() => gacha.load(props.gid));
watch(() => props.gid, (g) => gacha.load(g));
</script>

<template>
  <div class="gacha-board">
    <div v-if="st.loading" class="gacha-loading">{{ t('gacha.loading') }}</div>

    <div v-else-if="isUnsupported" class="gacha-unsupported">{{ t('gacha.unsupported') }}</div>

    <div v-else-if="st.errKind === 'url' || isEmpty" class="gacha-empty">
      <p class="gacha-url-hint" v-if="st.errKind === 'url'">{{ t('gacha.url_hint') }}</p>
      <p v-else>{{ t('gacha.empty') }}</p>
      <button class="gacha-refresh" @click="gacha.refresh(props.gid)">{{ t('gacha.refresh') }}</button>
    </div>

    <template v-else-if="sum">
      <div class="gacha-actions">
        <span class="gacha-sub">UID {{ sum.uid }}</span>
        <button class="gacha-refresh" @click="gacha.refresh(props.gid)">{{ t('gacha.refresh') }}</button>
      </div>

      <div class="gacha-cards">
        <div class="card"><div class="num">{{ sum.totalPulls }}</div><div class="cap">{{ t('gacha.total_pulls') }}</div></div>
        <div class="card"><div class="num">{{ sum.currency }}{{ sum.spendEst }}</div><div class="cap">{{ t('gacha.spend_est') }}</div></div>
        <div class="card"><div class="num">{{ sum.headlineCnt }}</div><div class="cap">{{ t('gacha.headline_cnt') }}</div></div>
        <div class="card"><div class="num">{{ sum.avgPity.toFixed(1) }}</div><div class="cap">{{ t('gacha.avg_pity') }}</div></div>
      </div>

      <div class="gacha-luck">
        <div class="luck-score">{{ sum.luckScore }}</div>
        <div class="luck-cap">{{ t('gacha.luck') }} · {{ t('gacha.worst') }} {{ sum.worstPull }}</div>
      </div>

      <div class="gacha-pity">
        <div v-for="b in sum.pity" :key="b.key" class="pity-row" :class="{ near: b.nearPity }">
          <span class="pity-label">{{ label(b.label) }}</span>
          <span class="pity-val">{{ b.current }} / {{ b.cap }}</span>
        </div>
      </div>

      <div class="gacha-recent">
        <div v-for="(h, i) in sum.recentHeadline" :key="i" class="recent-item">
          <span class="r-name">{{ h.name }}</span>
          <span class="r-time">{{ h.time }}</span>
          <span class="r-count">{{ t('gacha.pull_count', { n: h.count }) }}</span>
        </div>
      </div>
    </template>
  </div>
</template>

<style scoped>
.gacha-board { padding: 14px 22px; overflow-y: auto; max-height: 100%; }
.gacha-cards { display: grid; grid-template-columns: repeat(4, 1fr); gap: 12px; }
.card { background: rgba(0,0,0,.35); border-radius: 10px; padding: 14px; }
.card .num { font-weight: 800; font-variant-numeric: tabular-nums; font-size: 1.5rem; }
.card .cap { color: var(--tx-dim, #aaa); font-size: .8rem; margin-top: 4px; }
.gacha-actions { display: flex; justify-content: space-between; align-items: center; margin-bottom: 12px; }
.pity-row.near .pity-val { color: var(--gold-1, #e8c265); }
.gacha-empty, .gacha-unsupported, .gacha-loading { padding: 40px; text-align: center; color: var(--tx-dim, #aaa); }
</style>
```

> Visual fidelity to Prompt.md §2 (donut, mini bar chart, full timeline styling) is a
> polish pass for real-machine smoke; this task locks the data wiring + states. The
> distribution mini-bars and donut may be added during smoke without changing the
> store/summary contract.

- [ ] **Step 4: Wire DetailView to branch on homeTab**

Replace `frontend/src/components/DetailView.vue` `<script setup>` + template:
```vue
<script setup lang="ts">
import { computed } from 'vue';
import { useGamesStore } from '../stores/games';
import { useViewStore } from '../stores/view';
import NewsPanel from './NewsPanel.vue';
import GachaBoard from './GachaBoard.vue';

const games = useGamesStore();
const view = useViewStore();
const gid = computed(() => games.selected?.id ?? '');
</script>

<template>
  <div class="view view-detail">
    <template v-if="view.homeTab === 'gacha' && gid">
      <GachaBoard :gid="gid" class="gacha-slot" />
    </template>
    <template v-else>
      <NewsPanel v-if="gid" :gid="gid" class="news-slot" />
    </template>
  </div>
</template>

<style scoped>
.view-detail { position: relative; }
.news-slot { position: absolute; top: 16px; right: 25px; max-height: calc(100% - 140px); }
.gacha-slot { position: absolute; inset: 0; }
</style>
```

- [ ] **Step 5: Enable the NavStrip gacha tab**

Replace the disabled gacha button in `frontend/src/components/NavStrip.vue`:
```vue
    <button
      class="nav-tab"
      :class="{ active: view.homeTab === 'gacha' }"
      @click="view.setHomeTab('gacha')"
    ><span class="nav-tab-content"><span class="material-symbols-outlined nav-tab-icon">monitoring</span><span class="nav-tab-label">{{ t('nav.gacha') }}</span></span></button>
```
(Remove the `<!-- gacha is a P1 shell tab... -->` comment and the `disabled`/`:title` attrs.)

- [ ] **Step 6: Update the stale NavStrip test**

In `frontend/src/__tests__/NavStrip.test.ts`, find assertions that the gacha tab is `.disabled`/inert and replace with: clicking the gacha tab sets `homeTab` to `'gacha'` and marks it active. (Read the file first; rewrite only the gacha-disabled cases. Example replacement test:)
```ts
  it('clicking gacha tab activates it', async () => {
    const i18n = createI18n({ legacy: false, locale: 'en', messages: { en } });
    const w = mount(NavStrip, { global: { plugins: [i18n] } });
    const view = useViewStore();
    await w.findAll('.nav-tab')[1].trigger('click');
    expect(view.homeTab).toBe('gacha');
  });
```

- [ ] **Step 7: Run frontend tests**

Run:
```bash
cd frontend && npx vitest run src/components/__tests__/GachaBoard.spec.ts src/__tests__/NavStrip.test.ts src/__tests__/App_navstrip.test.ts
```
Expected: PASS.

- [ ] **Step 8: Commit**

```bash
git add frontend/src/components/GachaBoard.vue frontend/src/components/__tests__/GachaBoard.spec.ts frontend/src/components/DetailView.vue frontend/src/components/NavStrip.vue frontend/src/__tests__/NavStrip.test.ts
git commit -m "feat(gacha-p3): GachaBoard.vue + DetailView homeTab branch + enable NavStrip gacha tab"
```

---

## Task 10: i18n keys + parity

**Files:**
- Modify: `frontend/src/locales/en.json`, `frontend/src/locales/zh-TW.json`, `frontend/src/locales/zh-CN.json`

- [ ] **Step 1: Add the `gacha` block to all three locales**

Add a `"gacha"` object (place near the existing `"news"` block). en.json:
```json
  "gacha": {
    "loading": "Loading…",
    "unsupported": "Gacha analysis isn't supported for this game yet.",
    "empty": "No records yet. Open the gacha history in-game once, then refresh.",
    "url_hint": "Records link expired. Open the gacha history in-game again, then refresh.",
    "refresh": "Refresh records",
    "total_pulls": "Total pulls",
    "spend_est": "Spent (est.)",
    "headline_cnt": "Top-rarity",
    "avg_pity": "Avg per top",
    "luck": "Luck",
    "worst": "Worst",
    "pull_count": "{n} pulls"
  }
```
zh-TW.json:
```json
  "gacha": {
    "loading": "載入中…",
    "unsupported": "此遊戲尚未支援抽卡分析。",
    "empty": "尚無紀錄。請先在遊戲內開啟一次抽卡紀錄，再重新整理。",
    "url_hint": "紀錄連結已失效。請在遊戲內重新開啟抽卡紀錄後再重新整理。",
    "refresh": "重新整理紀錄",
    "total_pulls": "總抽數",
    "spend_est": "已花費（估算）",
    "headline_cnt": "最高星數",
    "avg_pity": "平均出貨",
    "luck": "幸運值",
    "worst": "最非",
    "pull_count": "{n} 抽出貨"
  }
```
zh-CN.json:
```json
  "gacha": {
    "loading": "加载中…",
    "unsupported": "此游戏尚未支持抽卡分析。",
    "empty": "尚无记录。请先在游戏内打开一次抽卡记录，再刷新。",
    "url_hint": "记录链接已失效。请在游戏内重新打开抽卡记录后再刷新。",
    "refresh": "刷新记录",
    "total_pulls": "总抽数",
    "spend_est": "已花费（估算）",
    "headline_cnt": "最高星数",
    "avg_pity": "平均出货",
    "luck": "幸运值",
    "worst": "最非",
    "pull_count": "{n} 抽出货"
  }
```

- [ ] **Step 2: Run the parity test**

Run:
```bash
cd frontend && npx vitest run src/__tests__/i18n_parity.test.ts
```
Expected: PASS (all three locales have identical key sets).

- [ ] **Step 3: Commit**

```bash
git add frontend/src/locales/
git commit -m "feat(gacha-p3): gacha.* i18n keys (en/zh-TW/zh-CN parity)"
```

---

## Task 11: Refresh-chain reset hook

**Files:**
- Modify: `frontend/src/composables/useRefreshAll.ts`

- [ ] **Step 1: Read the file and add the reset**

Read `frontend/src/composables/useRefreshAll.ts`. Mirror the existing `news.reset()` call: import `useGachaStore` and call `gacha.reset()` in the same refresh routine (lazy refetch on next view; do NOT trigger a network refresh here — `useRefreshAll` has no current-gid concept).

Example diff (adapt to actual file shape):
```ts
import { useGachaStore } from '../stores/gacha';
// ... inside the refresh function, next to news.reset():
const gacha = useGachaStore();
gacha.reset();
```

- [ ] **Step 2: Run the full frontend suite**

Run:
```bash
cd frontend && npx vitest run
```
Expected: PASS (all suites, including the new gacha ones).

- [ ] **Step 3: Commit**

```bash
git add frontend/src/composables/useRefreshAll.ts
git commit -m "feat(gacha-p3): reset gacha store on global refresh (lazy refetch, mirrors news.reset)"
```

---

## Task 12: Full verification + build

**Files:** none (verification only)

- [ ] **Step 1: Full Go suite**

Run:
```bash
go test ./...
```
Expected: PASS (no `-race`).

- [ ] **Step 2: Full frontend suite**

Run:
```bash
cd frontend && npx vitest run
```
Expected: PASS.

- [ ] **Step 3: Wails build (generates bindings + binary)**

Run:
```bash
wails build
```
Expected: `build/bin/omnigate.exe` produced; `frontend/wailsjs/go/app/App` now contains `RefreshGacha`/`GetGachaSummary`. If go.mod picked up stray toolchain edits, revert those lines (known repo habit).

- [ ] **Step 4: Commit any binding regen / go.mod cleanup**

```bash
git add -A
git commit -m "chore(gacha-p3): regenerate Wails bindings for RefreshGacha/GetGachaSummary"
```

---

## Real-machine smoke (USER, after merge-readiness)

Not an automated task — hand off to the user:
1. Launch Endfield, open the gacha history page once.
2. In omnigate: select Endfield → click 抽卡分析 tab → board appears.
3. Click 重新整理紀錄 → records pull in, stats populate; click again (cache valid) → no re-open needed.
4. Let the token expire (or clear it) → 重新整理 shows the re-open guidance, no crash.
5. Other games' 抽卡分析 tab → 「未支援」 state, no crash.
6. Topbar global refresh → gacha re-loads lazily on next view.

---

## Notes for the executor

- **Reviewer model:** every per-task review runs on `opus` (spec-compliance + code-quality), against the **uncommitted** working tree, BEFORE the task's commit. The commit steps above are deferred until both reviews return APPROVE (per subagent-review-gates).
- **No `-race`** on this host (CGO_ENABLED=0).
- **Never log a gacha URL** (it carries a live token) — enforced in Task 7.
- Ordering is by monotonic `ID`, never parsed `Time` (avoids timezone fragility).
- HoYoverse + WuWa are **Plan 2** (need a live research spike for `getGachaLog` host/biz/region, Genshin webCaches `data_2` scan, and the Kuro convene API).

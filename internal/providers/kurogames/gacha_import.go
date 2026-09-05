package kurogames

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"omnigate/internal/core"
)

// wuwatracker.com "wuwatracker-pulls" export support.
//
// The export stores each pull's timestamp converted from the record API's
// server-LOCAL string to UTC (RFC3339, e.g. "2026-06-13T06:20:36+00:00"),
// while fetchWuwa ids embed the server-local string verbatim
// ("2026-06-13 14:20:36" on UTC+8 servers). Import must convert back with the
// right server offset or (a) ids desync from fetchWuwa → the next refresh
// re-adds the same pulls as duplicates and (b) pity windows shift. The offset
// is not in the export, so buildWtPulls infers it by maximizing id overlap
// with the uid's already-stored pulls, falling back to UTC+8 (Asia/HMT/SEA).
const (
	wtDefaultOffsetHours = 8
	wtOffsetMatchMin     = 5 // min id overlaps required to trust an inferred offset
)

type wtExport struct {
	PlayerID string   `json:"playerId"`
	Pulls    []wtPull `json:"pulls"`
}

type wtPull struct {
	CardPoolType int    `json:"cardPoolType"`
	ResourceID   int    `json:"resourceId"`
	QualityLevel int    `json:"qualityLevel"`
	Name         string `json:"name"`
	Time         string `json:"time"`
	Group        int    `json:"group"` // 1-based position within a 10-pull; 1 = oldest
}

// wtOrdered is one export pull with its synthesized within-(pool,instant)
// ordinal already assigned (offset-independent: shifting whole hours never
// changes grouping or order).
type wtOrdered struct {
	pool int
	rank int
	name string
	utc  time.Time
	res  int
	ord  int
}

// ParseGachaImport implements core.GachaImportProvider for WuWa.
func (p *Provider) ParseGachaImport(gid core.GameID, data []byte, existing func(uid string) map[string]bool) (core.GachaFetchResult, error) {
	if findByID(gid) == nil {
		return core.GachaFetchResult{}, core.ErrUnknownGame
	}
	var exp wtExport
	if err := json.Unmarshal(data, &exp); err != nil {
		return core.GachaFetchResult{}, fmt.Errorf("wuwatracker export: %w", err)
	}
	if exp.PlayerID == "" || len(exp.Pulls) == 0 {
		return core.GachaFetchResult{}, fmt.Errorf("wuwatracker export: missing playerId or pulls")
	}
	// wuwatracker leaves resourceId null on ~25% of real rows (partial item
	// coverage, not schema — the SAME item appears both with and without one).
	// Backfill from same-name rows that do carry an id; a wrong itemType is
	// permanent (INSERT OR IGNORE means a later live fetch never corrects the
	// row), so best-effort resolution here matters. Names never seen with an
	// id fall back to the pool's column in wtItemTypeFor.
	nameRes := map[string]int{}
	for _, r := range exp.Pulls {
		if r.ResourceID != 0 && nameRes[r.Name] == 0 {
			nameRes[r.Name] = r.ResourceID
		}
	}
	for i := range exp.Pulls {
		if exp.Pulls[i].ResourceID == 0 {
			exp.Pulls[i].ResourceID = nameRes[exp.Pulls[i].Name]
		}
	}
	flat, err := orderWtPulls(exp.Pulls)
	if err != nil {
		return core.GachaFetchResult{}, err
	}
	var known map[string]bool
	if existing != nil {
		known = existing(exp.PlayerID)
	}
	pulls, off, matched := buildWtPulls(flat, known)
	p.logger.Info("wuwatracker import parsed",
		"uid", exp.PlayerID, "pulls", len(pulls), "offsetHours", off, "matchedExisting", matched)
	return core.GachaFetchResult{UID: exp.PlayerID, Pulls: pulls}, nil
}

// orderWtPulls assigns each pull its within-(pool,instant) ordinal. The export
// array is newest-first (like the record API) and a same-second 10-pull's rows
// carry `group` 1..10 with 1 = OLDEST; fetchWuwa assigns ordinals oldest-first,
// so each same-instant group is sorted by `group` ASCENDING before numbering.
// Getting this wrong shifts adjacent pity counts AND desyncs ids (the exact bug
// of the 2026-06 one-off import). Rows without `group` (all zero) keep the
// reversed array order — the same oldest-first treatment fetchWuwa applies.
func orderWtPulls(raw []wtPull) ([]wtOrdered, error) {
	type gkey struct {
		pool int
		utc  time.Time
	}
	groups := map[gkey][]wtOrdered{}
	order := []gkey{}
	// reverse iteration = oldest-first, mirroring fetchWuwa.
	for i := len(raw) - 1; i >= 0; i-- {
		r := raw[i]
		t, err := time.Parse(time.RFC3339, r.Time)
		if err != nil {
			return nil, fmt.Errorf("wuwatracker export: pull %d: bad time %q", i, r.Time)
		}
		k := gkey{r.CardPoolType, t.UTC()}
		if _, ok := groups[k]; !ok {
			order = append(order, k)
		}
		groups[k] = append(groups[k], wtOrdered{
			pool: r.CardPoolType, rank: r.QualityLevel, name: r.Name,
			utc: t.UTC(), res: r.ResourceID, ord: r.Group,
		})
	}
	out := make([]wtOrdered, 0, len(raw))
	for _, k := range order {
		g := groups[k]
		sort.SliceStable(g, func(i, j int) bool { return g[i].ord < g[j].ord })
		for i := range g {
			g[i].ord = i
			out = append(out, g[i])
		}
	}
	return out, nil
}

func wtID(pool int, utc time.Time, offsetHours, ord int) (id, localTime string) {
	localTime = utc.Add(time.Duration(offsetHours) * time.Hour).Format("2006-01-02 15:04:05")
	return fmt.Sprintf("w|%d|%s|%d", pool, localTime, ord), localTime
}

// inferWtOffset picks the whole-hour server offset whose synthesized ids
// overlap the most with already-stored pulls. Candidates are tried with the
// UTC+8 default first so ties keep the default; below wtOffsetMatchMin hits
// (fresh/disjoint partition) the default wins outright.
func inferWtOffset(flat []wtOrdered, known map[string]bool) (offsetHours, matched int) {
	if len(known) == 0 {
		return wtDefaultOffsetHours, 0
	}
	cands := []int{wtDefaultOffsetHours}
	for c := -12; c <= 14; c++ {
		if c != wtDefaultOffsetHours {
			cands = append(cands, c)
		}
	}
	best, bestN := wtDefaultOffsetHours, -1
	for _, c := range cands {
		n := 0
		for _, f := range flat {
			if id, _ := wtID(f.pool, f.utc, c, f.ord); known[id] {
				n++
			}
		}
		if n > bestN {
			best, bestN = c, n
		}
	}
	if bestN < wtOffsetMatchMin {
		return wtDefaultOffsetHours, 0
	}
	return best, bestN
}

// buildWtPulls converts ordered export pulls into store-ready GachaPulls.
// Names are stored VERBATIM (the export ships English): the icon index and
// wuwaStandardPool both carry en forms, and back-translating EN→ZH is exactly
// what produced the 赤春/裁春 data bug — never do it.
func buildWtPulls(flat []wtOrdered, known map[string]bool) (pulls []core.GachaPull, offsetHours, matched int) {
	offsetHours, matched = inferWtOffset(flat, known)
	pulls = make([]core.GachaPull, 0, len(flat))
	for _, f := range flat {
		id, local := wtID(f.pool, f.utc, offsetHours, f.ord)
		pulls = append(pulls, core.GachaPull{
			ID:        id,
			BannerKey: poolBanner(f.pool),
			ItemType:  wtItemTypeFor(f.res, f.pool),
			Rank:      f.rank,
			Name:      f.name,
			Time:      local,
		})
	}
	return pulls, offsetHours, matched
}

// wtItemType derives the display type from the resource id: resonators use
// 4-digit ids (1109), weapons 8-digit (21020043). Display-only, matching the
// live zh-Hant fetch's resourceType values.
func wtItemType(resourceID int) string {
	if resourceID >= 10000 {
		return "武器"
	}
	return "角色"
}

// wtItemTypeFor types a pull, falling back to the pool's column when the
// export never carried a resource id for this name. The fallback is imperfect
// BOTH ways (weapon pools drop 4★ resonators, character pools drop 3★ weapon
// filler), but after the name backfill the residue is a handful of rows and
// the 2026-08 real export resolves them all correctly.
func wtItemTypeFor(resourceID, pool int) string {
	if resourceID != 0 {
		return wtItemType(resourceID)
	}
	switch pool {
	case 2, 4, 9, 11: // weapon, standard_weapon, weapon_exchange, collab_weapon
		return "武器"
	}
	return "角色"
}

var _ core.GachaImportProvider = (*Provider)(nil)

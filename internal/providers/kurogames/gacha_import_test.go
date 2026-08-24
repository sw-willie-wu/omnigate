package kurogames

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"omnigate/internal/core"
)

func wtGid() core.GameID { return core.GameID("kurogames/wutheringwaves") }

func TestWtParse_RejectsBadInput(t *testing.T) {
	p := New(Settings{}, nil)
	cases := map[string]string{
		"bad json":    `{`,
		"no playerId": `{"pulls":[{"cardPoolType":1,"resourceId":1109,"qualityLevel":5,"name":"X","time":"2026-06-13T06:20:36+00:00","group":1}]}`,
		"empty pulls": `{"playerId":"700","pulls":[]}`,
		"bad time":    `{"playerId":"700","pulls":[{"cardPoolType":1,"resourceId":1109,"qualityLevel":5,"name":"X","time":"2026-06-13 06:20:36","group":1}]}`,
	}
	for name, body := range cases {
		if _, err := p.ParseGachaImport(wtGid(), []byte(body), nil); err == nil {
			t.Errorf("%s: expected error", name)
		}
	}
	if _, err := p.ParseGachaImport(core.GameID("kurogames/nope"), []byte(`{}`), nil); err != core.ErrUnknownGame {
		t.Errorf("unknown game: got %v", err)
	}
}

// Export arrays list a same-second 10-pull newest-first with group 10..1;
// ordinals must come out group-ASC (group g → ord g-1) or pity counts shift
// and ids desync from fetchWuwa (the 2026-06 one-off import bug).
func TestWtOrder_GroupAscWithinSameSecond(t *testing.T) {
	p := New(Settings{}, nil)
	rows := make([]string, 0, 10)
	for g := 10; g >= 1; g-- { // newest-first, like the real export
		q, name := 3, fmt.Sprintf("Filler%d", g)
		if g == 4 {
			q, name = 5, "Lucilla"
		}
		rows = append(rows, fmt.Sprintf(
			`{"cardPoolType":1,"resourceId":%d,"qualityLevel":%d,"name":"%s","time":"2026-06-13T06:20:36+00:00","group":%d}`,
			1109, q, name, g))
	}
	body := `{"playerId":"700","pulls":[` + strings.Join(rows, ",") + `]}`
	res, err := p.ParseGachaImport(wtGid(), []byte(body), nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.UID != "700" || len(res.Pulls) != 10 {
		t.Fatalf("uid=%q n=%d", res.UID, len(res.Pulls))
	}
	// default offset +8: 06:20:36 UTC → 14:20:36 local; group 4 → ord 3.
	want := "w|1|2026-06-13 14:20:36|3"
	found := false
	for _, pl := range res.Pulls {
		if pl.Name == "Lucilla" {
			found = true
			if pl.ID != want || pl.Time != "2026-06-13 14:20:36" || pl.Rank != 5 || pl.BannerKey != "character" {
				t.Fatalf("lucilla=%+v want id %s", pl, want)
			}
		}
	}
	if !found {
		t.Fatal("Lucilla not found")
	}
}

// Rows without a group field (all zero) keep reversed-array order — the same
// oldest-first treatment fetchWuwa applies to the API's newest-first data.
func TestWtOrder_NoGroupFallsBackToArrayOrder(t *testing.T) {
	p := New(Settings{}, nil)
	body := `{"playerId":"700","pulls":[
		{"cardPoolType":1,"resourceId":21010013,"qualityLevel":3,"name":"C","time":"2026-06-01T02:00:00+00:00"},
		{"cardPoolType":1,"resourceId":21010013,"qualityLevel":3,"name":"B","time":"2026-06-01T02:00:00+00:00"},
		{"cardPoolType":1,"resourceId":21010013,"qualityLevel":3,"name":"A","time":"2026-06-01T02:00:00+00:00"}]}`
	res, err := p.ParseGachaImport(wtGid(), []byte(body), nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	ids := map[string]string{}
	for _, pl := range res.Pulls {
		ids[pl.Name] = pl.ID
	}
	if ids["A"] != "w|1|2026-06-01 10:00:00|0" || ids["B"] != "w|1|2026-06-01 10:00:00|1" || ids["C"] != "w|1|2026-06-01 10:00:00|2" {
		t.Fatalf("ordinals wrong: %v", ids)
	}
}

func TestWtItemType(t *testing.T) {
	if wtItemType(1109) != "角色" || wtItemType(21020043) != "武器" {
		t.Fatal("wtItemType wrong")
	}
}

// Real exports leave resourceId null on ~25% of rows. A null row whose name
// appears elsewhere WITH an id must borrow it; a name never seen with an id
// falls back to the pool's column. Wrong types are permanent (INSERT OR
// IGNORE), so this is load-bearing, not cosmetic.
func TestWtItemType_NullResourceIDBackfill(t *testing.T) {
	p := New(Settings{}, nil)
	body := `{"playerId":"700","pulls":[
		{"cardPoolType":1,"resourceId":21020043,"qualityLevel":3,"name":"Sword of Voyager","time":"2026-06-01T02:00:03Z","group":1},
		{"cardPoolType":1,"resourceId":null,"qualityLevel":3,"name":"Sword of Voyager","time":"2026-06-01T02:00:02Z","group":1},
		{"cardPoolType":2,"resourceId":null,"qualityLevel":5,"name":"Blazing Brilliance","time":"2026-06-01T02:00:01Z","group":1},
		{"cardPoolType":9,"resourceId":null,"qualityLevel":5,"name":"Stringmaster","time":"2026-06-01T02:00:00Z","group":2},
		{"cardPoolType":11,"resourceId":null,"qualityLevel":5,"name":"Blazing Justice","time":"2026-06-01T02:00:00Z","group":1},
		{"cardPoolType":1,"resourceId":null,"qualityLevel":4,"name":"Mystery","time":"2026-06-01T02:00:00Z","group":1}]}`
	res, err := p.ParseGachaImport(wtGid(), []byte(body), nil)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	types := map[string]string{}
	for _, pl := range res.Pulls {
		types[pl.Name+"|"+pl.Time] = pl.ItemType
	}
	if types["Sword of Voyager|2026-06-01 10:00:02"] != "武器" { // backfilled by name
		t.Fatalf("name backfill failed: %v", types)
	}
	if types["Blazing Brilliance|2026-06-01 10:00:01"] != "武器" || // weapon-pool fallback
		types["Stringmaster|2026-06-01 10:00:00"] != "武器" || // pool 9 weapon_exchange
		types["Blazing Justice|2026-06-01 10:00:00"] != "武器" { // pool 11 collab_weapon
		t.Fatalf("weapon-pool fallback failed: %v", types)
	}
	if types["Mystery|2026-06-01 10:00:00"] != "角色" { // character-pool fallback
		t.Fatalf("character-pool fallback failed: %v", types)
	}
}

// wtExportJSON builds an export body from (name, rank, utcTime, group) rows,
// already newest-first.
func wtExportJSON(uid string, rows [][4]string) string {
	items := make([]string, 0, len(rows))
	for _, r := range rows {
		items = append(items, fmt.Sprintf(
			`{"cardPoolType":1,"resourceId":1109,"qualityLevel":%s,"name":"%s","time":"%s","group":%s}`,
			r[1], r[0], r[2], r[3]))
	}
	return `{"playerId":"` + uid + `","pulls":[` + strings.Join(items, ",") + `]}`
}

// With ≥wtOffsetMatchMin id overlaps, the inferred offset must beat the +8
// default (here: a UTC+1 Europe-server partition).
func TestWtOffsetInference_NonDefault(t *testing.T) {
	p := New(Settings{}, nil)
	rows := make([][4]string, 0, 6)
	known := map[string]bool{}
	for i := 0; i < 6; i++ {
		utc := time.Date(2026, 6, 1, 2, 0, i, 0, time.UTC)
		rows = append(rows, [4]string{fmt.Sprintf("N%d", i), "3", utc.Format(time.RFC3339), "1"})
		known[fmt.Sprintf("w|1|%s|0", utc.Add(1*time.Hour).Format("2006-01-02 15:04:05"))] = true
	}
	res, err := p.ParseGachaImport(wtGid(), []byte(wtExportJSON("700", rows)), func(uid string) map[string]bool {
		if uid != "700" {
			t.Fatalf("existing called with uid=%q", uid)
		}
		return known
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, pl := range res.Pulls {
		if !known[pl.ID] {
			t.Fatalf("id %s not built with inferred +1 offset", pl.ID)
		}
	}
}

// Below the overlap threshold (fresh or disjoint partition) the default +8
// wins even if a stray id happens to match some other offset.
func TestWtOffsetInference_DefaultWhenNoOverlap(t *testing.T) {
	p := New(Settings{}, nil)
	rows := [][4]string{{"Solo", "5", "2026-06-13T06:20:36+00:00", "1"}}
	res, err := p.ParseGachaImport(wtGid(), []byte(wtExportJSON("700", rows)), func(string) map[string]bool {
		return map[string]bool{"w|1|2026-06-13 07:20:36|0": true} // single +1 match < threshold
	})
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if res.Pulls[0].ID != "w|1|2026-06-13 14:20:36|0" {
		t.Fatalf("id=%s want default +8", res.Pulls[0].ID)
	}
}

// End-to-end parity: the same pulls seen through fetchWuwa (server-local API
// fixture) and through the export path (UTC times) must synthesize IDENTICAL
// ids — this is the invariant that makes refresh-after-import dedup clean.
func TestWtParity_MatchesFetchWuwa(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		json.NewDecoder(r.Body).Decode(&body)
		if body["cardPoolType"].(float64) == 1 {
			// newest-first: one distinct-second 5★ + a same-second burst of three
			w.Write([]byte(`{"code":0,"message":"success","data":[
				{"qualityLevel":5,"resourceType":"角色","name":"Alpha","count":1,"time":"2026-06-01 10:00:00"},
				{"qualityLevel":3,"resourceType":"武器","name":"C","count":1,"time":"2026-06-01 09:59:58"},
				{"qualityLevel":3,"resourceType":"武器","name":"B","count":1,"time":"2026-06-01 09:59:58"},
				{"qualityLevel":3,"resourceType":"武器","name":"A","count":1,"time":"2026-06-01 09:59:58"}]}`))
			return
		}
		w.Write([]byte(`{"code":0,"message":"success","data":[]}`))
	}))
	defer srv.Close()
	p := New(Settings{}, nil)
	p.recordAPIBase = srv.URL
	p.recordDelay = 0
	live, err := p.fetchWuwa(context.Background(), url.Values{"player_id": {"700"}, "record_id": {"R"}})
	if err != nil {
		t.Fatalf("fetchWuwa: %v", err)
	}
	liveIDs := map[string]bool{}
	for _, pl := range live.Pulls {
		liveIDs[pl.ID] = true
	}

	// The same history as wuwatracker exports it: server-local (+8) → UTC,
	// newest-first, per-burst group 1 = oldest.
	rows := [][4]string{
		{"Alpha", "5", "2026-06-01T02:00:00+00:00", "1"},
		{"C", "3", "2026-06-01T01:59:58+00:00", "3"},
		{"B", "3", "2026-06-01T01:59:58+00:00", "2"},
		{"A", "3", "2026-06-01T01:59:58+00:00", "1"},
	}
	imp, err := p.ParseGachaImport(wtGid(), []byte(wtExportJSON("700", rows)), func(string) map[string]bool { return liveIDs })
	if err != nil {
		t.Fatalf("import: %v", err)
	}
	if len(imp.Pulls) != len(live.Pulls) {
		t.Fatalf("n import=%d live=%d", len(imp.Pulls), len(live.Pulls))
	}
	for _, pl := range imp.Pulls {
		if !liveIDs[pl.ID] {
			t.Fatalf("import id %s absent from fetchWuwa ids %v", pl.ID, liveIDs)
		}
	}
}

// M2 killer: export rows whose ARRAY order within a same-second group is not
// the reverse of `group`. The existing tests feed group 10..1 newest-first, so
// the plain reversal alone already produces the right answer and the explicit
// group-ASC sort is never exercised.
func TestWtOrder_ArrayOrderDiffersFromGroup(t *testing.T) {
	p := New(Settings{}, nil)
	rows := []string{ // newest-first array, but group order 3,1,2
		`{"cardPoolType":1,"resourceId":1109,"qualityLevel":3,"name":"g3","time":"2026-06-01T02:00:00Z","group":3}`,
		`{"cardPoolType":1,"resourceId":1109,"qualityLevel":3,"name":"g1","time":"2026-06-01T02:00:00Z","group":1}`,
		`{"cardPoolType":1,"resourceId":1109,"qualityLevel":3,"name":"g2","time":"2026-06-01T02:00:00Z","group":2}`,
	}
	res, err := p.ParseGachaImport(wtGid(), []byte(`{"playerId":"700","pulls":[`+strings.Join(rows, ",")+`]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, pl := range res.Pulls {
		got[pl.Name] = pl.ID
	}
	if got["g1"] != "w|1|2026-06-01 10:00:00|0" || got["g2"] != "w|1|2026-06-01 10:00:00|1" || got["g3"] != "w|1|2026-06-01 10:00:00|2" {
		t.Fatalf("group-ASC sort not applied: %v", got)
	}
}

// M11 killer: fetchWuwa numbers ordinals PER POOL (each pool is a separate
// request with its own `ordinals` map). Every existing test uses cardPoolType 1
// only, so dropping pool from the grouping key goes unnoticed.
func TestWtOrder_OrdinalsArePerPool(t *testing.T) {
	p := New(Settings{}, nil)
	rows := []string{
		`{"cardPoolType":2,"resourceId":1109,"qualityLevel":3,"name":"p2","time":"2026-06-01T02:00:00Z","group":1}`,
		`{"cardPoolType":1,"resourceId":1109,"qualityLevel":3,"name":"p1","time":"2026-06-01T02:00:00Z","group":1}`,
	}
	res, err := p.ParseGachaImport(wtGid(), []byte(`{"playerId":"700","pulls":[`+strings.Join(rows, ",")+`]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, pl := range res.Pulls {
		got[pl.Name] = pl.ID
	}
	if got["p1"] != "w|1|2026-06-01 10:00:00|0" || got["p2"] != "w|2|2026-06-01 10:00:00|0" {
		t.Fatalf("ordinals leaked across pools: %v", got)
	}
}

// M16 killer: the REAL export mixes "Z" and "+00:00" renderings (1420 vs 1209
// rows in the 2026-08-24 sample). time.Parse gives them DIFFERENT Location
// pointers, so without the .UTC() normalization they are unequal as map keys
// and one same-second group silently splits in two.
func TestWtOrder_MixedZAndOffsetAreOneInstant(t *testing.T) {
	p := New(Settings{}, nil)
	rows := []string{
		`{"cardPoolType":1,"resourceId":1109,"qualityLevel":3,"name":"b","time":"2026-06-01T02:00:00+00:00","group":2}`,
		`{"cardPoolType":1,"resourceId":1109,"qualityLevel":3,"name":"a","time":"2026-06-01T02:00:00Z","group":1}`,
	}
	res, err := p.ParseGachaImport(wtGid(), []byte(`{"playerId":"700","pulls":[`+strings.Join(rows, ",")+`]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, pl := range res.Pulls {
		got[pl.Name] = pl.ID
	}
	if got["a"] != "w|1|2026-06-01 10:00:00|0" || got["b"] != "w|1|2026-06-01 10:00:00|1" {
		t.Fatalf("Z/+00:00 not treated as one instant: %v", got)
	}
}

// M9/M10 killer: inferWtOffset documents "candidates are tried with the UTC+8
// default first so ties keep the default" — nothing tests it.
func TestWtOffsetInference_TieKeepsDefault(t *testing.T) {
	p := New(Settings{}, nil)
	rows := []string{}
	known := map[string]bool{}
	for i := 0; i < 6; i++ {
		s := string(rune('0' + i))
		rows = append(rows, `{"cardPoolType":1,"resourceId":1109,"qualityLevel":3,"name":"n`+s+`","time":"2026-06-01T02:00:0`+s+`Z","group":1}`)
		known[`w|1|2026-06-01 10:00:0`+s+`|0`] = true // +8
		known[`w|1|2026-06-01 11:00:0`+s+`|0`] = true // +9, identical overlap count
	}
	res, err := p.ParseGachaImport(wtGid(), []byte(`{"playerId":"700","pulls":[`+strings.Join(rows, ",")+`]}`), func(string) map[string]bool { return known })
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(res.Pulls[0].ID, "w|1|2026-06-01 10:") {
		t.Fatalf("tie did not keep the +8 default: %s", res.Pulls[0].ID)
	}
}

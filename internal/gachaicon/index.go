package gachaicon

import (
	"encoding/json"
	"fmt"
	"strconv"

	"omnigate/internal/core"
)

// Entry is one resolved item: source id, icon ref, and char/weapon kind.
type Entry struct {
	ID      string `json:"id"`
	IconRef string `json:"iconRef"`
	Kind    string `json:"kind"` // "char" | "weapon"
}

// Index holds per-game name→Entry and id→Entry maps. Read-only once built; the
// Manager swaps whole *Index values under a lock (never mutates a live one).
type Index struct {
	names map[core.GameID]map[string]Entry
	equip map[core.GameID]map[string]Entry
	byID  map[core.GameID]map[string]Entry
}

func NewIndex() *Index {
	return &Index{
		names: map[core.GameID]map[string]Entry{},
		equip: map[core.GameID]map[string]Entry{},
		byID:  map[core.GameID]map[string]Entry{},
	}
}

func (x *Index) put(gid core.GameID, name string, e Entry) {
	if x.names[gid] == nil {
		x.names[gid] = map[string]Entry{}
	}
	x.names[gid][Canonicalize(name)] = e
	x.indexByID(gid, e)
}
func (x *Index) putEquip(gid core.GameID, name string, e Entry) {
	if x.equip[gid] == nil {
		x.equip[gid] = map[string]Entry{}
	}
	x.equip[gid][Canonicalize(name)] = e
	x.indexByID(gid, e)
}
func (x *Index) indexByID(gid core.GameID, e Entry) {
	if x.byID[gid] == nil {
		x.byID[gid] = map[string]Entry{}
	}
	if e.ID != "" {
		x.byID[gid][e.ID] = e
	}
}

// Resolve looks up an item by record name + equip flag. For ZZZ/WuWa a miss is
// retried with the 繁→簡 (t2s) form. equip=true checks the weapon-side map first.
func (x *Index) Resolve(gid core.GameID, name string, equip bool) (Entry, bool) {
	primary, secondary := x.names[gid], x.equip[gid]
	if equip {
		primary, secondary = x.equip[gid], x.names[gid]
	}
	cn := Canonicalize(name)
	if e, ok := primary[cn]; ok {
		return e, true
	}
	if e, ok := secondary[cn]; ok {
		return e, true
	}
	if isHakush(gid) {
		s := t2s(cn)
		if e, ok := primary[s]; ok {
			return e, true
		}
		if e, ok := secondary[s]; ok {
			return e, true
		}
	}
	return Entry{}, false
}

// EntryByID returns the Entry for a resolved id (used by ServeIcon).
func (x *Index) EntryByID(gid core.GameID, id string) (Entry, bool) {
	e, ok := x.byID[gid][id]
	return e, ok
}

func isHakush(gid core.GameID) bool {
	return gid == "hoyoverse/zzz" || gid == "kurogames/wutheringwaves"
}

// buildFromAmber/Yatta parse Project Amber/Yatta {lang}->json envelopes into the
// index (kind = "char"/"weapon"; topRank filters to that rarity). Keys every lang.
func buildFromAmber(idx *Index, kind string, topRank int, byLang map[string][]byte) error {
	return buildAY(idx, core.GameID("hoyoverse/genshin"), kind, topRank, byLang)
}
func buildFromYatta(idx *Index, kind string, topRank int, byLang map[string][]byte) error {
	return buildAY(idx, core.GameID("hoyoverse/starrail"), kind, topRank, byLang)
}

func buildAY(idx *Index, gid core.GameID, kind string, topRank int, byLang map[string][]byte) error {
	// Every language's name is keyed into the same per-game map, so a later
	// language overwrites an earlier one for the same name key — harmless since
	// the overwrite carries the same id/Entry (only the language of the key set differs).
	for _, raw := range byLang {
		var env ayEnvelope
		if err := json.Unmarshal(raw, &env); err != nil {
			return fmt.Errorf("ay parse: %w", err)
		}
		for _, it := range env.Data.Items {
			if it.Rank != topRank {
				continue
			}
			e := Entry{ID: toIDString(it.ID), IconRef: it.Icon, Kind: kind}
			if kind == "weapon" {
				idx.putEquip(gid, it.Name, e)
			} else {
				idx.put(gid, it.Name, e)
			}
		}
	}
	return nil
}

func toIDString(v any) string {
	switch n := v.(type) {
	case float64:
		return strconv.FormatInt(int64(n), 10)
	case string:
		return n
	default:
		return fmt.Sprintf("%v", n)
	}
}

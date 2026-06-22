package gachaicon

import (
	"encoding/json"
	"fmt"
	"strings"

	"omnigate/internal/core"
)

// OBSERVED HAKUSH-FAMILY SHAPE (captured 2026-06-22 — live).
//
// NOTE: hakush.in shut down on 2026-02-14 (Shanghai police crackdown). The data
// is now served by the revived "nanoka.cc" mirror. The old flat
// `https://api.hakush.in/<game>/data/character.json` endpoint is DEAD (api.hakush.in
// is authoritative NXDOMAIN). The new layout is VERSIONED:
//
//	base host : https://static.nanoka.cc
//	manifest  : https://static.nanoka.cc/manifest.json   (per-game {latest, available[], live, new})
//	data      : https://static.nanoka.cc/<game>/<version>/<endpoint>.json
//	              e.g. https://static.nanoka.cc/ww/3.5.3/character.json
//	                   https://static.nanoka.cc/ww/3.5.3/weapon.json
//	                   https://static.nanoka.cc/zzz/3.1.2+16857772/character.json
//	games     : ww (Wuthering Waves), zzz (Zenless Zone Zero), gi, hsr, nte
//
// TOP-LEVEL SHAPE (all four files): a JSON OBJECT keyed by id-string, e.g.
//	{ "1104": { ...item... }, "1105": { ... } }   (the key IS the item id)
//
// PER-ITEM FIELDS we use:
//	rank : int rarity. WuWa scale 1..5, top = 5. ZZZ scale 2..4, top = 4 (S-rank).
//	       (caller passes topRank, so this func filters to whatever it's given.)
//	icon : string. FORMAT DIFFERS PER GAME:
//	         WuWa : full Unreal objectpath, e.g.
//	                "/Game/Aki/UI/UIResources/Common/Image/IconRoleHead256/T_IconRoleHead256_14_UI.T_IconRoleHead256_14_UI"
//	                (chars use .../IconRoleHead256/..., weapons use .../IconWeapon/...)
//	         ZZZ  : short code, e.g. "IconRole01" (char) / "Weapon_S_1021" (w-engine).
//	names: TOP-LEVEL keys "en","ja","ko","zh". zh is SIMPLIFIED Chinese (no zh-tw),
//	       which is why Resolve() bridges 繁→簡 via t2s for these games. Some forms
//	       may be absent (e.g. a few entries have no en) — absent decodes to "".
//
// CONFIRMED ICON URL PATTERNS (each curled → HTTP 200, content-type image/webp):
//	WuWa : https://static.nanoka.cc/assets/ww/<icon w/ "/Game/Aki/UI/" stripped, cut at first '.'>.webp
//	         e.g. .../assets/ww/UIResources/Common/Image/IconRoleHead256/T_IconRoleHead256_14_UI.webp
//	ZZZ  : https://static.nanoka.cc/assets/zzz/<icon code (trailing img ext stripped)>.webp
//	         e.g. .../assets/zzz/IconRole01.webp
//	(transform reverse-engineered from the nanoka.cc SvelteKit bundle's ww/zzz chunks;
//	 see wuwaIconURL/zzzIconURL in sources.go.)

// hakushItem is one id-keyed record from a nanoka.cc <game>/<version>/{character,weapon}.json.
type hakushItem struct {
	Rank int    `json:"rank"`
	Icon string `json:"icon"`
	EN   string `json:"en"`
	JA   string `json:"ja"`
	KO   string `json:"ko"`
	ZH   string `json:"zh"`
}

// buildFromHakush parses a nanoka.cc (ex-Hakush) id-keyed item file into the index.
// kind is "char"/"weapon". topRank>0 filters to that rarity (skip when rank != topRank);
// topRank==0 means "all ranks" — unlike buildAY, which always filters by topRank.
// Every non-empty name form (zh/en/ja/ko) is keyed to the same Entry, using the map
// key as the item id. Keying every language form is last-writer-wins when a name is
// shared across items (harmless: same roster, so the same Entry wins), as on buildAY.
func buildFromHakush(idx *Index, gid core.GameID, kind string, topRank int, raw []byte) error {
	var items map[string]hakushItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return fmt.Errorf("hakush parse (%s %s): %w", gid, kind, err)
	}
	for id, it := range items {
		if topRank > 0 && it.Rank != topRank {
			continue
		}
		e := Entry{ID: id, IconRef: it.Icon, Kind: kind}
		for _, name := range []string{it.ZH, it.EN, it.JA, it.KO} {
			if strings.TrimSpace(name) == "" {
				continue
			}
			if kind == "weapon" {
				idx.putEquip(gid, name, e)
			} else {
				idx.put(gid, name, e)
			}
		}
	}
	return nil
}

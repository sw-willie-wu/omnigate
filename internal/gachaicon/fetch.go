package gachaicon

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"omnigate/internal/core"
)

// amberLangs / yattaLangs are the languages keyed into the GI / HSR indices
// (Traditional + Simplified + English so record names in any of them resolve).
// The two sites use DIFFERENT simplified-Chinese codes: Amber (GI, gi.yatta.moe)
// serves "chs", Yatta (HSR, sr.yatta.moe) serves "cn" — requesting "chs" on
// sr.yatta.moe 404s. They must not share one list.
var amberLangs = []string{"cht", "chs", "en"}
var yattaLangs = []string{"cht", "cn", "en"}

// fetchAYLangs GETs {base}/api/v2/{lang}/{ep} for each lang, SKIPPING langs that
// fail rather than aborting: a single language's outage (or a renamed lang code)
// must not zero the whole index. Returns the bodies that succeeded, and errors
// only when none did (so a total outage still surfaces as a warm failure).
func (m *Manager) fetchAYLangs(base string, langs []string, ep string) (map[string][]byte, error) {
	out := map[string][]byte{}
	var lastErr error
	for _, l := range langs {
		b, err := m.httpGet(base + "/api/v2/" + l + "/" + ep)
		if err != nil {
			lastErr = err
			continue
		}
		out[l] = b
	}
	if len(out) == 0 {
		return nil, lastErr
	}
	return out, nil
}

// httpGet does a GET via the Manager's client and returns the body on HTTP 200.
func (m *Manager) httpGet(url string) ([]byte, error) {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := m.hc.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GET %s -> %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// nanokaLatest reads <game>.latest from the nanoka manifest (promoted from the
// matchrate reference test). game is the nanoka slug, e.g. "ww" or "zzz".
func (m *Manager) nanokaLatest(game string) (string, error) {
	b, err := m.httpGet("https://static.nanoka.cc/manifest.json")
	if err != nil {
		return "", err
	}
	var mani map[string]struct {
		Latest string `json:"latest"`
	}
	if err := json.Unmarshal(b, &mani); err != nil {
		return "", fmt.Errorf("nanoka manifest parse: %w", err)
	}
	if mani[game].Latest == "" {
		return "", fmt.Errorf("no %s.latest in nanoka manifest", game)
	}
	return mani[game].Latest, nil
}

// fetchSources builds a fresh *Index for gid from its live data sources.
//   - GI  : gi.yatta.moe Amber avatar/weapon (rank-5).
//   - HSR : sr.yatta.moe Yatta avatar/equipment (rank-5).
//   - ZZZ : nanoka versioned character/weapon (rank-4 = S-rank).
//   - WuWa: nanoka versioned character/weapon (rank-5).
//   - Endfield: no source yet → empty index.
func fetchSources(m *Manager, gid core.GameID) (*Index, error) {
	idx := NewIndex()
	switch gid {
	case "hoyoverse/genshin":
		avatar, err := m.fetchAYLangs("https://gi.yatta.moe", amberLangs, "avatar")
		if err != nil {
			return nil, err
		}
		weapon, err := m.fetchAYLangs("https://gi.yatta.moe", amberLangs, "weapon")
		if err != nil {
			return nil, err
		}
		if err := buildFromAmber(idx, "char", 5, avatar); err != nil {
			return nil, err
		}
		if err := buildFromAmber(idx, "weapon", 5, weapon); err != nil {
			return nil, err
		}
	case "hoyoverse/starrail":
		avatar, err := m.fetchAYLangs("https://sr.yatta.moe", yattaLangs, "avatar")
		if err != nil {
			return nil, err
		}
		equip, err := m.fetchAYLangs("https://sr.yatta.moe", yattaLangs, "equipment")
		if err != nil {
			return nil, err
		}
		if err := buildFromYatta(idx, "char", 5, avatar); err != nil {
			return nil, err
		}
		if err := buildFromYatta(idx, "weapon", 5, equip); err != nil {
			return nil, err
		}
	case "hoyoverse/zzz":
		v, err := m.nanokaLatest("zzz")
		if err != nil {
			return nil, err
		}
		ch, err := m.httpGet("https://static.nanoka.cc/zzz/" + v + "/character.json")
		if err != nil {
			return nil, err
		}
		wp, err := m.httpGet("https://static.nanoka.cc/zzz/" + v + "/weapon.json")
		if err != nil {
			return nil, err
		}
		if err := buildFromHakush(idx, gid, "char", 4, ch); err != nil {
			return nil, err
		}
		if err := buildFromHakush(idx, gid, "weapon", 4, wp); err != nil {
			return nil, err
		}
	case "kurogames/wutheringwaves":
		v, err := m.nanokaLatest("ww")
		if err != nil {
			return nil, err
		}
		ch, err := m.httpGet("https://static.nanoka.cc/ww/" + v + "/character.json")
		if err != nil {
			return nil, err
		}
		wp, err := m.httpGet("https://static.nanoka.cc/ww/" + v + "/weapon.json")
		if err != nil {
			return nil, err
		}
		if err := buildFromHakush(idx, gid, "char", 5, ch); err != nil {
			return nil, err
		}
		if err := buildFromHakush(idx, gid, "weapon", 5, wp); err != nil {
			return nil, err
		}
	case "hypergryph/endfield":
		token, err := m.skportGuestToken()
		if err != nil {
			return nil, err // total failure → err (do NOT swallow to empty+nil; would stamp fetchedAt → 7d TTL lockout)
		}
		built := 0
		for _, lang := range []string{"zh_Hant", "en"} { // zh_Hans is empty on skport; build-time t2s covers zh-cn
			if ch, err := m.skportGet("/web/v1/wiki/item/catalog", "typeMainId=1&typeSubId=1", token, lang); err == nil {
				if buildFromEndfieldCatalog(idx, gid, "char", ch) == nil {
					built++
				}
			}
			if wp, err := m.skportGet("/web/v1/wiki/item/catalog", "typeMainId=1&typeSubId=2", token, lang); err == nil {
				if buildFromEndfieldCatalog(idx, gid, "weapon", wp) == nil {
					built++
				}
			}
		}
		if built == 0 {
			return nil, fmt.Errorf("endfield catalog: all languages failed")
		}
	}
	return idx, nil
}

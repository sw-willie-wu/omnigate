package gachaicon

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"omnigate/internal/core"
)

// yattaLangs are the languages keyed into the GI/HSR (Amber/Yatta) indices.
// Traditional + Simplified + English so record names in any of them resolve.
var yattaLangs = []string{"cht", "chs", "en"}

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
		avatar := map[string][]byte{}
		weapon := map[string][]byte{}
		for _, l := range yattaLangs {
			b, err := m.httpGet("https://gi.yatta.moe/api/v2/" + l + "/avatar")
			if err != nil {
				return nil, err
			}
			avatar[l] = b
			b, err = m.httpGet("https://gi.yatta.moe/api/v2/" + l + "/weapon")
			if err != nil {
				return nil, err
			}
			weapon[l] = b
		}
		if err := buildFromAmber(idx, "char", 5, avatar); err != nil {
			return nil, err
		}
		if err := buildFromAmber(idx, "weapon", 5, weapon); err != nil {
			return nil, err
		}
	case "hoyoverse/starrail":
		avatar := map[string][]byte{}
		equip := map[string][]byte{}
		for _, l := range yattaLangs {
			b, err := m.httpGet("https://sr.yatta.moe/api/v2/" + l + "/avatar")
			if err != nil {
				return nil, err
			}
			avatar[l] = b
			b, err = m.httpGet("https://sr.yatta.moe/api/v2/" + l + "/equipment")
			if err != nil {
				return nil, err
			}
			equip[l] = b
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
		// No public icon source yet — serve an empty (but valid) index.
	}
	return idx, nil
}

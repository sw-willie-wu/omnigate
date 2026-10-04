package kurogames

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
)

const installStateFile = "launcherDownloadConfig.json"

type installedBundle struct {
	Version       string
	ResourcePacks []string
	State         string
	Raw           map[string]any
}

type installState struct {
	Version string
	Bundles map[string]installedBundle
	Legacy  bool
	Raw     map[string]any
}

func readInstallState(path string) (installState, error) {
	s := installState{Bundles: map[string]installedBundle{}, Raw: map[string]any{}}
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		s.Legacy = true
		return s, nil
	}
	if err != nil {
		return s, err
	}
	data = bytes.TrimPrefix(data, []byte("\xef\xbb\xbf")) // UTF-8 BOM (Review Focus #1)
	if err := json.Unmarshal(data, &s.Raw); err != nil {
		return s, fmt.Errorf("kurogames: parse %s: %w", installStateFile, err)
	}
	s.Version, _ = s.Raw["version"].(string)
	rawBundles, hasBundles := s.Raw["bundles"].(map[string]any)
	if !hasBundles {
		s.Legacy = true
		if s.Version != "" {
			s.Bundles[legacyBundle] = installedBundle{Version: s.Version, ResourcePacks: []string{"common", packOf(legacyBundle)}, Raw: map[string]any{"state": ""}}
		}
		return s, nil
	}
	for name, v := range rawBundles {
		m, _ := v.(map[string]any)
		if m == nil {
			m = map[string]any{}
		}
		b := installedBundle{Raw: m}
		b.Version, _ = m["version"].(string)
		b.State, _ = m["state"].(string)
		if arr, ok := m["resourcePacks"].([]any); ok {
			for _, x := range arr {
				if str, ok := x.(string); ok {
					b.ResourcePacks = append(b.ResourcePacks, str)
				}
			}
		}
		s.Bundles[name] = b
	}
	return s, nil
}

func (s installState) filterKnown(pending bool) []string {
	var out []string
	for _, n := range bundlePriority {
		b, ok := s.Bundles[n]
		if !ok {
			continue
		}
		if (b.State != "") == pending {
			out = append(out, n)
		}
	}
	return out
}

func (s installState) installedKnown() []string { return s.filterKnown(false) }
func (s installState) pendingKnown() []string   { return s.filterKnown(true) }

func (s installState) packVersion(pack string) string {
	if pack == "common" {
		min := ""
		for _, n := range s.installedKnown() {
			v := s.Bundles[n].Version
			if min == "" || versionLess(v, min) {
				min = v
			}
		}
		return min
	}
	for _, n := range s.installedKnown() {
		if packOf(n) == pack {
			return s.Bundles[n].Version
		}
	}
	return ""
}

func writeInstallState(path string, mutate func(*installState)) error {
	s, err := readInstallState(path)
	if err != nil {
		return err
	}
	mutate(&s)
	doc := map[string]any{}
	for k, v := range s.Raw {
		doc[k] = v
	}
	doc["version"] = s.Version
	bundles := map[string]any{}
	names := make([]string, 0, len(s.Bundles))
	for n := range s.Bundles {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		b := s.Bundles[n]
		m := map[string]any{}
		for k, v := range b.Raw {
			m[k] = v
		}
		if _, ok := m["state"]; !ok {
			m["state"] = ""
		}
		m["version"] = b.Version
		packs := b.ResourcePacks
		if len(packs) == 0 {
			packs = []string{"common", packOf(n)}
		}
		m["resourcePacks"] = packs
		bundles[n] = m
	}
	doc["bundles"] = bundles
	out, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, out, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// versionLess compares dotted numeric versions; "" sorts first. A non-numeric
// segment falls back to string comparison for that segment.
func versionLess(a, b string) bool {
	if a == b {
		return false
	}
	if a == "" {
		return true
	}
	if b == "" {
		return false
	}
	as, bs := strings.Split(a, "."), strings.Split(b, ".")
	for i := 0; i < len(as) || i < len(bs); i++ {
		var x, y string
		if i < len(as) {
			x = as[i]
		}
		if i < len(bs) {
			y = bs[i]
		}
		xi, xerr := strconv.Atoi(x)
		yi, yerr := strconv.Atoi(y)
		if xerr == nil && yerr == nil {
			if xi != yi {
				return xi < yi
			}
			continue
		}
		if x != y {
			return x < y
		}
	}
	return false
}

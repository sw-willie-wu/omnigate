package kurogames

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"omnigate/internal/core"
)

const gameIndexPath = "launcher/game/50004_P7xcUZnEr1AXIGON25E6KjpOgTlVrg6e/G153/official/index.json"

// gameIndexURLs is a var so tests can point at httptest servers. Primary
// first; the backup host is only tried when the primary fails.
var gameIndexURLs = func() []string {
	return []string{
		"https://prod-alicdn-gamestarter.kurogame.com/" + gameIndexPath,
		"https://prod-volcdn-gamestarter.kurogame.net/" + gameIndexPath,
	}
}

var bundleLaunchArgs = map[string]string{"SD": "-krqlv=sd", "HD": "-krqlv=hd", "UHD": "-krqlv=uhd"}
var bundlePriority = []string{"UHD", "HD", "SD"}

const legacyBundle = "HD"

var bundleNameRe = regexp.MustCompile(`^[A-Z0-9]+$`)

func knownBundle(name string) bool { _, ok := bundleLaunchArgs[name]; return ok }
func packOf(bundle string) string  { return strings.ToLower(bundle) }

type gameIndexV3 struct {
	CDNList       []cdnEntry                `json:"cdnList"`
	ResourcePacks map[string]indexConfigRaw `json:"resourcePacks"`
	Bundles       map[string]bundleRaw      `json:"bundles"`
	Config        gameConfigRaw             `json:"config"`
	Predownload   json.RawMessage           `json:"predownload"`
}

type gameConfigRaw struct{} // top-level config: nothing consumed (options come from bundles)

type bundleRaw struct {
	ResourcePacks []string        `json:"resourcePacks"`
	Config        bundleConfigRaw `json:"config"`
}

type bundleConfigRaw struct {
	DisplayName     map[string]string  `json:"displayName"`
	CommandSwitch   int                `json:"commandSwitch"`
	CommandList     []extendCommandRaw `json:"commandList"`
	RHIOptionSwitch int                `json:"RHIOptionSwitch"`
	RHIOptionList   []rhiOptionRaw     `json:"RHIOptionList"`
}

type extendCommandRaw struct {
	ID      string            `json:"id"`
	Cmd     string            `json:"cmd"`
	Default int               `json:"default"`
	Text    map[string]string `json:"text"`
}

type rhiOptionRaw struct {
	CmdOption string            `json:"cmdOption"`
	IsShow    int               `json:"isShow"`
	Text      map[string]string `json:"text"`
}

func manifestInvalid(reason string) *core.UpdateError {
	return &core.UpdateError{Code: "manifest_invalid", Retryable: false, Params: map[string]string{"reason": reason}}
}

func toLocalized(m map[string]string) core.LocalizedString {
	out := core.LocalizedString{}
	for src, dst := range map[string]string{"zh-Hans": "zh-CN", "zh-Hant": "zh-TW", "en": "en"} {
		if v := m[src]; v != "" {
			out[dst] = v
		}
	}
	return out
}

func bundleOptions(b bundleRaw) []core.LaunchOption {
	var out []core.LaunchOption
	seen := map[string]bool{}
	add := func(cmd string, label map[string]string, def bool) {
		if cmd == "" || seen[cmd] {
			return
		}
		if strings.HasPrefix(cmd, "-krqlv=") {
			return // quality comes from the bundle, never from an option (spec §2.2)
		}
		seen[cmd] = true
		out = append(out, core.LaunchOption{Cmd: cmd, Label: toLocalized(label), Default: def})
	}
	if b.Config.CommandSwitch == 1 {
		for _, c := range b.Config.CommandList {
			add(c.Cmd, c.Text, c.Default == 1)
		}
	}
	if b.Config.RHIOptionSwitch == 1 {
		for _, r := range b.Config.RHIOptionList {
			if r.IsShow == 1 {
				add(r.CmdOption, r.Text, false)
			}
		}
	}
	return out
}

// safeVersionSegment reports whether v is usable as one path segment:
// non-empty, not "."/"..", no separators.
func safeVersionSegment(v string) bool {
	return v != "" && v != "." && v != ".." && !strings.ContainsAny(v, `/\`) && filepath.Base(v) == v
}

func validateIndexV3(idx *gameIndexV3) error {
	if _, ok := idx.ResourcePacks["common"]; !ok {
		return manifestInvalid("resourcePacks missing common")
	}
	// Pack versions become sidecar dir names (<temp>/<gid>/<version>/), so each
	// must be a single safe path segment.
	for pack, rp := range idx.ResourcePacks {
		if !safeVersionSegment(rp.Version) {
			return manifestInvalid(fmt.Sprintf("bad pack version %q for %s", rp.Version, pack))
		}
		for _, pc := range rp.PatchConfig {
			if !safeVersionSegment(pc.Version) {
				return manifestInvalid(fmt.Sprintf("bad pack version %q in %s patchConfig", pc.Version, pack))
			}
		}
	}
	for name, b := range idx.Bundles {
		if !bundleNameRe.MatchString(name) {
			return manifestInvalid("bad bundle name " + name)
		}
		if len(b.ResourcePacks) != 2 || b.ResourcePacks[0] != "common" || b.ResourcePacks[1] != packOf(name) {
			return manifestInvalid("bad bundle shape " + name)
		}
		if _, ok := idx.ResourcePacks[b.ResourcePacks[1]]; !ok {
			return manifestInvalid("bundle " + name + " references missing pack")
		}
	}
	return nil
}

// packPaths is the game-dir-relative path set of an indexFile (spec §2.3):
// full → resource dests; patch → non-krpdiff resource dests ∪ group src/dst ∪ deleteFiles.
func packPaths(f *indexFileRaw) []string {
	groupDiffs := map[string]bool{}
	for _, g := range f.GroupInfos {
		groupDiffs[g.Dest] = true
	}
	var out []string
	for _, r := range f.Resource {
		if !groupDiffs[r.Dest] {
			out = append(out, r.Dest)
		}
	}
	for _, g := range f.GroupInfos {
		for _, s := range g.SrcFiles {
			out = append(out, s.Dest)
		}
		for _, d := range g.DstFiles {
			out = append(out, d.Dest)
		}
	}
	return append(out, f.DeleteFiles...)
}

func validatePackIndexFile(idx *gameIndexV3, pack string, f *indexFileRaw) error {
	if pack == "common" {
		for _, p := range packPaths(f) {
			for b := range idx.Bundles {
				if strings.HasPrefix(p, "Client/Content/"+b+"/") {
					return manifestInvalid("common path in bundle dir: " + p)
				}
			}
		}
		return nil
	}
	prefix := "Client/Content/" + strings.ToUpper(pack) + "/"
	for _, p := range packPaths(f) {
		if !strings.HasPrefix(p, prefix) {
			return manifestInvalid(pack + " path outside " + prefix + ": " + p)
		}
	}
	return nil
}

func fetchGameIndexV3(ctx context.Context, client *http.Client) (*gameIndexV3, error) {
	var lastErr error
	for _, u := range gameIndexURLs() {
		body, _, err := fetchJSON(ctx, client, u)
		if err != nil {
			lastErr = err
			continue
		}
		var idx gameIndexV3
		if err := json.Unmarshal(body, &idx); err != nil {
			lastErr = fmt.Errorf("v3 index parse: %w", err)
			continue
		}
		if err := validateIndexV3(&idx); err != nil {
			return nil, err // a well-formed but invalid contract is not a host problem
		}
		return &idx, nil
	}
	return nil, lastErr
}

func fetchPackIndexFile(ctx context.Context, client *http.Client, cdn string, cfg indexConfigRaw) (*indexFileRaw, error) {
	body, _, err := fetchJSON(ctx, client, cdn+cfg.IndexFile)
	if err != nil {
		return nil, err
	}
	sum := md5.Sum(body)
	if cfg.IndexFileMD5 != "" && !strings.EqualFold(hex.EncodeToString(sum[:]), cfg.IndexFileMD5) {
		return nil, &core.UpdateError{Code: "manifest_changed", Retryable: true, Params: map[string]string{"reason": "indexFileMd5 mismatch", "file": cfg.IndexFile}}
	}
	var f indexFileRaw
	if err := json.Unmarshal(body, &f); err != nil {
		return nil, fmt.Errorf("indexFile json parse: %w", err)
	}
	return &f, nil
}

func planToken(idx *gameIndexV3, targets []string) string {
	sorted := append([]string(nil), targets...)
	sort.Strings(sorted)
	var sb strings.Builder
	for _, name := range sorted {
		c := idx.ResourcePacks[name]
		fmt.Fprintf(&sb, "%s|%s|%s\n", name, c.Version, strings.ToLower(c.IndexFileMD5))
	}
	sum := md5.Sum([]byte(sb.String()))
	return hex.EncodeToString(sum[:])
}

func catalogFromIndex(idx *gameIndexV3, now time.Time) core.BundleCatalog {
	cat := core.BundleCatalog{FetchedAt: now, CommonVersion: idx.ResourcePacks["common"].Version}
	rank := func(n string) int {
		for i, p := range bundlePriority {
			if p == n {
				return i
			}
		}
		return len(bundlePriority)
	}
	names := make([]string, 0, len(idx.Bundles))
	for n := range idx.Bundles {
		names = append(names, n)
	}
	sort.Slice(names, func(i, j int) bool {
		if rank(names[i]) != rank(names[j]) {
			return rank(names[i]) < rank(names[j])
		}
		return names[i] < names[j]
	})
	for _, n := range names {
		b := idx.Bundles[n]
		pk := idx.ResourcePacks[packOf(n)]
		cat.Bundles = append(cat.Bundles, core.BundleCatalogEntry{
			Name: n, DisplayName: toLocalized(b.Config.DisplayName),
			PackSize: pk.Size, PackVersion: pk.Version, Options: bundleOptions(b),
		})
	}
	return cat
}

var _ = errors.New // keep errors import if unused after edits

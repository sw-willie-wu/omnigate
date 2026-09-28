package hoyoverse

import (
	"bufio"
	"encoding/json"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"omnigate/internal/core"
	pb "omnigate/internal/providers/hoyoverse/sophon/proto"
)

// onDemandSkipper decides, per category, which manifest assets may be left to
// the game's own on-demand downloader instead of being planned by omnigate.
//
// Star Rail keeps a game-maintained DownloadBlacklist.json (see
// gameMeta.OnDemandBlacklistRel) of assets it fetches in-game only when
// needed; HoYoPlay never fills those in on update. An asset is skipped when it
// is listed there AND absent locally; a "<base>_<md5>.hash" install marker is
// skipped whenever its sibling "<base>.<ext>" was skipped, so the on-disk
// pck/hash pairing invariant is never broken (an orphan marker would make the
// game believe a voice chapter is installed).
//
// Blacklist lookups are case-sensitive (the file and the manifests agree on
// case in practice); a case-only mismatch errs on the side of planning the
// asset, never on silently skipping it.
//
// NOT goroutine-safe: Decide mutates state; call it only from the single-
// threaded planner loops (never from verifyMatchesParallel workers).
type onDemandSkipper struct {
	gameDir string
	set     map[string]struct{}      // blacklist entries, "/"-separated, normalised
	decided map[string]bool          // assetName → skipped? (populated by Decide; count-once)
	skipped map[string]*onDemandStat // category → accounting (log only; Bytes = AssetSize)
}

// onDemandStat is the per-category accounting emitted by LogSummary.
type onDemandStat struct {
	N     int
	Bytes int64
}

// companionRe matches the base name of a "<base>_<md5>.hash" install marker.
var companionRe = regexp.MustCompile(`^(.*)_[0-9a-f]{32}\.hash$`)

// loadOnDemandSkipper returns nil when gid has no OnDemandBlacklistRel. A
// missing blacklist yields an empty (non-nil) skipper; an unreadable one is
// logged and treated as empty. It never returns an error: the worst case is
// planning the on-demand content, never blocking an update.
func loadOnDemandSkipper(gid core.GameID, gameDir string, logger *slog.Logger) *onDemandSkipper {
	g := findByID(gid)
	if g == nil || g.OnDemandBlacklistRel == "" {
		return nil
	}
	if logger == nil {
		logger = slog.Default()
	}
	s := &onDemandSkipper{
		gameDir: gameDir,
		set:     map[string]struct{}{},
		decided: map[string]bool{},
		skipped: map[string]*onDemandStat{},
	}
	p := filepath.Join(gameDir, filepath.FromSlash(g.OnDemandBlacklistRel))
	f, err := os.Open(p)
	if err != nil {
		if !os.IsNotExist(err) {
			logger.Warn("sophon plan: on-demand blacklist unreadable", "path", p, "err", err)
		}
		return s
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	malformed := 0
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}
		var rec struct {
			FileName string `json:"fileName"`
		}
		if err := json.Unmarshal([]byte(line), &rec); err != nil || rec.FileName == "" {
			malformed++
			continue
		}
		name := strings.ReplaceAll(rec.FileName, `\`, "/")
		name = strings.TrimPrefix(name, "./")
		s.set[name] = struct{}{}
	}
	if err := sc.Err(); err != nil {
		// Directory-as-file on Windows surfaces here rather than at Open.
		logger.Warn("sophon plan: on-demand blacklist unreadable", "path", p, "err", err)
		s.set = map[string]struct{}{}
		return s
	}
	logger.Debug("sophon plan: on-demand blacklist loaded", "path", p, "entries", len(s.set), "malformed", malformed)
	return s
}

// Decide evaluates every asset of one category exactly once. probe(asset)
// returns the gameDir-relative path whose existence decides "absent" — the
// asset itself, or PatchInstr.OldFile for MethodPatch assets. A nil probe
// means identity (build/full flavor, where there are no patch records).
//
// Two passes: the base rule first (blacklisted ∧ probe path absent), which
// registers the stems of skipped assets; then companion markers, which follow
// their stem's verdict. Stem registration is local to this call, so the
// companion rule never crosses categories. An asset decided in an earlier
// call keeps its verdict (first-wins, for companions too) and is not
// accounted again, but still registers its stem so a companion listed in
// this category follows it. Asset names do not repeat across categories in
// practice, so first-wins never changes a verdict.
func (s *onDemandSkipper) Decide(category string, assets []*pb.SophonManifestAssetProperty, probe func(assetName string) string) {
	if s == nil {
		return
	}
	if probe == nil {
		probe = func(n string) string { return n }
	}
	skippedStems := map[string]bool{}
	stem := func(name string) string { return strings.TrimSuffix(name, path.Ext(name)) }
	isCompanion := func(name string) (string, bool) {
		m := companionRe.FindStringSubmatch(path.Base(name))
		if m == nil {
			return "", false
		}
		if d := path.Dir(name); d != "." {
			return d + "/" + m[1], true
		}
		return m[1], true
	}
	baseVerdict := func(name string) bool {
		if _, listed := s.set[name]; !listed {
			return false
		}
		return s.absent(probe(name))
	}
	settle := func(category, name string, size int64, verdict bool) {
		s.decided[name] = verdict
		if !verdict {
			return
		}
		st := s.skipped[category]
		if st == nil {
			st = &onDemandStat{}
			s.skipped[category] = st
		}
		st.N++
		st.Bytes += size
	}

	// Pass 1: base rule for everything that is not a companion marker.
	for _, a := range assets {
		if a == nil || a.AssetType != 0 {
			continue
		}
		name := a.AssetName
		if _, ok := isCompanion(name); ok {
			continue
		}
		verdict, seen := s.decided[name]
		if !seen {
			verdict = baseVerdict(name)
			settle(category, name, a.AssetSize, verdict)
		}
		if verdict {
			skippedStems[stem(name)] = true
		}
	}
	// Pass 2: companion markers follow their stem; otherwise the base rule.
	for _, a := range assets {
		if a == nil || a.AssetType != 0 {
			continue
		}
		name := a.AssetName
		st, ok := isCompanion(name)
		if !ok {
			continue
		}
		if _, seen := s.decided[name]; seen {
			continue
		}
		verdict := skippedStems[st] || baseVerdict(name)
		settle(category, name, a.AssetSize, verdict)
	}
}

// absent reports whether <gameDir>/<rel> does not exist (any stat error).
func (s *onDemandSkipper) absent(rel string) bool {
	_, err := os.Stat(filepath.Join(s.gameDir, filepath.FromSlash(rel)))
	return err != nil
}

// IsSkipped reports the Decide verdict for assetName (false when undecided).
func (s *onDemandSkipper) IsSkipped(assetName string) bool {
	if s == nil {
		return false
	}
	return s.decided[assetName]
}

// LogSummary emits one Info line per category plus a total; nothing when no
// asset was skipped. assetBytes is the AssetSize sum of skipped assets
// (deferred content) — NOT the TotalBytes reduction, because patch blobs are
// shared and only fully-released blobs leave the plan.
func (s *onDemandSkipper) LogSummary(logger *slog.Logger, phase string) {
	if s == nil || len(s.skipped) == 0 {
		return
	}
	if logger == nil {
		logger = slog.Default()
	}
	cats := make([]string, 0, len(s.skipped))
	for c := range s.skipped {
		cats = append(cats, c)
	}
	sort.Strings(cats)
	var total onDemandStat
	for _, c := range cats {
		st := s.skipped[c]
		logger.Info("sophon plan: skipped on-demand assets", "phase", phase, "category", c, "n", st.N, "assetBytes", st.Bytes)
		total.N += st.N
		total.Bytes += st.Bytes
	}
	logger.Info("sophon plan: skipped on-demand assets", "phase", phase, "category", "total", "n", total.N, "assetBytes", total.Bytes)
}

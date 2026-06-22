package gachaicon

import (
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"omnigate/internal/core"
)

// indexTTL is how long a network-warmed index is considered fresh; WarmAsync
// skips a refetch while inside this window.
const indexTTL = 7 * 24 * time.Hour

// Manager owns the per-game icon indices and the on-disk icon byte cache.
//
// Concurrency model (must be correct by construction — built with CGO disabled,
// so the race detector is unavailable):
//   - idx/fetchedAt/diskTried are read under mu.RLock and mutated under mu.Lock.
//     A live *Index is NEVER mutated in place; Warm builds a fresh one and swaps
//     the whole pointer, so concurrent readers always see a complete index.
//   - warming (the per-game in-flight warm guard) is owned by warmMu.
//   - imgFlight maps a cache path to a per-path mutex (single-flight icon fetch);
//     the map itself is guarded by imgMu.
type Manager struct {
	cacheDir string
	logger   *slog.Logger
	hc       *http.Client

	mu        sync.RWMutex
	idx       map[core.GameID]*Index
	fetchedAt map[core.GameID]time.Time
	diskTried map[core.GameID]bool // lazy disk-load attempted (per game)

	warmMu  sync.Mutex
	warming map[core.GameID]bool

	imgMu     sync.Mutex
	imgFlight map[string]*sync.Mutex

	onWarm  func(core.GameID)
	urlFn   func(core.GameID, string, string) string
	fetchFn func(*Manager, core.GameID) (*Index, error)
}

// NewManager builds a Manager rooted at dataDir; icon bytes and persisted indices
// live under <dataDir>/.cache/gachaicons. The signature is intentionally 2-arg;
// the warm callback is wired separately via SetOnWarm.
func NewManager(dataDir string, logger *slog.Logger) *Manager {
	if logger == nil {
		logger = slog.Default()
	}
	return &Manager{
		cacheDir:  filepath.Join(dataDir, ".cache", "gachaicons"),
		logger:    logger,
		hc:        &http.Client{Timeout: 30 * time.Second},
		idx:       map[core.GameID]*Index{},
		fetchedAt: map[core.GameID]time.Time{},
		diskTried: map[core.GameID]bool{},
		warming:   map[core.GameID]bool{},
		imgFlight: map[string]*sync.Mutex{},
		urlFn:     iconURL,
		fetchFn:   fetchSources,
	}
}

// SetOnWarm registers a callback invoked once after each successful Warm (used to
// notify the frontend that a game's icons became resolvable). nil is allowed.
//
// MUST be called before the Manager is shared with concurrent goroutines: onWarm
// (like urlFn/fetchFn) is a set-once-before-publication field with no locking, so
// mutating it after other goroutines may read it is not safe.
func (m *Manager) SetOnWarm(fn func(core.GameID)) { m.onWarm = fn }

// snapshot returns the current *Index for gid, lazy-loading the persisted on-disk
// index exactly once if nothing is in memory yet. The fast path takes only RLock.
func (m *Manager) snapshot(gid core.GameID) *Index {
	m.mu.RLock()
	x := m.idx[gid]
	tried := m.diskTried[gid]
	m.mu.RUnlock()
	if x != nil || tried {
		return x
	}
	// Slow path: attempt a one-time disk load (pure disk read, no lock held).
	loaded := m.loadDiskIndex(gid)
	m.mu.Lock()
	defer m.mu.Unlock()
	m.diskTried[gid] = true
	if m.idx[gid] == nil && loaded != nil {
		m.idx[gid] = loaded
	}
	return m.idx[gid]
}

// swap atomically installs a freshly built index for gid.
func (m *Manager) swap(gid core.GameID, idx *Index) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.idx[gid] = idx
}

// Resolve maps a record name (+equip flag) to its Entry via the current index.
func (m *Manager) Resolve(gid core.GameID, name string, equip bool) (Entry, bool) {
	x := m.snapshot(gid)
	if x == nil {
		return Entry{}, false
	}
	return x.Resolve(gid, name, equip)
}

// ServeIcon returns the cached icon bytes for a resolved id, fetching on a miss.
// Disk-cache hit → bytes; miss → single-flight fetch + best-effort cache. The
// write is atomic (temp + rename); a cache-write failure does NOT prevent serving
// the fetched bytes.
func (m *Manager) ServeIcon(gid core.GameID, kind, id string) ([]byte, string, error) {
	x := m.snapshot(gid)
	if x == nil {
		return nil, "", fmt.Errorf("no index for %s", gid)
	}
	e, ok := x.EntryByID(gid, id)
	if !ok {
		return nil, "", fmt.Errorf("unknown id %s/%s", gid, id)
	}
	path := m.cachePath(gid, kind, id)
	if b, err := os.ReadFile(path); err == nil {
		return b, "image/png", nil
	}
	lock := m.pathLock(path)
	lock.Lock()
	defer lock.Unlock()
	// Re-check under the per-path lock: a concurrent fetch may have just filled it.
	if b, err := os.ReadFile(path); err == nil {
		return b, "image/png", nil
	}
	url := m.urlFn(gid, e.Kind, e.IconRef)
	if url == "" {
		return nil, "", fmt.Errorf("no icon url for %s", gid)
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := m.hc.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, "", fmt.Errorf("icon fetch %d", resp.StatusCode)
	}
	b, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", err
	}
	m.writeCacheBestEffort(path, b)
	mime := resp.Header.Get("Content-Type")
	if mime == "" {
		mime = "image/png"
	}
	return b, mime, nil
}

func (m *Manager) cachePath(gid core.GameID, kind, id string) string {
	flat := flatGID(gid)
	return filepath.Join(m.cacheDir, flat, kind+"-"+id+".img")
}

func flatGID(gid core.GameID) string { return strings.ReplaceAll(string(gid), "/", "_") }

func (m *Manager) pathLock(path string) *sync.Mutex {
	m.imgMu.Lock()
	defer m.imgMu.Unlock()
	if l, ok := m.imgFlight[path]; ok {
		return l
	}
	l := &sync.Mutex{}
	m.imgFlight[path] = l
	return l
}

func (m *Manager) writeCacheBestEffort(path string, b []byte) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		m.logger.Debug("gachaicon cache mkdir failed", "err", err)
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		m.logger.Debug("gachaicon cache write failed", "err", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		m.logger.Debug("gachaicon cache rename failed", "err", err)
	}
}

// --- index warm + disk persistence ---

// Warm rebuilds gid's index from its live sources, applies overrides, persists it
// to disk (best-effort), swaps it in, and fires the onWarm callback. Concurrent
// Warms for the same game are coalesced: a second caller returns nil immediately
// while the first is in flight.
func (m *Manager) Warm(gid core.GameID) error {
	if m == nil {
		return nil
	}
	// In-flight guard.
	m.warmMu.Lock()
	if m.warming[gid] {
		m.warmMu.Unlock()
		return nil
	}
	m.warming[gid] = true
	m.warmMu.Unlock()
	defer func() {
		m.warmMu.Lock()
		delete(m.warming, gid)
		m.warmMu.Unlock()
	}()

	idx, err := m.fetchFn(m, gid)
	if err != nil {
		return err
	}
	applyOverrides(idx)
	m.persistIndex(gid, idx) // best-effort; failure must not abort warm
	// Install the index and its freshness stamp together so a reader never sees
	// the new index paired with a zero fetchedAt.
	m.mu.Lock()
	m.idx[gid] = idx
	m.fetchedAt[gid] = time.Now()
	m.mu.Unlock()
	if m.onWarm != nil {
		m.onWarm(gid)
	}
	return nil
}

// WarmAsync warms gid in the background unless a fresh (network-warmed within the
// TTL) index is already present, or a warm is already in flight.
func (m *Manager) WarmAsync(gid core.GameID) {
	if m == nil {
		return
	}
	m.mu.RLock()
	x := m.idx[gid]
	at := m.fetchedAt[gid]
	m.mu.RUnlock()
	if x != nil && !at.IsZero() && time.Since(at) < indexTTL {
		return // fresh
	}
	m.warmMu.Lock()
	inflight := m.warming[gid]
	m.warmMu.Unlock()
	if inflight {
		return
	}
	go func() {
		if err := m.Warm(gid); err != nil {
			m.logger.Debug("gachaicon warm failed", "game", string(gid), "err", err)
		}
	}()
}

// diskIndex is the on-disk JSON form of an *Index. byID is intentionally NOT
// persisted — it is rebuilt deterministically from names/equip on load.
type diskIndex struct {
	Names map[core.GameID]map[string]Entry `json:"names"`
	Equip map[core.GameID]map[string]Entry `json:"equip"`
}

func (m *Manager) indexPath(gid core.GameID) string {
	return filepath.Join(m.cacheDir, "index-"+flatGID(gid)+".json")
}

func (m *Manager) persistIndex(gid core.GameID, x *Index) {
	di := diskIndex{Names: x.names, Equip: x.equip}
	b, err := json.Marshal(di)
	if err != nil {
		m.logger.Debug("gachaicon index marshal failed", "err", err)
		return
	}
	path := m.indexPath(gid)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		m.logger.Debug("gachaicon index mkdir failed", "err", err)
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		m.logger.Debug("gachaicon index write failed", "err", err)
		return
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		m.logger.Debug("gachaicon index rename failed", "err", err)
	}
}

// loadDiskIndex reads a previously persisted index for gid, or returns nil if none
// exists / it is unreadable. byID is rebuilt by replaying put/putEquip.
func (m *Manager) loadDiskIndex(gid core.GameID) *Index {
	b, err := os.ReadFile(m.indexPath(gid))
	if err != nil {
		return nil
	}
	var di diskIndex
	if err := json.Unmarshal(b, &di); err != nil {
		m.logger.Debug("gachaicon index parse failed", "game", string(gid), "err", err)
		return nil
	}
	idx := NewIndex()
	for g, mp := range di.Names {
		for name, e := range mp {
			idx.put(g, name, e)
		}
	}
	for g, mp := range di.Equip {
		for name, e := range mp {
			idx.putEquip(g, name, e)
		}
	}
	return idx
}

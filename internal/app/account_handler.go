package app

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"

	"omnigate/internal/core"
)

// uidCachePathFor puts wuwa_uid_cache.json beside settings (mirrors
// playStatePathFor / gachaDBPathFor).
func uidCachePathFor(settingsPath string) string {
	dir := filepath.Dir(settingsPath)
	if dir == "." || dir == "" {
		return "wuwa_uid_cache.json"
	}
	return filepath.Join(dir, "wuwa_uid_cache.json")
}

// uidCache is the App-owned, non-sensitive cuid→game-UID map (numbers only).
type uidCache struct {
	mu   sync.Mutex
	path string
	m    map[string]string
}

func loadUIDCache(path string) *uidCache {
	c := &uidCache{path: path, m: map[string]string{}}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &c.m)
	}
	return c
}

// Record stores cuid→uid and persists atomically (best-effort).
func (c *uidCache) Record(cuid, uid string) {
	if cuid == "" || uid == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.m[cuid] == uid {
		return
	}
	c.m[cuid] = uid
	b, _ := json.MarshalIndent(c.m, "", "  ")
	tmp := c.path + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		_ = os.Rename(tmp, c.path)
	}
}

// Backfill fills every account's empty UID from the cache; also records any
// account that already carries a UID (the trusted active one).
func (c *uidCache) Backfill(accts []core.GameAccount) {
	for i := range accts {
		if accts[i].UID != "" {
			c.Record(accts[i].ID, accts[i].UID)
			continue
		}
		c.mu.Lock()
		if uid, ok := c.m[accts[i].ID]; ok {
			accts[i].UID = uid
		}
		c.mu.Unlock()
	}
}

// ListGameAccounts returns the switchable accounts for a game, UID-enriched from
// the App-owned cache. Backends without the capability yield
// ErrAccountSwitchUnsupported (frontend hides the chip).
func (a *App) ListGameAccounts(gameID string) ([]core.GameAccount, error) {
	gid := core.GameID(gameID)
	p, err := a.provider(gid)
	if err != nil {
		return nil, err
	}
	sw, ok := p.(core.AccountSwitcher)
	if !ok {
		return nil, core.ErrAccountSwitchUnsupported
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	accts, err := sw.ListAccounts(ctx, gid)
	if err != nil {
		return nil, err
	}
	if a.uidCache != nil {
		a.uidCache.Backfill(accts)
	}
	return accts, nil
}

// SwitchGameAccount switches the active account for a game (game must be closed).
func (a *App) SwitchGameAccount(gameID, accountID string) error {
	gid := core.GameID(gameID)
	p, err := a.provider(gid)
	if err != nil {
		return err
	}
	sw, ok := p.(core.AccountSwitcher)
	if !ok {
		return core.ErrAccountSwitchUnsupported
	}
	ctx := a.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	return sw.SwitchAccount(ctx, gid, accountID)
}

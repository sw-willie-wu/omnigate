package app

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"

	"omnigate/internal/core"
	"omnigate/internal/store"
)

// acctMeta is the per-account cache value: the (numeric) game UID plus an
// optional user-defined label. It unmarshals from either the new object form
// {"uid":…,"label":…} or the legacy bare-string form "<uid>".
type acctMeta struct {
	UID   string `json:"uid"`
	Label string `json:"label,omitempty"`
}

func (m *acctMeta) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' { // legacy string value = bare UID
		var s string
		if err := json.Unmarshal(b, &s); err != nil {
			return err
		}
		m.UID = s
		return nil
	}
	type raw acctMeta // shed UnmarshalJSON to avoid infinite recursion
	var r raw
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	*m = acctMeta(r)
	return nil
}

// uidCache is the App-owned, non-sensitive cuid→{uid,label} map. It never holds
// credentials — only numeric UIDs and user-typed labels.
type uidCache struct {
	mu    sync.Mutex
	store store.StateStore
	m     map[string]acctMeta
}

func loadUIDCache(st store.StateStore) *uidCache {
	c := &uidCache{store: st, m: map[string]acctMeta{}}
	if st == nil {
		return c
	}
	if all, err := st.AllAccountUID(); err == nil {
		for cuid, a := range all {
			c.m[cuid] = acctMeta{UID: a.UID, Label: a.Label}
		}
	}
	return c
}

// persist upserts one cuid's row (best-effort). Caller holds c.mu. No-op when
// the store is nil (degraded mode).
func (c *uidCache) persist(cuid string) {
	if c.store == nil {
		return
	}
	e := c.m[cuid]
	if err := c.store.SetAccountUID(cuid, e.UID, e.Label); err != nil {
		slog.Default().Warn("account_uid persist failed", "cuid", cuid, "err", err)
	}
}

// Record stores cuid→uid, preserving any existing label, and persists atomically.
func (c *uidCache) Record(cuid, uid string) {
	if cuid == "" || uid == "" {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.m[cuid]
	if e.UID == uid {
		return
	}
	e.UID = uid
	c.m[cuid] = e
	c.persist(cuid)
}

// SetLabel sets (or clears) the user label for cuid, preserving the uid. The
// label is trimmed and clamped to 24 runes; an empty/whitespace value clears it.
func (c *uidCache) SetLabel(cuid, label string) {
	if cuid == "" {
		return
	}
	label = strings.TrimSpace(label)
	if r := []rune(label); len(r) > 24 {
		label = string(r[:24])
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	e := c.m[cuid]
	if e.Label == label {
		return
	}
	e.Label = label
	c.m[cuid] = e
	c.persist(cuid)
}

// Backfill fills each account's label from the cache (for every account,
// including the active one) and any empty UID; it also records any account that
// already carries a UID (the trusted active one).
func (c *uidCache) Backfill(accts []core.GameAccount) {
	for i := range accts {
		// One critical section reads the cached label (always) and uid (only
		// when missing). Release before calling Record — sync.Mutex is
		// non-reentrant and Record locks again.
		c.mu.Lock()
		e, ok := c.m[accts[i].ID]
		c.mu.Unlock()
		if ok {
			accts[i].Label = e.Label
			if accts[i].UID == "" {
				accts[i].UID = e.UID
			}
		}
		if accts[i].UID != "" {
			c.Record(accts[i].ID, accts[i].UID)
		}
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

// SetAccountLabel sets the user-defined display label for one account (cuid).
// Capability-gated (requires core.AccountSwitcher) so it rejects games that
// can't switch accounts. The label lives in the App-owned uid cache; no provider
// call, no credentials.
func (a *App) SetAccountLabel(gameID, accountID, label string) error {
	gid := core.GameID(gameID)
	p, err := a.provider(gid)
	if err != nil {
		return err
	}
	if _, ok := p.(core.AccountSwitcher); !ok {
		return core.ErrAccountSwitchUnsupported
	}
	if a.uidCache != nil {
		a.uidCache.SetLabel(accountID, label)
	}
	return nil
}

package kurogames

import (
	"encoding/json"
	"sync"

	"omnigate/internal/core"
)

// KV is the narrow config store the provider needs. Method names match
// store.StateStore so the App can inject its store directly (spec §4.1).
type KV interface {
	GetConfig(key string) (string, bool, error)
	SetConfig(key, value string) error
}

type memKV struct {
	mu sync.Mutex
	m  map[string]string
}

func newMemKV() *memKV { return &memKV{m: map[string]string{}} }

func (k *memKV) GetConfig(key string) (string, bool, error) {
	k.mu.Lock()
	defer k.mu.Unlock()
	v, ok := k.m[key]
	return v, ok, nil
}

func (k *memKV) SetConfig(key, value string) error {
	k.mu.Lock()
	defer k.mu.Unlock()
	k.m[key] = value
	return nil
}

func kvKey(gid core.GameID, suffix string) string { return "games." + string(gid) + "." + suffix }

func (p *Provider) SetKV(kv KV) {
	if kv != nil {
		p.kv = kv
	}
}

func (p *Provider) HasExternalKV() bool { _, mem := p.kv.(*memKV); return !mem }

func (p *Provider) loadCatalog(gid core.GameID) (core.BundleCatalog, bool) {
	var c core.BundleCatalog
	v, ok, err := p.kv.GetConfig(kvKey(gid, "bundle_catalog"))
	if err != nil || !ok || v == "" {
		return c, false
	}
	if json.Unmarshal([]byte(v), &c) != nil {
		p.logger.Warn("kurogames: bundle catalog unreadable; treating as missing", "game", gid)
		return core.BundleCatalog{}, false
	}
	return c, true
}

func (p *Provider) saveCatalog(gid core.GameID, c core.BundleCatalog) {
	b, _ := json.Marshal(c)
	if err := p.kv.SetConfig(kvKey(gid, "bundle_catalog"), string(b)); err != nil {
		p.logger.Warn("kurogames: save bundle catalog failed", "game", gid, "err", err)
	}
}

func (p *Provider) loadLaunchOpts(gid core.GameID) map[string]bool {
	out := map[string]bool{}
	v, ok, err := p.kv.GetConfig(kvKey(gid, "launch_opts"))
	if err == nil && ok && v != "" {
		_ = json.Unmarshal([]byte(v), &out)
	}
	return out
}

func (p *Provider) saveLaunchOpts(gid core.GameID, m map[string]bool) error {
	b, _ := json.Marshal(m)
	return p.kv.SetConfig(kvKey(gid, "launch_opts"), string(b))
}

func (p *Provider) activeBundleConfig(gid core.GameID) string {
	v, _, _ := p.kv.GetConfig(kvKey(gid, "active_bundle"))
	return v
}

func (p *Provider) setActiveBundleConfig(gid core.GameID, name string) error {
	return p.kv.SetConfig(kvKey(gid, "active_bundle"), name)
}

package hoyoverse

import (
	"sync"

	"omnigate/internal/core"
)

type planFlavor int

const (
	flavorNone      planFlavor = iota
	flavorPatch
	flavorFull
	flavorAudioOnly
	flavorPredlPatch
	flavorPredlFull
)

func (f planFlavor) String() string {
	switch f {
	case flavorNone:
		return "none"
	case flavorPatch:
		return "patch"
	case flavorFull:
		return "full"
	case flavorAudioOnly:
		return "audio_only"
	case flavorPredlPatch:
		return "predl_patch"
	case flavorPredlFull:
		return "predl_full"
	}
	return "unknown"
}

type genshinPlan struct {
	core.UpdatePlan
	flavor          planFlavor
	sourceVersion   string
	manifestETag    string
	audioLanguages  []string
	predlAvailable  bool
}

type manifestCache struct {
	mu    sync.Mutex
	plans map[core.GameID]*genshinPlan
}

func newManifestCache() *manifestCache {
	return &manifestCache{plans: make(map[core.GameID]*genshinPlan)}
}

func (c *manifestCache) put(gid core.GameID, gp *genshinPlan) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.plans[gid] = gp
}

func (c *manifestCache) get(gid core.GameID) *genshinPlan {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.plans[gid]
}

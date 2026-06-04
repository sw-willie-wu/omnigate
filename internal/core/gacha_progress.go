package core

import "context"

// GachaProgress is one progress tick emitted by a provider while paginating the
// record API during a refresh: which pool (banner key + 1-based position) and
// which 1-based page is about to be fetched. Carries no token/URL.
type GachaProgress struct {
	BannerKey string `json:"bannerKey"`
	Page      int    `json:"page"`
	PoolIndex int    `json:"poolIndex"`
	PoolTotal int    `json:"poolTotal"`
}

type gachaProgressKey struct{}

// WithGachaProgress attaches a progress reporter to ctx. Providers call
// ReportGachaProgress; if no reporter is attached the call is a no-op, so
// FetchGacha callers that don't care (incl. every existing test) are unaffected.
func WithGachaProgress(ctx context.Context, fn func(GachaProgress)) context.Context {
	return context.WithValue(ctx, gachaProgressKey{}, fn)
}

// ReportGachaProgress invokes the reporter attached via WithGachaProgress, if any.
func ReportGachaProgress(ctx context.Context, p GachaProgress) {
	if fn, ok := ctx.Value(gachaProgressKey{}).(func(GachaProgress)); ok && fn != nil {
		fn(p)
	}
}

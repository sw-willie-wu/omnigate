package core

import (
	"context"
	"testing"
)

func TestGachaProgressReporterRoundTrip(t *testing.T) {
	var got []GachaProgress
	ctx := WithGachaProgress(context.Background(), func(p GachaProgress) { got = append(got, p) })
	ReportGachaProgress(ctx, GachaProgress{BannerKey: "character", Page: 1, PoolIndex: 1, PoolTotal: 4})
	ReportGachaProgress(ctx, GachaProgress{BannerKey: "weapon", Page: 2, PoolIndex: 2, PoolTotal: 4})
	if len(got) != 2 || got[0].BannerKey != "character" || got[1].Page != 2 {
		t.Fatalf("reporter got %+v", got)
	}
}

func TestGachaProgressNoReporterNoPanic(t *testing.T) {
	// No reporter attached → must be a silent no-op.
	ReportGachaProgress(context.Background(), GachaProgress{BannerKey: "x", Page: 1})
}

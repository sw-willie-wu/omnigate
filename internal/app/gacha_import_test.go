package app

import (
	"strings"
	"testing"

	"omnigate/internal/core"
)

// fakeImportProvider returns canned pulls; captures the existing-ids lookup so
// tests can assert the store wiring.
type fakeImportProvider struct {
	fakeProvider
	res      core.GachaFetchResult
	err      error
	gotKnown map[string]bool
}

func (f *fakeImportProvider) ParseGachaImport(_ core.GameID, _ []byte, existing func(uid string) map[string]bool) (core.GachaFetchResult, error) {
	if existing != nil {
		f.gotKnown = existing(f.res.UID)
	}
	return f.res, f.err
}

func newTestAppWithImport(t *testing.T, fp *fakeImportProvider) *App {
	t.Helper()
	gid := core.GameID("kurogames/wutheringwaves")
	fp.fakeProvider = fakeProvider{id: "kurogames", games: []core.GameDescriptor{{ID: gid, Backend: "kurogames"}}}
	a := newTestAppWithGacha(t, nil) // store + base app; provider replaced below
	a.providers = []core.Provider{fp}
	return a
}

func TestImportGachaData_UpsertsAndDedups(t *testing.T) {
	fp := &fakeImportProvider{res: core.GachaFetchResult{UID: "700", Pulls: []core.GachaPull{
		{ID: "w|1|2026-06-01 10:00:00|0", BannerKey: "character", Rank: 5, Name: "Alpha", Time: "2026-06-01 10:00:00"},
		{ID: "w|1|2026-06-01 10:00:01|0", BannerKey: "character", Rank: 3, Name: "Beta", Time: "2026-06-01 10:00:01"},
	}}}
	a := newTestAppWithImport(t, fp)
	gid := core.GameID("kurogames/wutheringwaves")

	out, err := a.importGachaData(gid, fp, []byte(`{}`))
	if err != nil || out.UID != "700" || out.Added != 2 || out.Total != 2 {
		t.Fatalf("first import out=%+v err=%v", out, err)
	}
	if len(fp.gotKnown) != 0 {
		t.Fatalf("first import known=%v want empty", fp.gotKnown)
	}
	// second import of the same data: nothing new, and the provider now sees
	// the stored ids through the lookup.
	out, err = a.importGachaData(gid, fp, []byte(`{}`))
	if err != nil || out.Added != 0 || out.Total != 2 {
		t.Fatalf("second import out=%+v err=%v", out, err)
	}
	if !fp.gotKnown["w|1|2026-06-01 10:00:00|0"] || !fp.gotKnown["w|1|2026-06-01 10:00:01|0"] {
		t.Fatalf("second import known=%v", fp.gotKnown)
	}
	all, err := a.gachaStore.AllPulls("kurogames/wutheringwaves", "700")
	if err != nil || len(all) != 2 {
		t.Fatalf("stored=%d err=%v", len(all), err)
	}
}

func TestImportGachaRecords_UnsupportedProvider(t *testing.T) {
	// endfield test app's provider lacks the import capability
	a := newTestAppWithGacha(t, &fakeGachaProvider{})
	if _, err := a.ImportGachaRecords("hypergryph/endfield"); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("err=%v want unsupported", err)
	}
}

func TestImportGachaData_ParseErrorPropagates(t *testing.T) {
	fp := &fakeImportProvider{err: core.ErrUnknownGame}
	a := newTestAppWithImport(t, fp)
	if _, err := a.importGachaData(core.GameID("kurogames/wutheringwaves"), fp, []byte(`{`)); err == nil {
		t.Fatal("want error")
	}
}

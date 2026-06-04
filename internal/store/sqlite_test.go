package store

import (
	"path/filepath"
	"testing"

	"omnigate/internal/core"
)

func openTemp(t *testing.T) *SQLiteStore {
	t.Helper()
	db, err := OpenSQLite(filepath.Join(t.TempDir(), "gacha.db"))
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestUpsertDedupAndAll(t *testing.T) {
	s := openTemp(t)
	pulls := []core.GachaPull{
		{ID: "100", BannerKey: "special", Rank: 6, Name: "A", Time: "t1"},
		{ID: "101", BannerKey: "special", Rank: 5, Name: "B", Time: "t2"},
	}
	added, err := s.UpsertPulls("hypergryph/endfield", "u1", pulls)
	if err != nil || added != 2 {
		t.Fatalf("first upsert added=%d err=%v want 2", added, err)
	}
	added2, err := s.UpsertPulls("hypergryph/endfield", "u1", append(pulls,
		core.GachaPull{ID: "102", BannerKey: "special", Rank: 5, Name: "C", Time: "t3"}))
	if err != nil || added2 != 1 {
		t.Fatalf("second upsert added=%d err=%v want 1", added2, err)
	}
	all, err := s.AllPulls("hypergryph/endfield", "u1")
	if err != nil || len(all) != 3 {
		t.Fatalf("AllPulls len=%d err=%v want 3", len(all), err)
	}
}

func TestUIDIsolationAndLatest(t *testing.T) {
	s := openTemp(t)
	s.UpsertPulls("hypergryph/endfield", "u1", []core.GachaPull{{ID: "1", Rank: 6}})
	s.UpsertPulls("hypergryph/endfield", "u2", []core.GachaPull{{ID: "1", Rank: 5}})
	if got, _ := s.AllPulls("hypergryph/endfield", "u2"); len(got) != 1 || got[0].Rank != 5 {
		t.Fatalf("uid isolation broken: %+v", got)
	}
	uids, _ := s.KnownUIDs("hypergryph/endfield")
	if len(uids) != 2 {
		t.Fatalf("KnownUIDs=%v want 2", uids)
	}
	if latest, _ := s.LatestUID("hypergryph/endfield"); latest != "u2" {
		t.Fatalf("LatestUID=%q want u2", latest)
	}
}

func TestURLCacheRoundTrip(t *testing.T) {
	s := openTemp(t)
	if err := s.PutURLCache("hypergryph/endfield", "u1", "https://x/page/gacha_y?token=z"); err != nil {
		t.Fatalf("PutURLCache: %v", err)
	}
	url, _, err := s.GetURLCache("hypergryph/endfield", "u1")
	if err != nil || url == "" {
		t.Fatalf("GetURLCache url=%q err=%v", url, err)
	}
}

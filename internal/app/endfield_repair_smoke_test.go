package app

// Live-API smoke for the schema-v7 Endfield repair (spec acceptance 3).
// Skipped unless explicitly enabled: it copies the user's real omnigate.db,
// opens the COPY (migration runs there), then drives the real RefreshGacha
// chain per account and asserts the weapon records dropped by the old PK
// come back. The original DB file is never touched. Run manually:
//
//	$env:OMNIGATE_E2E_ENDFIELD="1"
//	$env:OMNIGATE_E2E_DB="C:\Users\willie\Repos\omnigate\dist\omnigate.db"
//	$env:CGO_ENABLED="0"; go test ./internal/app/ -run TestEndfieldRepairSmoke -v -timeout 30m

import (
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"omnigate/internal/core"
	"omnigate/internal/providers/hypergryph"
	"omnigate/internal/store"
)

// bannerStat aggregates one (uid, banner) partition: row count and the max
// numeric id (0 when no id parses as an integer — e.g. HoYo/WuWa ids).
type bannerStat struct {
	n     int
	maxID int64
}

func countByBanner(t *testing.T, st *store.SQLiteStore, uid string) map[string]bannerStat {
	t.Helper()
	all, err := st.AllPulls("hypergryph/endfield", uid)
	if err != nil {
		t.Fatalf("AllPulls(%s): %v", uid, err)
	}
	out := map[string]bannerStat{}
	for _, p := range all {
		s := out[p.BannerKey]
		s.n++
		if v, err := strconv.ParseInt(p.ID, 10, 64); err == nil && v > s.maxID {
			s.maxID = v
		}
		out[p.BannerKey] = s
	}
	return out
}

func assertPoolPresent(t *testing.T, st *store.SQLiteStore, uid, poolID string) {
	t.Helper()
	all, err := st.AllPulls("hypergryph/endfield", uid)
	if err != nil {
		t.Fatalf("AllPulls(%s): %v", uid, err)
	}
	for _, p := range all {
		if p.PoolID == poolID {
			return
		}
	}
	t.Errorf("pool %s not present for uid %s", poolID, uid)
}

// newSmokeApp wires the minimal App fields RefreshGacha needs, with the REAL
// hypergryph provider. a.ctx stays nil — with a non-nil ctx a.emit calls the
// Wails runtime which log.Fatalf's outside a real app (see newTestAppWithEndfield).
func newSmokeApp(t *testing.T, st *store.SQLiteStore) *App {
	t.Helper()
	a := &App{settings: Settings{Version: 2}, logger: slog.Default()}
	a.gachaStore = st
	a.store = st
	a.providers = []core.Provider{hypergryph.New(hypergryph.Settings{}, slog.Default())}
	return a
}

func TestEndfieldRepairSmoke(t *testing.T) {
	if os.Getenv("OMNIGATE_E2E_ENDFIELD") != "1" {
		t.Skip("set OMNIGATE_E2E_ENDFIELD=1 to run")
	}
	src := os.Getenv("OMNIGATE_E2E_DB")
	if src == "" {
		t.Fatal("OMNIGATE_E2E_DB not set")
	}
	fi, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "smoke.db")
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dbPath, b, 0o600); err != nil {
		t.Fatal(err)
	}

	// Opening the copy runs the v6→v7 migration.
	st, err := store.OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if v, _, _ := st.GetMeta("schema_version"); v != "7" {
		t.Fatalf("copy schema_version = %s, want 7", v)
	}
	if _, err := os.Stat(dbPath + ".bak-v7"); err != nil {
		t.Fatalf("bak-v7 missing on copy: %v", err)
	}

	a := newSmokeApp(t, st)
	uip, _ := a.providers[0].(core.GachaUserInfoProvider)

	accts, err := st.ListGachaAccounts("hypergryph/endfield")
	if err != nil {
		t.Fatal(err)
	}
	if len(accts) == 0 {
		t.Fatal("no endfield accounts in DB copy")
	}
	for _, acc := range accts {
		t.Logf("=== account %s uid=%s label=%s", acc.ID, acc.UID, acc.Label)
		// Pre-flight: an expired token must read as SKIP, not a repair failure.
		if uip != nil {
			if _, err := uip.FetchUserInfo(t.Context(), acc.Token); err != nil {
				if errors.Is(err, core.ErrGachaCredentialExpired) {
					t.Logf("SKIP %s: token expired — re-login in omnigate then re-run", acc.UID)
					continue
				}
				t.Logf("pre-flight warning for %s: %v (continuing)", acc.UID, err)
			}
		}
		before := countByBanner(t, st, acc.UID)
		if _, err := a.RefreshGacha("hypergryph/endfield", acc.ID); err != nil {
			t.Errorf("refresh %s: %v", acc.UID, err)
			continue
		}
		after := countByBanner(t, st, acc.UID)
		// char banners must never shrink
		for _, bk := range []string{"special", "standard", "beginner", "joint"} {
			if after[bk].n < before[bk].n {
				t.Errorf("%s %s shrank %d→%d", acc.UID, bk, before[bk].n, after[bk].n)
			}
		}
		// weapon: hard "no shrink" for all; hard growth only for the primary
		// account whose server window (seqId 291..400) was measured — the other
		// accounts' windows are unmeasured and the server prunes old records.
		if after["weapon"].n < before["weapon"].n {
			t.Errorf("%s weapon shrank %d→%d", acc.UID, before["weapon"].n, after["weapon"].n)
		}
		t.Logf("%s weapon %d→%d (maxID %d→%d)", acc.UID,
			before["weapon"].n, after["weapon"].n, before["weapon"].maxID, after["weapon"].maxID)
		if acc.UID == "4191138757" {
			if after["weapon"].n <= before["weapon"].n {
				t.Errorf("primary weapon did not grow: %d→%d", before["weapon"].n, after["weapon"].n)
			}
			if after["weapon"].maxID <= 300 {
				t.Errorf("weapon max id still %d, want >300", after["weapon"].maxID)
			}
			assertPoolPresent(t, st, acc.UID, "weponbox_1_4_2") // 明曜申領
		}
		if _, ok, _ := st.GetMeta("v7_refetch_pending:hypergryph/endfield:" + acc.UID); ok {
			t.Errorf("%s repair key not cleared", acc.UID)
		}
		// Second refresh: counts must be stable. (May legitimately still be a
		// full refetch — PerPool weapon rows whose server-pruned poolId is empty
		// trip needsBackfill — that is expected, not a regression.)
		if _, err := a.RefreshGacha("hypergryph/endfield", acc.ID); err != nil {
			t.Errorf("second refresh %s: %v", acc.UID, err)
			continue
		}
		again := countByBanner(t, st, acc.UID)
		if again["weapon"].n != after["weapon"].n || again["special"].n != after["special"].n {
			t.Errorf("%s second refresh changed counts: weapon %d→%d special %d→%d", acc.UID,
				after["weapon"].n, again["weapon"].n, after["special"].n, again["special"].n)
		}
	}

	// The original DB must be untouched.
	fi2, err := os.Stat(src)
	if err != nil || !fi2.ModTime().Equal(fi.ModTime()) {
		t.Fatalf("ORIGINAL DB TOUCHED: err=%v mtime %v vs %v", err, fi2.ModTime(), fi.ModTime())
	}
}

package hypergryph

// Live smoke for the HTTP-401 → ErrGachaCredentialExpired mapping, using a
// genuinely expired token from the user's real DB (the Endfield 1.5.3 update
// invalidated older tokens). Run manually:
//
//	$env:OMNIGATE_E2E_EF401="1"
//	$env:OMNIGATE_E2E_DB="C:\Users\willie\Repos\omnigate\dist\omnigate.db"
//	$env:CGO_ENABLED="0"; go test ./internal/providers/hypergryph/ -run TestEndfieldExpired401Smoke -v

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"omnigate/internal/core"
	"omnigate/internal/store"
)

func TestEndfieldExpired401Smoke(t *testing.T) {
	if os.Getenv("OMNIGATE_E2E_EF401") != "1" {
		t.Skip("set OMNIGATE_E2E_EF401=1 to run (needs network + real DB)")
	}
	src := os.Getenv("OMNIGATE_E2E_DB")
	if src == "" {
		t.Fatal("OMNIGATE_E2E_DB not set")
	}
	// copy — never open the live DB (OpenSQLite migrates on open)
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(t.TempDir(), "ef401.db")
	if err := os.WriteFile(dbPath, b, 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := store.OpenSQLite(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()

	accts, err := st.ListGachaAccounts("hypergryph/endfield")
	if err != nil {
		t.Fatal(err)
	}
	p := New(Settings{}, slog.Default())
	p.pageDelay = 0
	tried := 0
	for _, acc := range accts {
		res, err := p.FetchGachaWithCredential(context.Background(), "hypergryph/endfield", acc.Token, "en-us", nil)
		if err == nil {
			t.Logf("%s: token still VALID (fetched %d pulls) — not usable for the 401 case", acc.UID, len(res.Pulls))
			continue
		}
		tried++
		if !errors.Is(err, core.ErrGachaCredentialExpired) {
			t.Errorf("%s: err = %v; want ErrGachaCredentialExpired (the pre-fix bug: generic status-401 error → code=internal)", acc.UID, err)
		} else {
			t.Logf("%s: expired token correctly mapped to ErrGachaCredentialExpired", acc.UID)
		}
	}
	if tried == 0 {
		t.Skip("no expired-token account available — cannot exercise the live 401 path")
	}
}

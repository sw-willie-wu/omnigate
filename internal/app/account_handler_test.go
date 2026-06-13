package app

import (
	"path/filepath"
	"testing"

	"omnigate/internal/core"
)

func TestUIDCache_RecordBackfill(t *testing.T) {
	dir := t.TempDir()
	c := loadUIDCache(filepath.Join(dir, "wuwa_uid_cache.json"))
	c.Record("535788351", "700001181")

	accts := []core.GameAccount{
		{ID: "537195734", Active: false},
		{ID: "535788351", UID: "700001181", Active: true},
	}
	c.Backfill(accts)
	if accts[1].UID != "700001181" {
		t.Errorf("active UID should remain: %+v", accts[1])
	}
	c2 := loadUIDCache(filepath.Join(dir, "wuwa_uid_cache.json"))
	again := []core.GameAccount{{ID: "535788351"}}
	c2.Backfill(again)
	if again[0].UID != "700001181" {
		t.Errorf("persisted cache should backfill UID, got %+v", again[0])
	}
}

// noSwitchProvider satisfies core.Provider (embed the interface; unused methods are
// never called) but does NOT implement core.AccountSwitcher.
type noSwitchProvider struct{ core.Provider }

func (noSwitchProvider) ID() core.BackendID { return "fake" }
func (noSwitchProvider) Games() []core.GameDescriptor {
	return []core.GameDescriptor{{ID: "fake/g", Backend: "fake"}}
}

func TestListGameAccounts_Unsupported(t *testing.T) {
	a := &App{}
	if err := a.registerProvider(noSwitchProvider{}); err != nil {
		t.Fatalf("registerProvider: %v", err)
	}
	_, err := a.ListGameAccounts("fake/g")
	if err != core.ErrAccountSwitchUnsupported {
		t.Fatalf("want ErrAccountSwitchUnsupported, got %v", err)
	}
}

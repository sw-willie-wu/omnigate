package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

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

// Test 5: SetAccountLabel is capability-gated like SwitchGameAccount.
func TestSetAccountLabel_Unsupported(t *testing.T) {
	a := &App{}
	if err := a.registerProvider(noSwitchProvider{}); err != nil {
		t.Fatalf("registerProvider: %v", err)
	}
	if err := a.SetAccountLabel("fake/g", "id", "x"); err != core.ErrAccountSwitchUnsupported {
		t.Fatalf("want ErrAccountSwitchUnsupported, got %v", err)
	}
}

// Test 1: legacy {cuid:"uid"} string-form caches must still load and backfill.
func TestUIDCache_LoadsLegacyStringFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wuwa_uid_cache.json")
	if err := os.WriteFile(path, []byte(`{"537195734":"700727240"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	c := loadUIDCache(path)
	accts := []core.GameAccount{{ID: "537195734"}}
	c.Backfill(accts)
	if accts[0].UID != "700727240" {
		t.Errorf("legacy string cache should backfill UID, got %+v", accts[0])
	}
}

// Test 2: a label set then reloaded must backfill onto the ACTIVE account (the
// one with a non-empty UID) — pins the B2 continue-branch fix.
func TestUIDCache_SetLabelPersistsAndBackfillsActive(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wuwa_uid_cache.json")
	c := loadUIDCache(path)
	c.SetLabel("537195734", "主帳")

	c2 := loadUIDCache(path)
	accts := []core.GameAccount{{ID: "537195734", UID: "700727240", Active: true}}
	c2.Backfill(accts)
	if accts[0].Label != "主帳" {
		t.Errorf("active account should get its label, got %+v", accts[0])
	}
	if accts[0].UID != "700727240" {
		t.Errorf("active UID must remain, got %+v", accts[0])
	}
}

// Test 3: Record preserves an existing label; SetLabel preserves the existing uid.
func TestUIDCache_RecordPreservesLabel_SetLabelPreservesUID(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wuwa_uid_cache.json")
	c := loadUIDCache(path)
	c.Record("c1", "u1")
	c.SetLabel("c1", "name")
	c.Record("c1", "u1") // re-record same uid must not drop the label

	accts := []core.GameAccount{{ID: "c1"}}
	c.Backfill(accts)
	if accts[0].UID != "u1" || accts[0].Label != "name" {
		t.Errorf("Record must preserve label, got %+v", accts[0])
	}

	c.SetLabel("c1", "name2")
	accts2 := []core.GameAccount{{ID: "c1"}}
	c.Backfill(accts2)
	if accts2[0].UID != "u1" {
		t.Errorf("SetLabel must preserve uid, got %+v", accts2[0])
	}
}

// Test 4: SetLabel trims, clamps to 24 runes, and an all-whitespace value clears
// the label while keeping the uid.
func TestUIDCache_SetLabelTrimClampClear(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "wuwa_uid_cache.json")
	c := loadUIDCache(path)
	c.Record("c1", "u1")

	c.SetLabel("c1", "  hi  ")
	accts := []core.GameAccount{{ID: "c1"}}
	c.Backfill(accts)
	if accts[0].Label != "hi" {
		t.Errorf("label should be trimmed, got %q", accts[0].Label)
	}

	c.SetLabel("c1", strings.Repeat("好", 30))
	accts = []core.GameAccount{{ID: "c1"}}
	c.Backfill(accts)
	if n := utf8.RuneCountInString(accts[0].Label); n != 24 {
		t.Errorf("label should clamp to 24 runes, got %d", n)
	}

	c.SetLabel("c1", "   ")
	accts = []core.GameAccount{{ID: "c1"}}
	c.Backfill(accts)
	if accts[0].Label != "" {
		t.Errorf("whitespace label should clear, got %q", accts[0].Label)
	}
	if accts[0].UID != "u1" {
		t.Errorf("clearing label must keep uid, got %+v", accts[0])
	}
}

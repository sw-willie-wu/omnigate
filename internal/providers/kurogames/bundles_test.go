package kurogames

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"omnigate/internal/core"
)

func TestBundleState_OfflineFromRecordAndCatalog(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, installStateFile), []byte(twoBundles), 0o644)
	p := newV3TestProvider(t, dir)
	_ = p.setActiveBundleConfig(gidWuwa, "SD")
	cat, st, found, err := p.BundleState(context.Background(), gidWuwa)
	if err != nil || found || st.Active != "SD" || st.Installed["HD"] != "3.7.0" || len(cat.Bundles) != 0 {
		t.Fatalf("cat=%+v st=%+v found=%v err=%v", cat, st, found, err)
	}
	p.saveCatalog(gidWuwa, catalogFromIndex(loadV3Index(t), time.Now()))
	if _, _, found, _ := p.BundleState(context.Background(), gidWuwa); !found {
		t.Fatal("catalog not found after save")
	}
}

func TestSetActiveBundle_RequiresInstalled(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, installStateFile), []byte(twoBundles), 0o644)
	p := newV3TestProvider(t, dir)
	if err := p.SetActiveBundle(context.Background(), gidWuwa, "UHD"); !isCode(err, "bundle_not_installed") {
		t.Fatalf("err=%v", err)
	}
	if err := p.SetActiveBundle(context.Background(), gidWuwa, "4K"); !isCode(err, "bundle_unknown") {
		t.Fatalf("err=%v", err)
	}
	if err := p.SetActiveBundle(context.Background(), gidWuwa, "SD"); err != nil || p.activeBundleConfig(gidWuwa) != "SD" {
		t.Fatalf("err=%v active=%q", err, p.activeBundleConfig(gidWuwa))
	}
}

func TestLaunchOptions_RoundTrip(t *testing.T) {
	p := newV3TestProvider(t, t.TempDir())
	_ = p.SetLaunchOption(context.Background(), gidWuwa, "-slno", true)
	m, _ := p.LaunchOptions(context.Background(), gidWuwa)
	if !m["-slno"] {
		t.Fatalf("m=%v", m)
	}
}

func TestBuildBundleInstallPlan(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, installStateFile), []byte(`{"version":"3.7.0","bundles":{"HD":{"version":"3.7.0","state":"","resourcePacks":["common","hd"]}}}`), 0o644)
	srv := newV3Server(t, map[string][]byte{"sd": readFixture(t, "sd_indexFile.json")}, nil)
	p := newV3TestProvider(t, dir)
	plan, err := p.BuildBundleInstallPlan(context.Background(), gidWuwa, "SD", nil)
	if err != nil || plan.Bundle != "SD" || len(plan.Files) != 100 || plan.Version != "3.7.0" {
		t.Fatalf("plan bundle=%q files=%d err=%v", plan.Bundle, len(plan.Files), err)
	}
	for _, f := range plan.Files {
		if !strings.HasPrefix(f.Path, "Client/Content/SD/") {
			t.Fatalf("stray file %s", f.Path)
		}
	}
	if plan.ManifestETag != planToken(srv.idx, []string{"sd"}) {
		t.Fatalf("token=%q want planToken(idx,[sd])", plan.ManifestETag)
	}
}

func TestBuildBundleInstallPlan_UpdateFirst(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, installStateFile), []byte(`{"version":"3.6.1","state":""}`), 0o644)
	newV3Server(t, nil, nil)
	_, err := newV3TestProvider(t, dir).BuildBundleInstallPlan(context.Background(), gidWuwa, "SD", nil)
	if !isCode(err, "bundle_update_first") {
		t.Fatalf("err=%v", err)
	}
}

func TestBuildBundleInstallPlan_NoRecord(t *testing.T) {
	newV3Server(t, nil, nil)
	_, err := newV3TestProvider(t, t.TempDir()).BuildBundleInstallPlan(context.Background(), gidWuwa, "SD", nil)
	if !isCode(err, "install_record_missing") {
		t.Fatalf("err=%v", err)
	}
}

func TestRemoveBundle(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, installStateFile), []byte(twoBundles), 0o644)
	_ = os.MkdirAll(filepath.Join(dir, "Client", "Content", "SD"), 0o755)
	p := newV3TestProvider(t, dir)
	if err := p.RemoveBundle(context.Background(), gidWuwa, "SD"); err != nil {
		t.Fatal(err)
	}
	s, _ := readInstallState(filepath.Join(dir, installStateFile))
	if _, ok := s.Bundles["SD"]; ok {
		t.Fatal("SD still registered")
	}
	if _, err := os.Stat(filepath.Join(dir, "Client", "Content", "SD")); !os.IsNotExist(err) {
		t.Fatal("SD dir still exists")
	}
}

func TestRemoveBundle_FailureDoesNotWriteBack(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, installStateFile), []byte(twoBundles), 0o644)
	p := newV3TestProvider(t, dir)
	p.removeAll = func(string) error { return errors.New("locked") }
	if err := p.RemoveBundle(context.Background(), gidWuwa, "SD"); !isCode(err, "bundle_remove_failed") {
		t.Fatalf("err=%v", err)
	}
	s, _ := readInstallState(filepath.Join(dir, installStateFile))
	if _, ok := s.Bundles["SD"]; !ok {
		t.Fatal("SD must remain registered on failure")
	}
}

func isCode(err error, code string) bool {
	var ue *core.UpdateError
	return errors.As(err, &ue) && ue.Code == code
}

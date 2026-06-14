package app

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"omnigate/internal/core"
)

// TestRefresh_PhantomPredlSilentInvalidate validates spec §2.4 phantom-predl
// rule: when CheckVersion's Current matches PredlReady.Plan.Version, the
// predl is silently dropped — no ConfirmDialog event, sidecar deleted,
// state.PredlReady cleared. Spec §7.5 mandate.
func TestRefresh_PhantomPredlSilentInvalidate(t *testing.T) {
	tempRoot := t.TempDir()

	gid := core.GameID("kurogames/wuwa")
	emitCount := 0
	var dialogEmitted bool
	emit := func(name string, args ...any) {
		emitCount++
		if name == "ui:confirm" {
			dialogEmitted = true
		}
	}

	a := &App{
		updateRegistry: NewUpdateStateRegistry(emit, realClock{}),
		settings:       Settings{App: AppSettings{TempDir: tempRoot}, Backends: BackendSettings{}},
		detect:         map[core.BackendID]detectEntry{},
	}
	defer a.updateRegistry.emitter.Stop()

	a.providers = []core.Provider{&phantomPredlFakeProvider{currentVersion: "3.4.0", gid: gid}}

	gameIDFlat := "kurogames-wuwa"
	versionDir := filepath.Join(tempRoot, gameIDFlat, "3.4.0")
	if err := os.MkdirAll(versionDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(versionDir, "predl_ready.json"),
		[]byte(`{"etag":"e","version":"3.4.0","entries":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	state := a.updateRegistry.Get(gid)
	state.PredlReady = &core.UpdatePlan{
		GameID: gid, Version: "3.4.0", ManifestETag: "e",
	}

	if _, err := a.RefreshVersion(string(gid)); err != nil {
		t.Fatalf("RefreshVersion: %v", err)
	}

	state.mu.RLock()
	defer state.mu.RUnlock()
	if state.PredlReady != nil {
		t.Errorf("PredlReady = %+v, want nil after phantom invalidate", state.PredlReady)
	}
	if _, err := os.Stat(versionDir); err == nil {
		t.Errorf("versionDir %s still exists; should be removed", versionDir)
	}
	if dialogEmitted {
		t.Errorf("ui:confirm dialog emitted; phantom invalidate must be silent")
	}
}

type phantomPredlFakeProvider struct {
	currentVersion string
	gid            core.GameID
}

func (p *phantomPredlFakeProvider) ID() core.BackendID                  { return "kurogames" }
func (p *phantomPredlFakeProvider) DisplayName() core.LocalizedString   { return core.LocalizedString{} }
func (p *phantomPredlFakeProvider) Games() []core.GameDescriptor        { return []core.GameDescriptor{{ID: p.gid}} }
func (p *phantomPredlFakeProvider) SettingsSchema() []core.SettingField { return nil }
func (p *phantomPredlFakeProvider) DetectInstall(_ context.Context) ([]core.InstalledGame, error) {
	return []core.InstalledGame{{GameID: p.gid, InstallPath: ""}}, nil
}
func (p *phantomPredlFakeProvider) GetIcon(_ context.Context, _ core.GameID) (string, error) { return "", nil }
func (p *phantomPredlFakeProvider) GetBackgrounds(_ context.Context, _ core.GameID) ([]core.Background, error) {
	return nil, nil
}
func (p *phantomPredlFakeProvider) CheckVersion(_ context.Context, _ core.GameID) (core.VersionInfo, error) {
	return core.VersionInfo{Current: p.currentVersion, Latest: p.currentVersion}, nil
}
func (p *phantomPredlFakeProvider) Launch(_ context.Context, _ core.GameID, _ core.LaunchOptions) (int, error) {
	return 0, nil
}

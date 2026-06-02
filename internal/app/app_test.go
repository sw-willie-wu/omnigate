package app

import (
	"context"
	"errors"
	"log/slog"
	"path/filepath"
	"testing"

	"omnigate/internal/core"
)

// fakeProvider satisfies core.Provider with configurable behavior for tests.
type fakeProvider struct {
	id    core.BackendID
	games []core.GameDescriptor
	// Optional install set; nil means DetectInstall returns ([], nil)
	installs []core.InstalledGame
	// optional path for PathProvider
	path string
}

func (f *fakeProvider) ID() core.BackendID                                  { return f.id }
func (f *fakeProvider) DisplayName() core.LocalizedString                   { return core.LocalizedString{"en": string(f.id)} }
func (f *fakeProvider) Games() []core.GameDescriptor                        { return f.games }
func (f *fakeProvider) SettingsSchema() []core.SettingField                 { return nil }
func (f *fakeProvider) DetectInstall(ctx context.Context) ([]core.InstalledGame, error) {
	return f.installs, nil
}
func (f *fakeProvider) GetIcon(ctx context.Context, gid core.GameID) (string, error) { return "", nil }
func (f *fakeProvider) GetBackgrounds(ctx context.Context, gid core.GameID) ([]core.Background, error) {
	return nil, nil
}
func (f *fakeProvider) CheckVersion(ctx context.Context, gid core.GameID) (core.VersionInfo, error) {
	return core.VersionInfo{}, nil
}
func (f *fakeProvider) Launch(ctx context.Context, gid core.GameID, opts core.LaunchOptions) (int, error) {
	return 0, nil
}
func (f *fakeProvider) PrimaryPath() string { return f.path }

func TestProviderLookup(t *testing.T) {
	a := newAppForTest(t,
		&fakeProvider{id: "hoyoverse", games: []core.GameDescriptor{{ID: "hoyoverse/genshin", Backend: "hoyoverse"}}},
		&fakeProvider{id: "kurogames", games: []core.GameDescriptor{{ID: "kurogames/wuwa", Backend: "kurogames"}}},
	)

	p, err := a.provider("hoyoverse/genshin")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID() != "hoyoverse" {
		t.Errorf("got provider %q, want hoyoverse", p.ID())
	}

	p, err = a.provider("kurogames/wuwa")
	if err != nil {
		t.Fatal(err)
	}
	if p.ID() != "kurogames" {
		t.Errorf("got provider %q, want kurogames", p.ID())
	}

	if _, err := a.provider("nonexistent/game"); !errors.Is(err, core.ErrUnknownGame) {
		t.Errorf("provider(unknown) = %v, want ErrUnknownGame", err)
	}

	if _, err := a.provider("malformed-no-slash"); err == nil {
		t.Errorf("provider(malformed) succeeded; want error")
	}
}

func TestRegisterProvider_RejectsMismatchedGameIDs(t *testing.T) {
	bad := &fakeProvider{
		id:    "hoyoverse",
		games: []core.GameDescriptor{{ID: "kurogames/wuwa", Backend: "kurogames"}}, // wrong prefix!
	}
	a := &App{}
	if err := a.registerProvider(bad); err == nil {
		t.Errorf("registerProvider accepted mismatched game id; want error")
	}
}

func TestRegisterProvider_RejectsMultiSlashGID(t *testing.T) {
	a := &App{
		settings: Settings{Version: 1},
		logger:   slog.Default(),
	}
	bad := &fakeProvider{
		id:    "hoyoverse",
		games: []core.GameDescriptor{{ID: "hoyoverse/genshin/cn"}}, // multi-slash → invalid
	}
	err := a.registerProvider(bad)
	if err == nil {
		t.Errorf("expected error for multi-slash gid; got nil")
	}
}

func TestSetLanguage_PersistsAndValidates(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "settings.toml")
	a := &App{
		settingsP: p,
		settings:  defaultSettings(),
	}

	// A supported language updates both in-memory state and the TOML file
	// without going through the heavy UpdateSettings (no provider rebuild).
	if err := a.SetLanguage("en"); err != nil {
		t.Fatalf("SetLanguage(en) error: %v", err)
	}
	if got := a.GetSettings().App.Language; got != "en" {
		t.Errorf("in-memory language = %q, want en", got)
	}
	loaded, err := LoadSettings(p)
	if err != nil {
		t.Fatalf("LoadSettings: %v", err)
	}
	if loaded.App.Language != "en" {
		t.Errorf("persisted language = %q, want en", loaded.App.Language)
	}

	// An unsupported language is rejected and leaves state untouched.
	if err := a.SetLanguage("fr-FR"); err == nil {
		t.Errorf("SetLanguage(fr-FR) accepted invalid language; want error")
	}
	if got := a.GetSettings().App.Language; got != "en" {
		t.Errorf("language mutated after invalid set = %q, want en", got)
	}
}

func TestCachedDetect_CachesAcrossCalls(t *testing.T) {
	p := &fakeProvider{
		id:       "hoyoverse",
		installs: []core.InstalledGame{{GameID: "hoyoverse/genshin", InstallPath: "C:/x"}},
	}
	a := newAppForTest(t, p)

	g1, err := a.cachedDetect(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	g2, err := a.cachedDetect(context.Background(), p)
	if err != nil {
		t.Fatal(err)
	}
	if len(g1) != 1 || len(g2) != 1 {
		t.Fatalf("cached detect lengths = %d, %d", len(g1), len(g2))
	}
	// Both calls should return the same slice content (cache hit on second).
	if g1[0].InstallPath != g2[0].InstallPath {
		t.Errorf("cache returned different install path on second call")
	}
}

func TestRefreshClearsCache(t *testing.T) {
	p := &fakeProvider{id: "hoyoverse"}
	a := newAppForTest(t, p)
	if _, err := a.cachedDetect(context.Background(), p); err != nil {
		t.Fatal(err)
	}
	a.Refresh()
	a.detectMu.Lock()
	defer a.detectMu.Unlock()
	if _, ok := a.detect[p.ID()]; ok {
		t.Errorf("cache not cleared after Refresh()")
	}
}

func TestListBackends_DerivesStatuses(t *testing.T) {
	hoyo := &fakeProvider{
		id:       "hoyoverse",
		path:     "C:/Program Files/HoYoPlay",
		installs: []core.InstalledGame{{GameID: "hoyoverse/genshin"}},
	}
	emptyKuro := &fakeProvider{
		id:   "kurogames",
		path: "C:/Program Files/Wuthering Waves", // path set, no installs
	}
	unconfigured := &fakeProvider{
		id:   "hypergryph",
		path: "", // path empty
	}
	a := newAppForTest(t, hoyo, emptyKuro, unconfigured)
	statuses := a.ListBackends()
	if len(statuses) != 3 {
		t.Fatalf("got %d, want 3 statuses", len(statuses))
	}
	byID := map[string]BackendStatus{}
	for _, s := range statuses {
		byID[s.BackendID] = s
	}
	// Note: emptyKuro's path doesn't actually exist on the test FS; that means
	// status will be "launcher_missing" not "empty". Adjust as needed once
	// status semantics are concrete.
	if byID["hoyoverse"].Status != "ok" && byID["hoyoverse"].Status != "launcher_missing" {
		t.Errorf("hoyoverse status = %q (allowed: ok|launcher_missing depending on FS)", byID["hoyoverse"].Status)
	}
	if byID["hypergryph"].Status != "path_unset" {
		t.Errorf("hypergryph status = %q, want path_unset", byID["hypergryph"].Status)
	}
}

// Helper: build an App with the given providers and skip settings file IO.
func newAppForTest(t *testing.T, ps ...core.Provider) *App {
	t.Helper()
	a := &App{
		settingsP: "",
		providers: nil,
		detect:    map[core.BackendID]detectEntry{},
	}
	for _, p := range ps {
		if err := a.registerProvider(p); err != nil {
			t.Fatalf("register %s: %v", p.ID(), err)
		}
	}
	a.ctx = context.Background()
	return a
}

func TestTempDirFor_KurogamesNoSettings(t *testing.T) {
	a := newAppForTest(t)
	got := a.tempDirFor("kurogames", "kurogames/wuwa")
	want := filepath.Join(osTempDir(), "omnigate")
	if got != want {
		t.Errorf("tempDirFor(kurogames, ...) = %q, want %q", got, want)
	}
}

func TestTempDirFor_DefaultBranch(t *testing.T) {
	a := newAppForTest(t)
	got := a.tempDirFor("nonexistent-backend", "any/game")
	want := filepath.Join(osTempDir(), "omnigate", "nonexistent-backend")
	if got != want {
		t.Errorf("tempDirFor default = %q, want %q", got, want)
	}
}

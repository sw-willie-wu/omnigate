package app

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"omnigate/internal/core"
	"omnigate/internal/providers/hoyoverse"
	"omnigate/internal/providers/hypergryph"
	"omnigate/internal/providers/kurogames"
)

// fakeProvider satisfies core.Provider with configurable behavior for tests.
type fakeProvider struct {
	id    core.BackendID
	games []core.GameDescriptor
	// Optional install set; nil means DetectInstall returns ([], nil)
	installs []core.InstalledGame
	// DefaultScan result + captured SetResolvedPaths injection (Task 8)
	def      map[core.GameID]string
	injected map[core.GameID]string
}

func (f *fakeProvider) DefaultScan(context.Context) (map[core.GameID]string, error) {
	return f.def, nil
}
func (f *fakeProvider) SetResolvedPaths(paths map[core.GameID]string) { f.injected = paths }

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
		installs: []core.InstalledGame{{GameID: "hoyoverse/genshin"}},
	}
	emptyKuro := &fakeProvider{
		id: "kurogames", // no installs
	}
	unconfigured := &fakeProvider{
		id: "hypergryph",
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
	// Status is now derived from a.resolved (ok | empty). None of these providers
	// have a resolvable path (no def map, no override) → all "empty".
	if byID["hoyoverse"].Status != "empty" {
		t.Errorf("hoyoverse status = %q, want empty", byID["hoyoverse"].Status)
	}
	if byID["hypergryph"].Status != "empty" {
		t.Errorf("hypergryph status = %q, want empty", byID["hypergryph"].Status)
	}
}

// buildAppWithResolved builds an App with a single fakeProvider whose game gid
// resolves (via DefaultScan) to dir, runs resolveAll, and returns the App.
// The backend prefix is derived from gid (e.g. "fake/g" → "fake").
func buildAppWithResolved(t *testing.T, gid core.GameID, dir string) *App {
	t.Helper()
	backend, _, err := core.ParseGameID(gid)
	if err != nil {
		t.Fatalf("bad gid %q: %v", gid, err)
	}
	fp := &fakeProvider{
		id:    backend,
		games: []core.GameDescriptor{{ID: gid, Backend: backend}},
		def:   map[core.GameID]string{gid: dir},
	}
	a := &App{
		settings: Settings{Version: 2, Games: map[string]GameSettings{}},
		detect:   map[core.BackendID]detectEntry{},
		resolved: map[core.GameID]resolvedEntry{},
		logger:   slog.Default(),
	}
	a.providers = []core.Provider{fp}
	a.ctx = context.Background()
	a.resolveAll(a.ctx) // single goroutine in test: no lock needed
	return a
}

func TestListGames_CarriesResolvedSource(t *testing.T) {
	dir := t.TempDir()
	gid := core.GameID("fake/g")
	a := buildAppWithResolved(t, gid, dir)
	rows, _ := a.ListGames()
	var row *GameRow
	for i := range rows {
		if rows[i].ID == string(gid) {
			row = &rows[i]
		}
	}
	if row == nil || !row.Installed || row.ResolvedPath != dir || row.PathSource != string(core.SourceDefault) {
		t.Fatalf("row=%+v", row)
	}
}

func TestListGames_InvalidOverride_NotInstalled(t *testing.T) {
	gid := core.GameID("fake/g")
	a := buildAppWithResolved(t, gid, t.TempDir())
	// set an override to a non-existent dir, re-resolve
	a.settings.Games[string(gid)] = GameSettings{Path: `Z:\does\not\exist`}
	a.resolveAll(a.ctx)
	rows, _ := a.ListGames()
	var row *GameRow
	for i := range rows {
		if rows[i].ID == string(gid) {
			row = &rows[i]
		}
	}
	if row == nil || row.PathSource != string(core.SourceOverride) || row.Installed {
		t.Fatalf("invalid override row=%+v (want source=override, installed=false)", row)
	}
	if row.OverridePath != `Z:\does\not\exist` {
		t.Fatalf("override path not surfaced: %+v", row)
	}
}

func TestResolveAll_InjectsUnderWriteLock(t *testing.T) {
	dir := t.TempDir()
	gid := core.GameID("fake/g")
	fp := &fakeProvider{
		id:    "fake",
		games: []core.GameDescriptor{{ID: gid, Backend: "fake"}},
		def:   map[core.GameID]string{gid: dir},
	}
	a := &App{
		settings: Settings{Version: 2, Games: map[string]GameSettings{}},
		detect:   map[core.BackendID]detectEntry{},
		resolved: map[core.GameID]resolvedEntry{},
		logger:   slog.Default(),
	}
	a.providers = []core.Provider{fp}
	a.ctx = context.Background()

	// Mirror the real call path: resolveAll runs while the settings write lock
	// is held (as constructProviders does). Must NOT deadlock.
	a.settingsMu.Lock()
	a.resolveAll(a.ctx)
	a.settingsMu.Unlock()

	if fp.injected[gid] != dir {
		t.Fatalf("not injected: %+v", fp.injected)
	}
	if a.resolved[gid].Source != core.SourceDefault || a.resolved[gid].Path != dir {
		t.Fatalf("resolved wrong: %+v", a.resolved[gid])
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

func TestSetGameOverride_PersistsResolvesReturnsRow(t *testing.T) {
	overrideDir := t.TempDir()
	gid := core.GameID("fake/g")
	a := buildAppWithResolved(t, gid, t.TempDir())
	a.settingsP = filepath.Join(t.TempDir(), "settings.toml")
	row, err := a.SetGameOverride(string(gid), overrideDir)
	if err != nil {
		t.Fatal(err)
	}
	if row.PathSource != string(core.SourceOverride) || row.ResolvedPath != overrideDir || row.OverridePath != overrideDir || !row.Installed {
		t.Fatalf("row=%+v", row)
	}
	a.settingsMu.RLock()
	got := a.settings.Games[string(gid)].Path
	a.settingsMu.RUnlock()
	if got != overrideDir {
		t.Errorf("not persisted: %q", got)
	}
}

func TestClearGameOverride_RevertsToDetection(t *testing.T) {
	dir := t.TempDir()
	gid := core.GameID("fake/g")
	a := buildAppWithResolved(t, gid, dir) // default-scan resolves to dir
	a.settingsP = filepath.Join(t.TempDir(), "settings.toml")
	_, _ = a.SetGameOverride(string(gid), t.TempDir())
	row, err := a.ClearGameOverride(string(gid))
	if err != nil {
		t.Fatal(err)
	}
	if row.PathSource != string(core.SourceDefault) || row.ResolvedPath != dir || row.OverridePath != "" {
		t.Fatalf("after clear row=%+v (want default %q, no override)", row, dir)
	}
}

func TestSetGameOverride_PreservesBackgroundPath(t *testing.T) {
	gid := core.GameID("fake/g")
	a := buildAppWithResolved(t, gid, t.TempDir())
	a.settingsP = filepath.Join(t.TempDir(), "settings.toml")
	a.settings.Games = map[string]GameSettings{string(gid): {BackgroundPath: `D:\bg.png`}}
	override := t.TempDir()
	if _, err := a.SetGameOverride(string(gid), override); err != nil {
		t.Fatal(err)
	}
	a.settingsMu.RLock()
	g := a.settings.Games[string(gid)]
	a.settingsMu.RUnlock()
	if g.Path != override {
		t.Errorf("Path = %q, want %q", g.Path, override)
	}
	if g.BackgroundPath != `D:\bg.png` {
		t.Errorf("BackgroundPath lost on SetGameOverride: %q", g.BackgroundPath)
	}
}

func TestClearGameOverride_KeepsEntryWhenBackgroundPathSet(t *testing.T) {
	dir := t.TempDir()
	gid := core.GameID("fake/g")
	a := buildAppWithResolved(t, gid, dir)
	a.settingsP = filepath.Join(t.TempDir(), "settings.toml")
	a.settings.Games = map[string]GameSettings{string(gid): {Path: t.TempDir(), BackgroundPath: `D:\bg.png`}}
	if _, err := a.ClearGameOverride(string(gid)); err != nil {
		t.Fatal(err)
	}
	a.settingsMu.RLock()
	g, ok := a.settings.Games[string(gid)]
	a.settingsMu.RUnlock()
	if !ok {
		t.Fatal("entry deleted; BackgroundPath should have kept it")
	}
	if g.Path != "" {
		t.Errorf("Path = %q, want empty after clear", g.Path)
	}
	if g.BackgroundPath != `D:\bg.png` {
		t.Errorf("BackgroundPath lost on ClearGameOverride: %q", g.BackgroundPath)
	}
}

func TestRefreshGame_ReResolvesNoSettingsChange(t *testing.T) {
	dir := t.TempDir()
	gid := core.GameID("fake/g")
	a := buildAppWithResolved(t, gid, dir)
	a.settingsP = filepath.Join(t.TempDir(), "settings.toml")
	row, err := a.RefreshGame(string(gid))
	if err != nil {
		t.Fatal(err)
	}
	if row.PathSource != string(core.SourceDefault) || row.ResolvedPath != dir || !row.Installed {
		t.Fatalf("row=%+v", row)
	}
	// No override should have been written.
	a.settingsMu.RLock()
	_, present := a.settings.Games[string(gid)]
	a.settingsMu.RUnlock()
	if present {
		t.Errorf("RefreshGame mutated settings.Games")
	}
}

func TestSetGameOverride_UnknownGameErrors(t *testing.T) {
	gid := core.GameID("fake/g")
	a := buildAppWithResolved(t, gid, t.TempDir())
	a.settingsP = filepath.Join(t.TempDir(), "settings.toml")
	if _, err := a.SetGameOverride("other/missing", t.TempDir()); err == nil {
		t.Fatalf("SetGameOverride(unknown) succeeded; want error")
	}
}

func TestLaunch_RecordsLastPlayed(t *testing.T) {
	dir := t.TempDir()
	gid := core.GameID("fake/g")
	a := buildAppWithResolved(t, gid, dir)
	a.playState = loadPlayState(filepath.Join(t.TempDir(), "playstate.json"))

	if _, err := a.Launch(string(gid)); err != nil {
		t.Fatalf("Launch failed: %v", err)
	}
	if a.playState.Get(string(gid)).IsZero() {
		t.Fatalf("Launch did not record last-played")
	}
	rows, _ := a.ListGames()
	var lp string
	for i := range rows {
		if rows[i].ID == string(gid) {
			lp = rows[i].LastPlayed
		}
	}
	if lp == "" {
		t.Fatalf("ListGames row missing last_played")
	}
}

// fakeProbeProvider embeds fakeProvider and adds a LastPlayedProbe returning a
// fixed file list, so we can drive lastPlayedLocked's stat/max logic.
type fakeProbeProvider struct {
	fakeProvider
	files []string
}

func (f *fakeProbeProvider) LastPlayedFiles(_ core.GameID, _ string) []string {
	return f.files
}

func writeFileWithMtime(t *testing.T, path string, mt time.Time) {
	t.Helper()
	if err := os.WriteFile(path, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mt, mt); err != nil {
		t.Fatal(err)
	}
}

func TestLastPlayedLocked_FileNewerThanPlaystate(t *testing.T) {
	dir := t.TempDir()
	logf := filepath.Join(dir, "output_log.txt")
	fileMt := time.Now().Add(-1 * time.Hour).Truncate(time.Second)
	writeFileWithMtime(t, logf, fileMt)

	a := &App{}
	a.playState = loadPlayState(filepath.Join(dir, "playstate.json"))
	// playstate older than the file:
	a.playState.last["g/x"] = fileMt.Add(-24 * time.Hour)

	p := &fakeProbeProvider{files: []string{logf}}
	got := a.lastPlayedLocked(p, "g/x", "")
	if !got.Equal(fileMt) {
		t.Errorf("want file mtime %v, got %v", fileMt, got)
	}
}

func TestLastPlayedLocked_PlaystateNewerThanFile(t *testing.T) {
	dir := t.TempDir()
	logf := filepath.Join(dir, "output_log.txt")
	fileMt := time.Now().Add(-48 * time.Hour).Truncate(time.Second)
	writeFileWithMtime(t, logf, fileMt)

	a := &App{}
	a.playState = loadPlayState(filepath.Join(dir, "playstate.json"))
	psMt := time.Now().Add(-1 * time.Hour).Truncate(time.Second)
	a.playState.last["g/x"] = psMt

	p := &fakeProbeProvider{files: []string{logf}}
	got := a.lastPlayedLocked(p, "g/x", "")
	if !got.Equal(psMt) {
		t.Errorf("want playstate %v, got %v", psMt, got)
	}
}

func TestLastPlayedLocked_NoProbeInterface(t *testing.T) {
	dir := t.TempDir()
	a := &App{}
	a.playState = loadPlayState(filepath.Join(dir, "playstate.json"))
	psMt := time.Now().Add(-1 * time.Hour).Truncate(time.Second)
	a.playState.last["g/x"] = psMt

	// plain fakeProvider does NOT implement LastPlayedProbe → playstate only.
	got := a.lastPlayedLocked(&fakeProvider{}, "g/x", "")
	if !got.Equal(psMt) {
		t.Errorf("want playstate %v, got %v", psMt, got)
	}
}

func TestLastPlayedLocked_MissingFileFallsBack(t *testing.T) {
	dir := t.TempDir()
	a := &App{}
	a.playState = loadPlayState(filepath.Join(dir, "playstate.json"))
	psMt := time.Now().Add(-1 * time.Hour).Truncate(time.Second)
	a.playState.last["g/x"] = psMt

	missing := filepath.Join(dir, "does-not-exist.txt")
	p := &fakeProbeProvider{files: []string{missing}}
	got := a.lastPlayedLocked(p, "g/x", "")
	if !got.Equal(psMt) {
		t.Errorf("want playstate %v (missing file ignored), got %v", psMt, got)
	}
}

func TestLastPlayedLocked_MultipleFilesNewestWins(t *testing.T) {
	dir := t.TempDir()
	older := filepath.Join(dir, "output_log.txt")
	newer := filepath.Join(dir, "Player.log")
	olderMt := time.Now().Add(-10 * time.Hour).Truncate(time.Second)
	newerMt := time.Now().Add(-2 * time.Hour).Truncate(time.Second)
	writeFileWithMtime(t, older, olderMt)
	writeFileWithMtime(t, newer, newerMt)

	a := &App{}
	a.playState = loadPlayState(filepath.Join(dir, "playstate.json"))
	a.playState.last["g/x"] = newerMt.Add(-24 * time.Hour) // older than both files

	// Candidate order puts the OLDER file first; the loop must still pick newer.
	p := &fakeProbeProvider{files: []string{older, newer}}
	got := a.lastPlayedLocked(p, "g/x", "")
	if !got.Equal(newerMt) {
		t.Errorf("want newest file mtime %v, got %v", newerMt, got)
	}
}

func TestLastPlayedLocked_NilPlayState(t *testing.T) {
	dir := t.TempDir()
	logf := filepath.Join(dir, "output_log.txt")
	fileMt := time.Now().Add(-1 * time.Hour).Truncate(time.Second)
	writeFileWithMtime(t, logf, fileMt)

	a := &App{} // playState nil — must not panic
	p := &fakeProbeProvider{files: []string{logf}}
	got := a.lastPlayedLocked(p, "g/x", "")
	if !got.Equal(fileMt) {
		t.Errorf("want file mtime %v, got %v", fileMt, got)
	}
}

// fakeNewsProvider embeds fakeProvider and returns canned news.
type fakeNewsProvider struct {
	fakeProvider
	items   []core.NewsItem
	gotLang string
}

func (f *fakeNewsProvider) GetNews(_ context.Context, _ core.GameID, lang string) ([]core.NewsItem, error) {
	f.gotLang = lang
	return f.items, nil
}

func TestGetNews_RoutesToProvider(t *testing.T) {
	gid := core.GameID("fake/g")
	fp := &fakeNewsProvider{
		fakeProvider: fakeProvider{id: "fake", games: []core.GameDescriptor{{ID: gid, Backend: "fake"}}},
		items:        []core.NewsItem{{Title: "Hello", Category: core.NewsAnnounce, URL: "https://x/1"}},
	}
	a := &App{settings: Settings{Version: 2}, logger: slog.Default()}
	a.providers = []core.Provider{fp}
	a.ctx = context.Background()

	out, err := a.GetNews(string(gid), "zh-TW")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Title != "Hello" {
		t.Fatalf("got %v", out)
	}
	if fp.gotLang != "zh-TW" {
		t.Errorf("lang passthrough = %q, want zh-TW", fp.gotLang)
	}
}

func TestGetNews_ProviderWithoutNews_ReturnsEmpty(t *testing.T) {
	gid := core.GameID("fake/g")
	a := &App{settings: Settings{Version: 2}, logger: slog.Default()}
	a.providers = []core.Provider{&fakeProvider{id: "fake", games: []core.GameDescriptor{{ID: gid, Backend: "fake"}}}}
	a.ctx = context.Background()

	out, err := a.GetNews(string(gid), "en")
	if err != nil {
		t.Fatalf("want nil err, got %v", err)
	}
	if len(out) != 0 {
		t.Fatalf("want empty, got %v", out)
	}
}

// fakeLangNewsProvider returns per-lang canned news and records the langs asked.
type fakeLangNewsProvider struct {
	fakeProvider
	byLang     map[string][]core.NewsItem
	askedLangs []string
}

func (f *fakeLangNewsProvider) GetNews(_ context.Context, _ core.GameID, lang string) ([]core.NewsItem, error) {
	f.askedLangs = append(f.askedLangs, lang)
	return f.byLang[lang], nil
}

func TestGetNews_ZhCNEmptyFallsBackToZhTW(t *testing.T) {
	gid := core.GameID("fake/g")
	fp := &fakeLangNewsProvider{
		fakeProvider: fakeProvider{id: "fake", games: []core.GameDescriptor{{ID: gid, Backend: "fake"}}},
		byLang: map[string][]core.NewsItem{
			"zh-TW": {{Title: "繁中新聞", Category: core.NewsAnnounce, URL: "https://x/1"}},
			// zh-CN intentionally absent → empty, so it must fall back to zh-TW.
		},
	}
	a := &App{settings: Settings{Version: 3}, logger: slog.Default()}
	a.providers = []core.Provider{fp}
	a.ctx = context.Background()

	out, err := a.GetNews(string(gid), "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Title != "繁中新聞" {
		t.Fatalf("want zh-TW fallback news, got %v", out)
	}
	if len(fp.askedLangs) != 2 || fp.askedLangs[0] != "zh-CN" || fp.askedLangs[1] != "zh-TW" {
		t.Errorf("expected fetch sequence [zh-CN, zh-TW], got %v", fp.askedLangs)
	}
}

func TestGetNews_ZhCNWithData_NoFallback(t *testing.T) {
	gid := core.GameID("fake/g")
	fp := &fakeLangNewsProvider{
		fakeProvider: fakeProvider{id: "fake", games: []core.GameDescriptor{{ID: gid, Backend: "fake"}}},
		byLang: map[string][]core.NewsItem{
			"zh-CN": {{Title: "简中新闻", Category: core.NewsAnnounce, URL: "https://x/1"}},
			"zh-TW": {{Title: "繁中新聞", Category: core.NewsAnnounce, URL: "https://x/2"}},
		},
	}
	a := &App{settings: Settings{Version: 3}, logger: slog.Default()}
	a.providers = []core.Provider{fp}
	a.ctx = context.Background()

	out, err := a.GetNews(string(gid), "zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 1 || out[0].Title != "简中新闻" {
		t.Fatalf("want zh-CN news with no fallback, got %v", out)
	}
	if len(fp.askedLangs) != 1 {
		t.Errorf("expected only [zh-CN] (no fallback when zh-CN has data), got %v", fp.askedLangs)
	}
}

func TestOpenExternalURL_RejectsNonHTTP(t *testing.T) {
	a := &App{logger: slog.Default()}
	if err := a.OpenExternalURL("file:///etc/passwd"); err == nil {
		t.Errorf("file:// accepted; want rejected")
	}
	if err := a.OpenExternalURL("javascript:alert(1)"); err == nil {
		t.Errorf("javascript: accepted; want rejected")
	}
	if err := a.OpenExternalURL("https://example.com"); err != nil {
		t.Errorf("https rejected: %v", err)
	}
}

func TestTempDirFor_UsesGlobalAppTempDir(t *testing.T) {
	a := &App{settings: Settings{App: AppSettings{TempDir: `D:\custom`}}}
	if got := a.tempDirFor(hoyoverse.BackendID, "hoyoverse/genshin"); got != filepath.Join(`D:\custom`, "hoyoverse") {
		t.Errorf("hoyoverse tempDir = %q", got)
	}
	if got := a.tempDirFor(kurogames.BackendID, "kurogames/wuwa"); got != `D:\custom` {
		t.Errorf("kuro tempDir = %q (want flat root)", got)
	}
	if got := a.tempDirFor(hypergryph.BackendID, "hypergryph/endfield"); got != filepath.Join(`D:\custom`, "hypergryph") {
		t.Errorf("gryph tempDir = %q", got)
	}
}

func TestTempDirFor_EmptyFallsBackToOSTemp(t *testing.T) {
	a := &App{settings: Settings{App: AppSettings{TempDir: ""}}}
	if got := a.tempDirFor(kurogames.BackendID, "kurogames/wuwa"); got != filepath.Join(osTempDir(), "omnigate") {
		t.Errorf("default kuro tempDir = %q", got)
	}
}

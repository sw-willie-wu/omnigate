package kurogames

import (
	"bytes"
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omnigate/internal/core"
)

func stateWith(t *testing.T, body string) installState {
	t.Helper()
	p := writeFile(t, t.TempDir(), body)
	s, err := readInstallState(p)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

const twoBundles = `{"version":"3.7.0","bundles":{"HD":{"version":"3.7.0","state":"","resourcePacks":["common","hd"]},"SD":{"version":"3.7.0","state":"","resourcePacks":["common","sd"]}}}`

func TestResolveActiveBundle(t *testing.T) {
	s := stateWith(t, twoBundles)
	if got := resolveActiveBundle(s, "SD"); got != "SD" {
		t.Fatalf("configured SD → %s", got)
	}
	if got := resolveActiveBundle(s, "UHD"); got != "HD" { // not installed → priority
		t.Fatalf("configured UHD → %s", got)
	}
	if got := resolveActiveBundle(s, ""); got != "HD" {
		t.Fatalf("empty → %s", got)
	}
	if got := resolveActiveBundle(installState{Bundles: map[string]installedBundle{}}, ""); got != "HD" {
		t.Fatalf("no record → %s", got)
	}
}

func TestBuildLaunchArgs(t *testing.T) {
	cat := core.BundleCatalog{Bundles: []core.BundleCatalogEntry{
		{Name: "HD", Options: []core.LaunchOption{{Cmd: "-slno"}, {Cmd: "-dx11"}, {Cmd: "-foo", Default: true}}},
	}}
	got := buildLaunchArgs("HD", cat, true, map[string]bool{"-slno": true, "-foo": false})
	if strings.Join(got, " ") != "-krqlv=hd -slno" {
		t.Fatalf("got %v", got)
	}
	if strings.Join(buildLaunchArgs("HD", cat, true, nil), " ") != "-krqlv=hd -foo" {
		t.Fatal("defaults not applied")
	}
	if strings.Join(buildLaunchArgs("SD", core.BundleCatalog{}, false, nil), " ") != "-krqlv=sd" {
		t.Fatal("no catalog should yield only -krqlv")
	}
}

func TestLoadCatalog_CorruptJSONIsMissing(t *testing.T) { // Review Focus #5
	p := New(Settings{}, nil)
	gid := core.GameID("kurogames/wutheringwaves")
	_ = p.kv.SetConfig(kvKey(gid, "bundle_catalog"), "{not json")
	if _, ok := p.loadCatalog(gid); ok {
		t.Fatal("corrupt catalog must read as missing")
	}
}

func TestLaunchArgsForGame_WarnsWhenActiveDirMissing(t *testing.T) { // Review Focus #2
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, installStateFile), []byte(twoBundles), 0o644)
	var buf bytes.Buffer
	p := New(Settings{}, slog.New(slog.NewTextHandler(&buf, nil)))
	gid := core.GameID("kurogames/wutheringwaves")
	args := p.launchArgsFor(context.Background(), gid, dir)
	if strings.Join(args, " ") != "-krqlv=hd" {
		t.Fatalf("args=%v", args)
	}
	if !strings.Contains(buf.String(), "active bundle dir missing") {
		t.Fatalf("no warn logged: %s", buf.String())
	}
}

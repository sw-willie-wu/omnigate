package kurogames

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, dir, body string) string {
	t.Helper()
	p := filepath.Join(dir, installStateFile)
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestReadInstallState_V3(t *testing.T) {
	p := writeFile(t, t.TempDir(), `{"appId":"50004","version":"3.7.0","state":"","bundles":{"HD":{"version":"3.7.0","state":"","resourcePacks":["common","hd"]}}}`)
	s, err := readInstallState(p)
	if err != nil || s.Legacy || strings.Join(s.installedKnown(), ",") != "HD" || s.packVersion("common") != "3.7.0" || s.packVersion("hd") != "3.7.0" {
		t.Fatalf("s=%+v err=%v", s, err)
	}
}

func TestReadInstallState_V2Legacy(t *testing.T) {
	p := writeFile(t, t.TempDir(), `{"appId":"50004","version":"3.6.1","state":""}`)
	s, _ := readInstallState(p)
	if !s.Legacy || strings.Join(s.installedKnown(), ",") != "HD" || s.packVersion("common") != "3.6.1" {
		t.Fatalf("s=%+v", s)
	}
}

func TestReadInstallState_Missing(t *testing.T) {
	s, err := readInstallState(filepath.Join(t.TempDir(), installStateFile))
	if err != nil || len(s.installedKnown()) != 0 || s.packVersion("common") != "" {
		t.Fatalf("s=%+v err=%v", s, err)
	}
}

func TestReadInstallState_BOM(t *testing.T) { // Review Focus #1
	p := writeFile(t, t.TempDir(), "\ufeff"+`{"version":"3.7.0","bundles":{"HD":{"version":"3.7.0","state":"","resourcePacks":["common","hd"]}}}`)
	s, err := readInstallState(p)
	if err != nil || s.packVersion("hd") != "3.7.0" {
		t.Fatalf("s=%+v err=%v", s, err)
	}
}

func TestInstallState_PendingAndUnknownExcluded(t *testing.T) {
	p := writeFile(t, t.TempDir(), `{"version":"3.7.0","bundles":{
		"HD":{"version":"3.6.1","state":"","resourcePacks":["common","hd"]},
		"SD":{"version":"3.7.0","state":"downloading","resourcePacks":["common","sd"]},
		"X4K":{"version":"3.7.0","state":"","resourcePacks":["common","x4k"]}}}`)
	s, _ := readInstallState(p)
	if strings.Join(s.installedKnown(), ",") != "HD" || strings.Join(s.pendingKnown(), ",") != "SD" {
		t.Fatalf("installed=%v pending=%v", s.installedKnown(), s.pendingKnown())
	}
	if s.packVersion("common") != "3.6.1" { // min over installedKnown only
		t.Fatalf("common=%q", s.packVersion("common"))
	}
}

func TestInstalledKnown_PriorityOrder(t *testing.T) {
	p := writeFile(t, t.TempDir(), `{"version":"3.7.0","bundles":{
		"SD":{"version":"3.7.0","state":"","resourcePacks":["common","sd"]},
		"UHD":{"version":"3.7.0","state":"","resourcePacks":["common","uhd"]},
		"HD":{"version":"3.7.0","state":"","resourcePacks":["common","hd"]}}}`)
	s, _ := readInstallState(p)
	if strings.Join(s.installedKnown(), ",") != "UHD,HD,SD" {
		t.Fatalf("got %v", s.installedKnown())
	}
}

func TestWriteInstallState_PreservesUnknownFieldsAndUpgradesLegacy(t *testing.T) {
	p := writeFile(t, t.TempDir(), `{"appId":"50004","version":"3.6.1","state":"","extra":42}`)
	if err := writeInstallState(p, func(s *installState) {
		s.Version = "3.7.0"
		for _, n := range s.installedKnown() {
			b := s.Bundles[n]
			b.Version = "3.7.0"
			s.Bundles[n] = b
		}
		s.Bundles["SD"] = installedBundle{Version: "3.7.0", ResourcePacks: []string{"common", "sd"}}
	}); err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	b, _ := os.ReadFile(p)
	_ = json.Unmarshal(b, &doc)
	if doc["appId"] != "50004" || doc["extra"].(float64) != 42 || doc["version"] != "3.7.0" {
		t.Fatalf("doc=%v", doc)
	}
	bundles := doc["bundles"].(map[string]any)
	hd := bundles["HD"].(map[string]any)
	sd := bundles["SD"].(map[string]any)
	if hd["version"] != "3.7.0" || hd["state"] != "" || sd["state"] != "" || sd["resourcePacks"].([]any)[1] != "sd" {
		t.Fatalf("bundles=%v", bundles)
	}
}

func TestWriteInstallState_PreservesBundleInnerFieldsAndUnknownBundles(t *testing.T) {
	p := writeFile(t, t.TempDir(), `{"version":"3.7.0","bundles":{
		"HD":{"version":"3.7.0","state":"","resourcePacks":["common","hd"],"inner":"keep"},
		"X4K":{"version":"3.7.0","state":"","resourcePacks":["common","x4k"]}}}`)
	_ = writeInstallState(p, func(s *installState) { delete(s.Bundles, "SD") })
	b, _ := os.ReadFile(p)
	if !strings.Contains(string(b), `"inner": "keep"`) || !strings.Contains(string(b), `"X4K"`) {
		t.Fatalf("lost fields: %s", b)
	}
}

func TestVersionLess(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"3.6.1", "3.7.0", true}, {"3.10.0", "3.9.9", false}, {"3.7.0", "3.7.0", false}, {"", "3.7.0", true},
	}
	for _, c := range cases {
		if versionLess(c.a, c.b) != c.want {
			t.Errorf("versionLess(%q,%q) != %v", c.a, c.b, c.want)
		}
	}
}

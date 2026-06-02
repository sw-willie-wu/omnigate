package hoyoverse

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
)

func TestCheckVersion_ParsesMainAndPredownload(t *testing.T) {
	body := `{"retcode":0,"data":{"game_packages":[{"game":{"id":"gopR6Cufr3","biz":"hk4e_global"},"main":{"major":{"version":"5.5.0"},"patches":[]},"pre_download":{"major":{"version":"5.6.0"},"patches":[]}}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()

	c := newAPIClient(srv.URL, http.DefaultClient)
	got, err := c.fetchVersion(context.Background(), "gopR6Cufr3", "5.5.0")
	if err != nil {
		t.Fatal(err)
	}
	if got.Latest != "5.5.0" {
		t.Errorf("Latest = %s, want 5.5.0", got.Latest)
	}
	if got.Current != "5.5.0" {
		t.Errorf("Current = %s, want 5.5.0", got.Current)
	}
	if got.Predownload == nil || got.Predownload.TargetVersion != "5.6.0" {
		t.Errorf("Predownload = %+v, want target 5.6.0", got.Predownload)
	}
}

func TestCheckVersion_NoPredownload(t *testing.T) {
	body := `{"retcode":0,"data":{"game_packages":[{"game":{"id":"gopR6Cufr3","biz":"hk4e_global"},"main":{"major":{"version":"5.5.0"}}}]}}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(body))
	}))
	defer srv.Close()
	c := newAPIClient(srv.URL, http.DefaultClient)
	got, err := c.fetchVersion(context.Background(), "gopR6Cufr3", "5.5.0")
	if err != nil {
		t.Fatal(err)
	}
	if got.Predownload != nil {
		t.Errorf("Predownload = %+v, want nil", got.Predownload)
	}
}

func TestParseGamePackages_WithPatchesAndPreDownload(t *testing.T) {
	// Try multiple paths in case of different working directories
	var rawBody []byte
	var err error
	for _, path := range []string{
		"testdata/manifest-sample.json",
		"./testdata/manifest-sample.json",
		"internal/providers/hoyoverse/testdata/manifest-sample.json",
	} {
		rawBody, err = os.ReadFile(path)
		if err == nil {
			break
		}
	}
	if err != nil {
		t.Fatalf("could not read testdata: %v", err)
	}

	// Extract the "data" field from the envelope
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rawBody, &env); err != nil {
		t.Fatalf("failed to parse envelope: %v", err)
	}

	resp, err := parseGamePackagesResponse(env.Data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if len(resp.Data.GamePackages) != 1 {
		t.Fatalf("expected 1 game_package; got %d", len(resp.Data.GamePackages))
	}
	gp := resp.Data.GamePackages[0]

	// main.major
	if gp.Main.Major.Version != "5.7.0" {
		t.Errorf("main.major.version = %q want 5.7.0", gp.Main.Major.Version)
	}
	if len(gp.Main.Major.GamePkgs) != 1 {
		t.Errorf("main.major.game_pkgs len = %d want 1", len(gp.Main.Major.GamePkgs))
	}
	if len(gp.Main.Major.AudioPkgs) != 2 {
		t.Errorf("main.major.audio_pkgs len = %d want 2", len(gp.Main.Major.AudioPkgs))
	}
	if gp.Main.Major.AudioPkgs[0].Language != "zh-cn" {
		t.Errorf("audio[0].language = %q want zh-cn", gp.Main.Major.AudioPkgs[0].Language)
	}
	if len(gp.Main.Patches) != 1 {
		t.Fatalf("main.patches len = %d want 1", len(gp.Main.Patches))
	}
	if gp.Main.Patches[0].Version != "5.6.0" {
		t.Errorf("patches[0].version = %q want 5.6.0 (FROM)", gp.Main.Patches[0].Version)
	}
	if gp.PreDownload == nil {
		t.Fatal("expected pre_download non-nil")
	}
	if gp.PreDownload.Major.Version != "5.8.0" {
		t.Errorf("pre_download.major.version = %q want 5.8.0", gp.PreDownload.Major.Version)
	}
}

func TestParseGamePackages_NoPreDownload(t *testing.T) {
	body := `{"game_packages":[{"game":{"id":"gopR6Cufr3","biz":"hk4e_global"},"main":{"major":{"version":"5.6.0","game_pkgs":[],"audio_pkgs":[]},"patches":[]}}]}`
	resp, err := parseGamePackagesResponse([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.Data.GamePackages[0].PreDownload != nil {
		t.Error("expected pre_download nil when absent")
	}
}

func TestParseGamePackages_DecompressedSizeStringToInt64(t *testing.T) {
	// Try multiple paths in case of different working directories
	var rawBody []byte
	var err error
	for _, path := range []string{
		"testdata/manifest-sample.json",
		"./testdata/manifest-sample.json",
		"internal/providers/hoyoverse/testdata/manifest-sample.json",
	} {
		rawBody, err = os.ReadFile(path)
		if err == nil {
			break
		}
	}
	if err != nil {
		t.Fatal(err)
	}

	// Extract the "data" field from the envelope
	var env struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(rawBody, &env); err != nil {
		t.Fatalf("failed to parse envelope: %v", err)
	}

	resp, err := parseGamePackagesResponse(env.Data)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	pkg := resp.Data.GamePackages[0].Main.Major.GamePkgs[0]
	if pkg.DecompressedSize != 30000000000 {
		t.Errorf("decompressed_size = %d want 30000000000", pkg.DecompressedSize)
	}
}

package kurogames

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"omnigate/internal/core"
)

func TestCheckForPredownload_TargetsPredownloadManifest(t *testing.T) {
	const gid = core.GameID("kurogames/wutheringwaves")
	index := `{
	  "default":{"version":"3.3.0","cdnList":[{"url":"PLACEHOLDER/","P":0}],"config":{"version":"3.3.0","indexFile":"default/indexFile.json","baseUrl":"default/zip/"}},
	  "predownload":{"version":"3.4.0","cdnList":[{"url":"PLACEHOLDER/","P":0}],"config":{"version":"3.4.0","indexFile":"predl/indexFile.json","baseUrl":"predl/zip/"}},
	  "predownloadSwitch":1
	}`
	manifest := `{"resource":[{"dest":"new.pak","md5":"abc","size":1234}]}`

	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"idx-etag"`)
		w.Write([]byte(strings.ReplaceAll(index, "PLACEHOLDER", srv.URL)))
	})
	mux.HandleFunc("/predl/indexFile.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(manifest))
	})
	mux.HandleFunc("/default/indexFile.json", func(w http.ResponseWriter, _ *http.Request) {
		t.Error("predownload check must NOT fetch the default manifest")
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	orig := indexJSONURL
	indexJSONURL = func() string { return srv.URL + "/index.json" }
	defer func() { indexJSONURL = orig }()

	p := New(Settings{}, nil)
	p.httpClient = srv.Client()
	p.SetResolvedPaths(map[core.GameID]string{gid: t.TempDir()})

	plan, err := p.CheckForPredownload(context.Background(), gid, nil)
	if err != nil {
		t.Fatalf("CheckForPredownload: %v", err)
	}
	if plan.Kind != core.PlanPredownload {
		t.Errorf("Kind = %v, want PlanPredownload", plan.Kind)
	}
	if plan.Version != "3.4.0" {
		t.Errorf("Version = %q, want 3.4.0 (predownload target)", plan.Version)
	}
	if len(plan.Files) != 1 || plan.Files[0].Path != "new.pak" {
		t.Fatalf("Files = %+v, want [new.pak]", plan.Files)
	}
	if !strings.Contains(plan.Files[0].URL, "/predl/zip/new.pak") {
		t.Errorf("file URL = %q, want predl CDN path", plan.Files[0].URL)
	}
	if plan.ManifestETag != `"idx-etag"` {
		t.Errorf("ManifestETag = %q, want index.json etag", plan.ManifestETag)
	}
}

func TestCheckForPredownload_NoActivePredl(t *testing.T) {
	const gid = core.GameID("kurogames/wutheringwaves")
	index := `{"default":{"version":"3.3.0","cdnList":[{"url":"x/","P":0}],"config":{}},"predownloadSwitch":0}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(index))
	}))
	defer srv.Close()
	orig := indexJSONURL
	indexJSONURL = func() string { return srv.URL }
	defer func() { indexJSONURL = orig }()

	p := New(Settings{}, nil)
	p.httpClient = srv.Client()
	p.SetResolvedPaths(map[core.GameID]string{gid: t.TempDir()})

	_, err := p.CheckForPredownload(context.Background(), gid, nil)
	if !errors.Is(err, core.ErrPredownloadUnsupported) {
		t.Fatalf("err = %v, want ErrPredownloadUnsupported", err)
	}
}

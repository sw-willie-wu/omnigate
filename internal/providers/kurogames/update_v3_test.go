package kurogames

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"omnigate/internal/core"
)

type v3Server struct {
	srv   *httptest.Server
	files map[string][]byte // path → body
	idx   *gameIndexV3      // the index as served (for planToken assertions)
}

// newV3Server serves game_index.json (rewritten so every pack's indexFile
// points at a served path with a matching md5) plus the given pack indexFiles.
// packs: pack → (full indexFile body, optional patch map fromVersion→body).
func newV3Server(t *testing.T, full map[string][]byte, patches map[string]map[string][]byte) *v3Server {
	t.Helper()
	s := &v3Server{files: map[string][]byte{}}
	idx := loadV3Index(t)
	md := func(b []byte) string { h := md5.Sum(b); return hex.EncodeToString(h[:]) }
	for pack, cfg := range idx.ResourcePacks {
		body, ok := full[pack]
		if !ok {
			body = []byte(`{"resource":[]}`)
		}
		path := "full/" + pack + ".json"
		s.files[path] = body
		cfg.IndexFile, cfg.IndexFileMD5, cfg.BaseURL = path, md(body), "zip/"
		var pcs []indexConfigRaw
		for from, pb := range patches[pack] {
			pp := "patch/" + pack + "/" + from + ".json"
			s.files[pp] = pb
			pcs = append(pcs, indexConfigRaw{Version: from, IndexFile: pp, IndexFileMD5: md(pb), BaseURL: "zip/"})
		}
		cfg.PatchConfig = pcs
		idx.ResourcePacks[pack] = cfg
	}
	s.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := strings.TrimPrefix(r.URL.Path, "/")
		if p == "index.json" {
			b, _ := json.Marshal(idx)
			_, _ = w.Write(b)
			return
		}
		if b, ok := s.files[p]; ok {
			_, _ = w.Write(b)
			return
		}
		w.WriteHeader(404)
	}))
	idx.CDNList = []cdnEntry{{URL: s.srv.URL + "/", P: 1}} // handler marshals idx per request, so this is served
	s.idx = idx
	old := gameIndexURLs
	gameIndexURLs = func() []string { return []string{s.srv.URL + "/index.json"} }
	t.Cleanup(func() { gameIndexURLs = old; s.srv.Close() })
	return s
}

func readFixture(t *testing.T, name string) []byte {
	b, err := os.ReadFile("testdata/v3/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// tinyIndexFile writes each entry into dir and returns a full indexFile body
// whose md5/size match the written files (so md5 filtering finds them present).
func tinyIndexFile(t *testing.T, dir string, entries map[string]string) []byte {
	t.Helper()
	var f indexFileRaw
	for rel, content := range entries {
		full := filepath.Join(dir, filepath.FromSlash(rel))
		_ = os.MkdirAll(filepath.Dir(full), 0o755)
		_ = os.WriteFile(full, []byte(content), 0o644)
		h := md5.Sum([]byte(content))
		f.Resource = append(f.Resource, manifestFileRaw{Dest: rel, MD5: hex.EncodeToString(h[:]), Size: int64(len(content))})
	}
	b, _ := json.Marshal(f)
	return b
}

func newV3TestProvider(t *testing.T, installDir string) *Provider {
	// The real game may be running on the dev machine; RunUpdate's process
	// guard must not see it.
	origProc := isProcessRunning
	isProcessRunning = func(string) bool { return false }
	t.Cleanup(func() { isProcessRunning = origProc })
	p := New(Settings{TempDir: t.TempDir()}, nil)
	p.SetResolvedPaths(map[core.GameID]string{"kurogames/wutheringwaves": installDir})
	return p
}

const gidWuwa = core.GameID("kurogames/wutheringwaves")

func TestCheckForUpdateV3_UpToDateIsEmpty(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, installStateFile), []byte(`{"version":"3.7.0","bundles":{"HD":{"version":"3.7.0","state":"","resourcePacks":["common","hd"]}}}`), 0o644)
	common := tinyIndexFile(t, dir, map[string]string{"Client/Content/Paks/a.pak": "a"})
	hd := tinyIndexFile(t, dir, map[string]string{"Client/Content/HD/b.pak": "b"})
	newV3Server(t, map[string][]byte{"common": common, "hd": hd}, nil)
	p := newV3TestProvider(t, dir)
	plan, err := p.CheckForUpdateWithProgress(context.Background(), gidWuwa, nil)
	if err != nil || len(plan.Files) != 0 || len(plan.PatchGroups) != 0 || plan.Bundle != "" {
		t.Fatalf("plan=%+v err=%v", plan, err)
	}
	if plan.ManifestETag == "" || plan.Version != "3.7.0" {
		t.Fatalf("token/version: %+v", plan)
	}
}

func TestCheckForUpdateV3_LegacyUsesCommonPatchAndHdFull(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, installStateFile), []byte(`{"version":"3.6.1","state":""}`), 0o644)
	hdFull := readFixture(t, "hd_indexFile.json")
	commonPatch := readFixture(t, "common_3.6.1_indexFile.json")
	newV3Server(t, map[string][]byte{"hd": hdFull}, map[string]map[string][]byte{"common": {"3.6.1": commonPatch}})
	p := newV3TestProvider(t, dir)
	plan, err := p.CheckForUpdateWithProgress(context.Background(), gidWuwa, nil)
	if err != nil {
		t.Fatal(err)
	}
	var hdFiles, ephemeral int
	for _, f := range plan.Files {
		if strings.HasPrefix(f.Path, "Client/Content/HD/") {
			hdFiles++
		}
		if f.Ephemeral {
			ephemeral++
		}
	}
	if hdFiles != 100 {
		t.Fatalf("hd full files = %d, want 100", hdFiles)
	}
	// 本機無任何 Paks → 1:1 group 全數 group-level fallback，krpdiff 不下載
	if ephemeral != 0 || len(plan.DeleteFiles) != 7 {
		t.Fatalf("ephemeral=%d delete=%d", ephemeral, len(plan.DeleteFiles))
	}
}

func TestCheckForUpdateV3_CommonTouchingBundleDirIsInvalid(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, installStateFile), []byte(`{"version":"3.7.0","bundles":{"HD":{"version":"3.7.0","state":"","resourcePacks":["common","hd"]}}}`), 0o644)
	bad := tinyIndexFile(t, t.TempDir(), map[string]string{"Client/Content/SD/x.pak": "x"})
	newV3Server(t, map[string][]byte{"common": bad}, nil)
	_, err := newV3TestProvider(t, dir).CheckForUpdateWithProgress(context.Background(), gidWuwa, nil)
	var ue *core.UpdateError
	if !errorsAs(err, &ue) || ue.Code != "manifest_invalid" {
		t.Fatalf("err=%v", err)
	}
}

func TestCheckForUpdateV3_NoRecord(t *testing.T) {
	dir := t.TempDir()
	newV3Server(t, nil, nil)
	_, err := newV3TestProvider(t, dir).CheckForUpdateWithProgress(context.Background(), gidWuwa, nil)
	var ue *core.UpdateError
	if !errorsAs(err, &ue) || ue.Code != "install_record_missing" {
		t.Fatalf("err=%v", err)
	}
}

func TestCheckForUpdateV3_ProgressMonotoneEndsAtTotal(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, installStateFile), []byte(`{"version":"3.7.0","bundles":{"HD":{"version":"3.7.0","state":"","resourcePacks":["common","hd"]}}}`), 0o644)
	common := tinyIndexFile(t, dir, map[string]string{"Client/Content/Paks/a.pak": "a", "Client/Content/Paks/b.pak": "b"})
	hd := tinyIndexFile(t, dir, map[string]string{"Client/Content/HD/c.pak": "c"})
	newV3Server(t, map[string][]byte{"common": common, "hd": hd}, nil)
	var calls [][2]int
	_, err := newV3TestProvider(t, dir).CheckForUpdateWithProgress(context.Background(), gidWuwa, func(d, tot int) { calls = append(calls, [2]int{d, tot}) })
	if err != nil || len(calls) == 0 {
		t.Fatalf("err=%v calls=%v", err, calls)
	}
	total := calls[0][1]
	prev := -1
	for _, c := range calls {
		if c[1] != total || c[0] < prev {
			t.Fatalf("non-monotone or total changed: %v", calls)
		}
		prev = c[0]
	}
	if last := calls[len(calls)-1]; last[0] != total || total != 3 {
		t.Fatalf("last=%v total=%d", last, total)
	}
}

func TestMkFetchFullPack_MD5MismatchIsManifestChanged(t *testing.T) { // Review Focus #3（fallback 路徑）
	srv := newV3Server(t, map[string][]byte{"common": []byte(`{"resource":[]}`)}, nil)
	idx := *srv.idx
	idx.ResourcePacks = map[string]indexConfigRaw{}
	for k, v := range srv.idx.ResourcePacks {
		idx.ResourcePacks[k] = v
	}
	c := idx.ResourcePacks["common"]
	c.IndexFileMD5 = "00000000000000000000000000000000"
	idx.ResourcePacks["common"] = c
	p := newV3TestProvider(t, t.TempDir())
	_, _, _, err := p.mkFetchFullPack(&idx, "common", srv.srv.URL+"/")(context.Background())
	var ue *core.UpdateError
	if !errorsAs(err, &ue) || ue.Code != "manifest_changed" {
		t.Fatalf("err=%v", err)
	}
}

func TestRunUpdate_TokenDriftIsManifestChanged(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, installStateFile), []byte(`{"version":"3.7.0","bundles":{"HD":{"version":"3.7.0","state":"","resourcePacks":["common","hd"]}}}`), 0o644)
	newV3Server(t, nil, nil)
	p := newV3TestProvider(t, dir)
	plan := core.UpdatePlan{GameID: gidWuwa, Kind: core.PlanUpdate, ManifestETag: "stale", Version: "3.7.0"}
	err := p.RunUpdate(context.Background(), plan, nil)
	var ue *core.UpdateError
	if !errorsAs(err, &ue) || ue.Code != "manifest_changed" {
		t.Fatalf("err=%v", err)
	}
}

func TestCheckVersion_V3(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, installStateFile), []byte(`{"version":"3.6.1","state":""}`), 0o644)
	newV3Server(t, nil, nil)
	p := newV3TestProvider(t, dir)
	vi, err := p.CheckVersion(context.Background(), gidWuwa)
	if err != nil || vi.Current != "3.6.1" || vi.Latest != "3.7.0" || vi.Predownload != nil {
		t.Fatalf("vi=%+v err=%v", vi, err)
	}
	if _, ok := p.loadCatalog(gidWuwa); !ok {
		t.Fatal("CheckVersion must write the catalog")
	}
}

func TestApply_WritesBackV3Record(t *testing.T) {
	// 以 0-file plan 走完 RunUpdate（download 無事可做、apply 只寫回）
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, installStateFile), []byte(`{"version":"3.6.1","state":""}`), 0o644)
	common := tinyIndexFile(t, dir, map[string]string{"Client/Content/Paks/a.pak": "a"})
	hd := tinyIndexFile(t, dir, map[string]string{"Client/Content/HD/b.pak": "b"})
	newV3Server(t, map[string][]byte{"common": common, "hd": hd}, nil)
	p := newV3TestProvider(t, dir)
	plan, err := p.CheckForUpdateWithProgress(context.Background(), gidWuwa, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.RunUpdate(context.Background(), plan, nil); err != nil {
		t.Fatal(err)
	}
	s, _ := readInstallState(filepath.Join(dir, installStateFile))
	if s.Legacy || s.packVersion("common") != "3.7.0" || s.Bundles["HD"].Version != "3.7.0" {
		t.Fatalf("state=%+v", s)
	}
}

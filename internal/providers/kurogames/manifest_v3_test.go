package kurogames

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"omnigate/internal/core"
)

func loadV3Index(t *testing.T) *gameIndexV3 {
	t.Helper()
	b, err := os.ReadFile("testdata/v3/game_index.json")
	if err != nil {
		t.Fatal(err)
	}
	var idx gameIndexV3
	if err := json.Unmarshal(b, &idx); err != nil {
		t.Fatal(err)
	}
	return &idx
}

func loadIndexFile(t *testing.T, name string) *indexFileRaw {
	t.Helper()
	b, err := os.ReadFile("testdata/v3/" + name)
	if err != nil {
		t.Fatal(err)
	}
	var f indexFileRaw
	if err := json.Unmarshal(b, &f); err != nil {
		t.Fatal(err)
	}
	return &f
}

func TestV3Index_ParsesFixture(t *testing.T) {
	idx := loadV3Index(t)
	if err := validateIndexV3(idx); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if idx.ResourcePacks["common"].Version != "3.7.0" {
		t.Fatalf("common version = %q", idx.ResourcePacks["common"].Version)
	}
	for _, b := range []string{"SD", "HD", "UHD"} {
		br, ok := idx.Bundles[b]
		if !ok || len(br.ResourcePacks) != 2 || br.ResourcePacks[1] != packOf(b) {
			t.Fatalf("bundle %s = %+v", b, br)
		}
	}
}

func TestBundleOptions_FixtureHD(t *testing.T) {
	idx := loadV3Index(t)
	opts := bundleOptions(idx.Bundles["HD"])
	var cmds []string
	for _, o := range opts {
		cmds = append(cmds, o.Cmd)
	}
	// commandList → -slno (default 0)；RHIOptionList → 跳過空 cmdOption，留 -dx11
	if strings.Join(cmds, ",") != "-slno,-dx11" {
		t.Fatalf("cmds = %v", cmds)
	}
	if opts[0].Default || opts[1].Default {
		t.Fatalf("defaults = %v/%v, want false/false", opts[0].Default, opts[1].Default)
	}
	if opts[0].Label["zh-TW"] == "" || opts[0].Label["zh-Hant"] != "" {
		t.Fatalf("label not mapped to zh-TW: %v", opts[0].Label)
	}
}

func TestBundleOptions_IgnoresKrqlvAndDuplicates(t *testing.T) {
	b := bundleRaw{Config: bundleConfigRaw{
		CommandSwitch: 1,
		CommandList: []extendCommandRaw{
			{Cmd: "-krqlv=hd", Default: 1},
			{Cmd: "-slno", Default: 1},
			{Cmd: "-slno", Default: 0},
		},
	}}
	opts := bundleOptions(b)
	if len(opts) != 1 || opts[0].Cmd != "-slno" || !opts[0].Default {
		t.Fatalf("opts = %+v", opts)
	}
}

func TestValidateIndexV3_Invalid(t *testing.T) {
	cases := map[string]func(*gameIndexV3){
		"missing common": func(i *gameIndexV3) { delete(i.ResourcePacks, "common") },
		"bad shape": func(i *gameIndexV3) {
			b := i.Bundles["SD"]
			b.ResourcePacks = []string{"common", "hd"}
			i.Bundles["SD"] = b
		},
		"bad name": func(i *gameIndexV3) { i.Bundles["hd-x"] = i.Bundles["HD"] },
	}
	for name, mut := range cases {
		t.Run(name, func(t *testing.T) {
			idx := loadV3Index(t)
			mut(idx)
			err := validateIndexV3(idx)
			var ue *core.UpdateError
			if !errorsAs(err, &ue) || ue.Code != "manifest_invalid" {
				t.Fatalf("err = %v, want manifest_invalid", err)
			}
		})
	}
}

func TestValidatePackIndexFile(t *testing.T) {
	idx := loadV3Index(t)
	if err := validatePackIndexFile(idx, "hd", loadIndexFile(t, "hd_indexFile.json")); err != nil {
		t.Fatalf("hd full: %v", err)
	}
	if err := validatePackIndexFile(idx, "common", loadIndexFile(t, "common_indexFile.json")); err != nil {
		t.Fatalf("common full: %v", err)
	}
	// patch indexFile：根目錄 krpdiff 檔名不得觸發（spec §2.3）
	if err := validatePackIndexFile(idx, "common", loadIndexFile(t, "common_3.6.1_indexFile.json")); err != nil {
		t.Fatalf("common patch: %v", err)
	}
	// quality pack 路徑越界
	bad := loadIndexFile(t, "sd_indexFile.json")
	bad.Resource[0].Dest = "Client/Content/HD/evil.pak"
	if err := validatePackIndexFile(idx, "sd", bad); err == nil {
		t.Fatal("sd with HD path: want manifest_invalid")
	}
	// common 撞 bundle 目錄（含 deleteFiles）
	c := loadIndexFile(t, "common_3.6.1_indexFile.json")
	c.DeleteFiles = append(c.DeleteFiles, "Client/Content/UHD/x.pak")
	if err := validatePackIndexFile(idx, "common", c); err == nil {
		t.Fatal("common deleting UHD path: want manifest_invalid")
	}
}

func TestPlanToken_StableAcrossUnrelatedChanges(t *testing.T) {
	idx := loadV3Index(t)
	a := planToken(idx, []string{"hd", "common"})
	b := planToken(idx, []string{"common", "hd"})
	if a != b || a == "" {
		t.Fatalf("order dependence: %q vs %q", a, b)
	}
	idx.Predownload = json.RawMessage(`{"x":1}`)
	idx.CDNList = nil
	if planToken(idx, []string{"common", "hd"}) != a {
		t.Fatal("token changed on predownload/cdnList change")
	}
	p := idx.ResourcePacks["hd"]
	p.IndexFileMD5 = "different"
	idx.ResourcePacks["hd"] = p
	if planToken(idx, []string{"common", "hd"}) == a {
		t.Fatal("token did not change on target pack md5 change")
	}
}

func TestFetchGameIndexV3_FallsBackToBackupHost(t *testing.T) {
	body, _ := os.ReadFile("testdata/v3/game_index.json")
	primary := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	defer primary.Close()
	backup := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
	defer backup.Close()
	old := gameIndexURLs
	gameIndexURLs = func() []string { return []string{primary.URL + "/index.json", backup.URL + "/index.json"} }
	defer func() { gameIndexURLs = old }()

	idx, err := fetchGameIndexV3(context.Background(), &http.Client{Timeout: 5 * time.Second})
	if err != nil || idx.ResourcePacks["common"].Version != "3.7.0" {
		t.Fatalf("idx=%v err=%v", idx, err)
	}
}

func TestFetchPackIndexFile_MD5Mismatch(t *testing.T) {
	body, _ := os.ReadFile("testdata/v3/hd_indexFile.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write(body) }))
	defer srv.Close()
	_, err := fetchPackIndexFile(context.Background(), srv.Client(), srv.URL+"/", indexConfigRaw{IndexFile: "x.json", IndexFileMD5: "00000000000000000000000000000000"})
	var ue *core.UpdateError
	if !errorsAs(err, &ue) || ue.Code != "manifest_changed" || !ue.Retryable {
		t.Fatalf("err = %v, want retryable manifest_changed", err)
	}
}

func TestCatalogFromIndex_SortedAndSized(t *testing.T) {
	idx := loadV3Index(t)
	cat := catalogFromIndex(idx, time.Unix(100, 0))
	var names []string
	for _, e := range cat.Bundles {
		names = append(names, e.Name)
	}
	if strings.Join(names, ",") != "UHD,HD,SD" {
		t.Fatalf("order = %v", names)
	}
	if cat.Bundles[1].PackSize != idx.ResourcePacks["hd"].Size || cat.CommonVersion != "3.7.0" {
		t.Fatalf("cat = %+v", cat)
	}
}

func errorsAs(err error, target **core.UpdateError) bool { return errors.As(err, target) }

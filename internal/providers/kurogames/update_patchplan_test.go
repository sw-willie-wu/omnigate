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
	"sort"
	"strings"
	"sync"
	"testing"

	"omnigate/internal/core"
)

// Standard patch-CDN vs full-CDN test fixtures — distinct so a fix-round-1
// finding C1 regression (fallback tasks accidentally URL'd via the patch
// base instead of the full base) shows up as a wrong-prefix assertion
// failure rather than silently matching either.
const (
	testPatchCDN     = "http://patch-cdn/"
	testPatchBaseURL = "/patch-base/"
	testFullCDN      = "http://full-cdn/"
	testFullBaseURL  = "/full-base/"
)

func md5Hex(b []byte) string {
	h := md5.Sum(b)
	return hex.EncodeToString(h[:])
}

func writeLocalFile(t *testing.T, dir, rel string, content []byte) {
	t.Helper()
	full := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir for %s: %v", rel, err)
	}
	if err := os.WriteFile(full, content, 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

func testProvider() *Provider {
	return New(Settings{}, nil)
}

// TestBuildPlan_ClassifiesGroups covers the three 1:1-group outcomes: local
// content == src → PatchGroup + Ephemeral krpdiff task; local == dst → group
// disappears entirely (already complete); local == neither → dst becomes a
// normal full-download FileTask.
func TestBuildPlan_ClassifiesGroups(t *testing.T) {
	srcA := []byte("SRC_A_CONTENT")
	dstA := []byte("DST_A_CONTENT_LONGER")
	diffA := []byte("DIFFBYTES_FOR_A_GROUP")

	srcB := []byte("SRC_B")
	dstB := []byte("DST_B_CONTENT")

	dstC := []byte("DST_C_CONTENT_XYZ12")
	localC := []byte("DST_C_CONTENT_ZYX21") // same length as dstC, different bytes/hash
	srcC := []byte("SRC_C_CONTENT_1234")    // never present locally

	idxFile := &indexFileRaw{
		ApplyTypes: []string{"group"},
		Resource: []manifestFileRaw{
			{Dest: "groupA.krpdiff", MD5: md5Hex(diffA), Size: int64(len(diffA))},
		},
		GroupInfos: []groupInfoRaw{
			{
				Dest:     "groupA.krpdiff",
				SrcFiles: []manifestFileRaw{{Dest: "fileA.dat", MD5: md5Hex(srcA), Size: int64(len(srcA))}},
				DstFiles: []manifestFileRaw{{Dest: "fileA.dat", MD5: md5Hex(dstA), Size: int64(len(dstA))}},
			},
			{
				Dest:     "groupB.krpdiff",
				SrcFiles: []manifestFileRaw{{Dest: "fileB.dat", MD5: md5Hex(srcB), Size: int64(len(srcB))}},
				DstFiles: []manifestFileRaw{{Dest: "fileB.dat", MD5: md5Hex(dstB), Size: int64(len(dstB))}},
			},
			{
				Dest:     "groupC.krpdiff",
				SrcFiles: []manifestFileRaw{{Dest: "fileC.dat", MD5: md5Hex(srcC), Size: int64(len(srcC))}},
				DstFiles: []manifestFileRaw{{Dest: "fileC.dat", MD5: md5Hex(dstC), Size: int64(len(dstC))}},
			},
		},
	}

	dir := t.TempDir()
	writeLocalFile(t, dir, "fileA.dat", srcA)
	writeLocalFile(t, dir, "fileB.dat", dstB)
	writeLocalFile(t, dir, "fileC.dat", localC)

	p := testProvider()
	files, groups, dels, peak, err := p.buildFileAndPatchPlan(context.Background(), dir, testPatchCDN, testPatchBaseURL, testFullCDN, testFullBaseURL, idxFile, nil, nil)
	if err != nil {
		t.Fatalf("buildFileAndPatchPlan: %v", err)
	}
	if len(dels) != 0 {
		t.Errorf("deleteFiles = %v, want empty", dels)
	}
	_ = peak

	if len(groups) != 1 {
		t.Fatalf("groups = %+v, want exactly 1 (group A)", groups)
	}
	g := groups[0]
	if g.DiffPath != "groupA.krpdiff" {
		t.Errorf("DiffPath = %q, want groupA.krpdiff", g.DiffPath)
	}
	if g.Src.Path != "fileA.dat" || g.Src.Hash != md5Hex(srcA) || g.Src.Size != int64(len(srcA)) {
		t.Errorf("Src = %+v, want {fileA.dat %s %d}", g.Src, md5Hex(srcA), len(srcA))
	}
	if g.Dst.Path != "fileA.dat" || g.Dst.Hash != md5Hex(dstA) || g.Dst.Size != int64(len(dstA)) {
		t.Errorf("Dst = %+v, want {fileA.dat %s %d}", g.Dst, md5Hex(dstA), len(dstA))
	}

	var ephemeral, fileCTask *core.FileTask
	for i := range files {
		f := &files[i]
		switch f.Path {
		case "groupA.krpdiff":
			ephemeral = f
		case "fileC.dat":
			fileCTask = f
		case "fileA.dat", "fileB.dat":
			t.Errorf("unexpected FileTask for %q (src file should never be downloaded directly)", f.Path)
		}
	}
	if ephemeral == nil {
		t.Fatal("no Ephemeral task for groupA.krpdiff")
	}
	if !ephemeral.Ephemeral {
		t.Error("groupA.krpdiff task Ephemeral = false, want true")
	}
	if ephemeral.Hash != md5Hex(diffA) || ephemeral.Size != int64(len(diffA)) {
		t.Errorf("groupA.krpdiff task = %+v, want hash=%s size=%d", ephemeral, md5Hex(diffA), len(diffA))
	}

	if fileCTask == nil {
		t.Fatal("no fallback FileTask for fileC.dat")
	}
	if fileCTask.Ephemeral {
		t.Error("fileC.dat task Ephemeral = true, want false (full download)")
	}
	if fileCTask.Hash != md5Hex(dstC) || fileCTask.Size != int64(len(dstC)) {
		t.Errorf("fileC.dat task = %+v, want hash=%s size=%d", fileCTask, md5Hex(dstC), len(dstC))
	}
	// C1: a 1:1-degraded dst fallback must be URL'd via the FULL config's
	// CDN+baseUrl, never the patch config's (dstFiles carry no fromFolder;
	// the patch base directory 404s on full-file content).
	wantURL := fileURL(testFullCDN, testFullBaseURL, manifestFileRaw{Dest: "fileC.dat"})
	if fileCTask.URL != wantURL {
		t.Errorf("fileC.dat task URL = %q, want %q (full config base)", fileCTask.URL, wantURL)
	}
	if strings.HasPrefix(fileCTask.URL, testPatchCDN) {
		t.Errorf("fileC.dat task URL = %q, must not use the patch CDN", fileCTask.URL)
	}
}

// TestBuildPlan_MultiGroupFallsBackToFullFiles covers a multi-file group
// (all src covered by dst) expanding directly into ordinary full-download
// FileTasks built from groupInfos.dstFiles metadata — including a dest with
// a space, to pin the fileURL %20-encoding path.
func TestBuildPlan_MultiGroupFallsBackToFullFiles(t *testing.T) {
	srcExe := []byte("OLD_EXE_BYTES")
	dstExe := []byte("NEW_EXE_BYTES_LONGER")
	srcOther := []byte("OLD_OTHER")
	dstOther := []byte("NEW_OTHER_BYTES")

	idxFile := &indexFileRaw{
		ApplyTypes: []string{"group"},
		GroupInfos: []groupInfoRaw{
			{
				Dest: "multi.krpdiff",
				SrcFiles: []manifestFileRaw{
					{Dest: "Wuthering Waves.exe", MD5: md5Hex(srcExe), Size: int64(len(srcExe))},
					{Dest: "other.dat", MD5: md5Hex(srcOther), Size: int64(len(srcOther))},
				},
				DstFiles: []manifestFileRaw{
					{Dest: "Wuthering Waves.exe", MD5: md5Hex(dstExe), Size: int64(len(dstExe))},
					{Dest: "other.dat", MD5: md5Hex(dstOther), Size: int64(len(dstOther))},
				},
			},
		},
	}

	dir := t.TempDir() // nothing local — both entries need download
	p := testProvider()
	files, groups, _, _, err := p.buildFileAndPatchPlan(context.Background(), dir, testPatchCDN, testPatchBaseURL, testFullCDN, testFullBaseURL, idxFile, nil, nil)
	if err != nil {
		t.Fatalf("buildFileAndPatchPlan: %v", err)
	}
	if len(groups) != 0 {
		t.Errorf("groups = %+v, want none (multi-group never forms a PatchGroup)", groups)
	}
	if len(files) != 2 {
		t.Fatalf("files = %+v, want 2 full-download tasks", files)
	}
	var exeTask *core.FileTask
	for i := range files {
		if files[i].Ephemeral {
			t.Errorf("file %+v: Ephemeral = true, want false", files[i])
		}
		// C1: multi-group dst fallback tasks must use the FULL config base,
		// never the patch base.
		if strings.HasPrefix(files[i].URL, testPatchCDN) {
			t.Errorf("file %+v: URL uses the patch CDN, want full CDN", files[i])
		}
		wantPrefix := testFullCDN + testFullBaseURL
		if !strings.HasPrefix(files[i].URL, wantPrefix) {
			t.Errorf("file %+v: URL = %q, want full CDN+base prefix %q", files[i], files[i].URL, wantPrefix)
		}
		if files[i].Path == "Wuthering Waves.exe" {
			exeTask = &files[i]
		}
	}
	if exeTask == nil {
		t.Fatal("no task for 'Wuthering Waves.exe'")
	}
	if !strings.Contains(exeTask.URL, "Wuthering%20Waves.exe") {
		t.Errorf("URL = %q, want %%20-encoded space", exeTask.URL)
	}
}

// TestBuildPlan_FallbackEntryAlreadyMatchesDstSkipped (M8): a group-level
// fallback dst entry whose local content already equals dst content must
// produce NO task at all — the merged hash batch still md5-filters
// fallback/general entries exactly like filterChangedFiles always has.
func TestBuildPlan_FallbackEntryAlreadyMatchesDstSkipped(t *testing.T) {
	alreadyDone := []byte("ALREADY_AT_TARGET_CONTENT")
	stillNeeded := []byte("NEW_CONTENT_NOT_LOCAL")

	idxFile := &indexFileRaw{
		ApplyTypes: []string{"group"},
		GroupInfos: []groupInfoRaw{
			{
				Dest: "multi.krpdiff",
				SrcFiles: []manifestFileRaw{
					{Dest: "done.dat", MD5: "old-done-hash", Size: 5},
					{Dest: "pending.dat", MD5: "old-pending-hash", Size: 5},
				},
				DstFiles: []manifestFileRaw{
					{Dest: "done.dat", MD5: md5Hex(alreadyDone), Size: int64(len(alreadyDone))},
					{Dest: "pending.dat", MD5: md5Hex(stillNeeded), Size: int64(len(stillNeeded))},
				},
			},
		},
	}

	dir := t.TempDir()
	writeLocalFile(t, dir, "done.dat", alreadyDone) // already at dst content
	// pending.dat intentionally absent locally.

	p := testProvider()
	files, _, _, _, err := p.buildFileAndPatchPlan(context.Background(), dir, testPatchCDN, testPatchBaseURL, testFullCDN, testFullBaseURL, idxFile, nil, nil)
	if err != nil {
		t.Fatalf("buildFileAndPatchPlan: %v", err)
	}
	for _, f := range files {
		if f.Path == "done.dat" {
			t.Errorf("done.dat produced a task (%+v) even though local already matches dst — md5 filter not applied", f)
		}
	}
	found := false
	for _, f := range files {
		if f.Path == "pending.dat" {
			found = true
		}
	}
	if !found {
		t.Error("pending.dat missing a task — should still need download")
	}
}

// TestBuildPlan_SrcOnlyGapEscalates: a multi-file group whose srcFiles
// contains an entry not covered by dstFiles or deleteFiles must escalate to
// whole-plan full fallback (the group-level fallback would silently leave
// that file stale otherwise).
func TestBuildPlan_SrcOnlyGapEscalates(t *testing.T) {
	idxFile := &indexFileRaw{
		ApplyTypes:  []string{"group"},
		DeleteFiles: []string{"some/other/file.dat"},
		GroupInfos: []groupInfoRaw{
			{
				Dest: "multi.krpdiff",
				SrcFiles: []manifestFileRaw{
					{Dest: "orphan.dat", MD5: "aaaa", Size: 5}, // NOT in dstFiles, NOT in deleteFiles
					{Dest: "covered.dat", MD5: "bbbb", Size: 5},
				},
				DstFiles: []manifestFileRaw{
					{Dest: "covered.dat", MD5: "cccc", Size: 5},
				},
			},
		},
	}

	fullResource := []manifestFileRaw{
		{Dest: "full1.dat", MD5: "deadbeef", Size: 10},
	}
	fullIdx := &indexFileRaw{Resource: fullResource}

	called := false
	fetchFull := func(ctx context.Context) (*indexFileRaw, string, string, error) {
		called = true
		return fullIdx, "http://full-cdn/", "/full-base/", nil
	}

	dir := t.TempDir()
	p := testProvider()
	files, groups, dels, _, err := p.buildFileAndPatchPlan(context.Background(), dir, testPatchCDN, testPatchBaseURL, testFullCDN, testFullBaseURL, idxFile, fetchFull, nil)
	if err != nil {
		t.Fatalf("buildFileAndPatchPlan: %v", err)
	}
	if !called {
		t.Fatal("fetchFull was not called — src-only gap should escalate to whole-plan fallback")
	}
	if len(groups) != 0 {
		t.Errorf("groups = %+v, want none (whole-plan fallback)", groups)
	}
	if len(files) != 1 || files[0].Path != "full1.dat" {
		t.Fatalf("files = %+v, want [full1.dat] from the FULL manifest", files)
	}
	if len(dels) != 1 || dels[0] != "some/other/file.dat" {
		t.Errorf("deleteFiles = %v, want patch manifest's deleteFiles carried through (src-only escalation trusts it)", dels)
	}
}

// TestBuildPlan_UnknownApplyTypes: an applyTypes value other than "group"
// means the patch manifest format isn't understood at all — whole-plan
// fallback, and deleteFiles is NOT carried through (nothing about the
// manifest is trusted).
func TestBuildPlan_UnknownApplyTypes(t *testing.T) {
	idxFile := &indexFileRaw{
		ApplyTypes:  []string{"group", "zip"},
		DeleteFiles: []string{"would/not/be/kept.dat"},
		GroupInfos: []groupInfoRaw{
			{Dest: "x.krpdiff", SrcFiles: []manifestFileRaw{{Dest: "a", MD5: "1", Size: 1}}, DstFiles: []manifestFileRaw{{Dest: "a", MD5: "2", Size: 1}}},
		},
	}
	fullIdx := &indexFileRaw{Resource: []manifestFileRaw{{Dest: "full1.dat", MD5: "deadbeef", Size: 10}}}
	called := false
	fetchFull := func(ctx context.Context) (*indexFileRaw, string, string, error) {
		called = true
		return fullIdx, "http://full-cdn/", "/full-base/", nil
	}

	dir := t.TempDir()
	p := testProvider()
	files, groups, dels, peak, err := p.buildFileAndPatchPlan(context.Background(), dir, testPatchCDN, testPatchBaseURL, testFullCDN, testFullBaseURL, idxFile, fetchFull, nil)
	if err != nil {
		t.Fatalf("buildFileAndPatchPlan: %v", err)
	}
	if !called {
		t.Fatal("fetchFull was not called for unknown applyTypes")
	}
	if len(groups) != 0 {
		t.Errorf("groups = %+v, want none", groups)
	}
	if peak != 0 {
		t.Errorf("peak = %d, want 0", peak)
	}
	if len(dels) != 0 {
		t.Errorf("deleteFiles = %v, want empty (unknown format trusts nothing)", dels)
	}
	if len(files) != 1 || files[0].Path != "full1.dat" {
		t.Fatalf("files = %+v, want [full1.dat] from the FULL manifest", files)
	}
}

// TestBuildPlan_SortsGroupsAndComputesPeak pins the Dst.Size-ascending sort
// order and the PeakTempBytes formula (spec §5) against a hand-computed
// value for 3 groups with distinct diff/dst sizes.
func TestBuildPlan_SortsGroupsAndComputesPeak(t *testing.T) {
	mk := func(name, srcRel string, dstSize, diffSize int64) (groupInfoRaw, manifestFileRaw, []byte) {
		srcContent := []byte("SRC_CONTENT_FOR_" + name)
		g := groupInfoRaw{
			Dest:     name + ".krpdiff",
			SrcFiles: []manifestFileRaw{{Dest: srcRel, MD5: md5Hex(srcContent), Size: int64(len(srcContent))}},
			DstFiles: []manifestFileRaw{{Dest: srcRel, MD5: "dst-hash-" + name, Size: dstSize}},
		}
		res := manifestFileRaw{Dest: name + ".krpdiff", MD5: "diff-hash-" + name, Size: diffSize}
		return g, res, srcContent
	}

	g1, r1, c1 := mk("g1", "f1.dat", 500, 100)
	g2, r2, c2 := mk("g2", "f2.dat", 200, 300)
	g3, r3, c3 := mk("g3", "f3.dat", 1000, 50)

	// Declared in DESCENDING Dst.Size order (g3=1000, g1=500, g2=200) —
	// deliberately NOT the ascending order the builder must produce (fix
	// round 1 finding I4). If the sort were dropped/broken, the peak
	// computation would run over this declaration order instead and yield a
	// DIFFERENT (larger) number — see the arithmetic below — so this also
	// pins that groups are genuinely re-sorted, not accidentally already
	// ascending.
	idxFile := &indexFileRaw{
		ApplyTypes: []string{"group"},
		Resource:   []manifestFileRaw{r3, r1, r2},
		GroupInfos: []groupInfoRaw{g3, g1, g2},
	}

	dir := t.TempDir()
	writeLocalFile(t, dir, "f1.dat", c1)
	writeLocalFile(t, dir, "f2.dat", c2)
	writeLocalFile(t, dir, "f3.dat", c3)

	p := testProvider()
	_, groups, _, peak, err := p.buildFileAndPatchPlan(context.Background(), dir, testPatchCDN, testPatchBaseURL, testFullCDN, testFullBaseURL, idxFile, nil, nil)
	if err != nil {
		t.Fatalf("buildFileAndPatchPlan: %v", err)
	}
	if len(groups) != 3 {
		t.Fatalf("groups = %+v, want 3", groups)
	}
	if !sort.SliceIsSorted(groups, func(i, j int) bool { return groups[i].Dst.Size < groups[j].Dst.Size }) {
		t.Errorf("groups not sorted by Dst.Size ascending: %+v", groups)
	}
	wantOrder := []string{"g2.krpdiff", "g1.krpdiff", "g3.krpdiff"}
	for i, w := range wantOrder {
		if groups[i].DiffPath != w {
			t.Errorf("groups[%d].DiffPath = %q, want %q", i, groups[i].DiffPath, w)
		}
	}
	// hand-computed per spec §5, CORRECT (ascending Dst.Size) order g2,g1,g3:
	// remaining = 100+300+50 = 450
	// g2(dst200,diff300): peak=max(0,450+200)=650; remaining=450-300=150
	// g1(dst500,diff100): peak=max(650,150+500)=650; remaining=150-100=50
	// g3(dst1000,diff50): peak=max(650,50+1000)=1050; remaining=50-50=0
	// => wantPeak = 1050.
	//
	// Contrast with the DECLARED (descending) order g3,g1,g2 — what a
	// broken/dropped sort would compute instead (fix round 1 finding I4):
	// remaining = 450 (same sum, order-independent)
	// g3(dst1000,diff50): peak=max(0,450+1000)=1450; remaining=450-50=400
	// g1(dst500,diff100): peak=max(1450,400+500)=1450; remaining=400-100=300
	// g2(dst200,diff300): peak=max(1450,300+200)=1450; remaining=300-300=0
	// => unsorted peak would be 1450 — different from wantPeak, so this test
	// fails if the ascending sort is ever dropped/broken, not just if the
	// arithmetic itself is wrong.
	const wantPeak = int64(1050)
	if peak != wantPeak {
		t.Errorf("peak = %d, want %d", peak, wantPeak)
	}
}

// TestBuildPlan_MergedProgressMonotonic asserts the single merged hash batch
// reports strictly increasing `done` and a constant `total` — no reset
// between the general-resource-file sub-batch and the 1:1-candidate
// sub-batch (spec §2 合併 total).
func TestBuildPlan_MergedProgressMonotonic(t *testing.T) {
	genContent := []byte("GENERAL_FILE_CONTENT")
	src1 := []byte("SRC_ONE")
	src2 := []byte("SRC_TWO")

	idxFile := &indexFileRaw{
		ApplyTypes: []string{"group"},
		Resource: []manifestFileRaw{
			{Dest: "general.dat", MD5: md5Hex(genContent), Size: int64(len(genContent))},
		},
		GroupInfos: []groupInfoRaw{
			{
				Dest:     "g1.krpdiff",
				SrcFiles: []manifestFileRaw{{Dest: "f1.dat", MD5: md5Hex(src1), Size: int64(len(src1))}},
				DstFiles: []manifestFileRaw{{Dest: "f1.dat", MD5: "dst1", Size: 999}},
			},
			{
				Dest:     "g2.krpdiff",
				SrcFiles: []manifestFileRaw{{Dest: "f2.dat", MD5: md5Hex(src2), Size: int64(len(src2))}},
				DstFiles: []manifestFileRaw{{Dest: "f2.dat", MD5: "dst2", Size: 999}},
			},
		},
	}

	dir := t.TempDir()
	writeLocalFile(t, dir, "general.dat", genContent)
	writeLocalFile(t, dir, "f1.dat", src1)
	writeLocalFile(t, dir, "f2.dat", src2)

	var mu sync.Mutex
	var doneSeq []int
	var totalSeq []int
	onProgress := func(done, total int) {
		mu.Lock()
		defer mu.Unlock()
		doneSeq = append(doneSeq, done)
		totalSeq = append(totalSeq, total)
	}

	p := testProvider()
	_, _, _, _, err := p.buildFileAndPatchPlan(context.Background(), dir, testPatchCDN, testPatchBaseURL, testFullCDN, testFullBaseURL, idxFile, nil, onProgress)
	if err != nil {
		t.Fatalf("buildFileAndPatchPlan: %v", err)
	}

	if len(doneSeq) != 3 {
		t.Fatalf("progress calls = %d, want 3 (1 general + 2 candidates)", len(doneSeq))
	}
	for i := 1; i < len(doneSeq); i++ {
		if doneSeq[i] <= doneSeq[i-1] {
			t.Errorf("done not strictly increasing at %d: %v", i, doneSeq)
			break
		}
	}
	for i, tot := range totalSeq {
		if tot != 3 {
			t.Errorf("total[%d] = %d, want 3 (constant)", i, tot)
		}
	}
}

// TestCheckForPredownload_FullFallbackUsesPredlConfig pins plan gate B1: a
// predownload patch manifest that escalates to whole-plan fallback must
// fetch the FULL indexFile from the PREDOWNLOAD config
// (idx.Predownload.Config — the top-level config, NOT its PatchConfig[]
// sub-entry used for the patch manifest itself), never the default
// (live-version) config's full manifest — otherwise the fallback plan mixes
// predl-target files with live-version files.
func TestCheckForPredownload_FullFallbackUsesPredlConfig(t *testing.T) {
	const gid = core.GameID("kurogames/wutheringwaves")

	// idx.Predownload.Config is the FULL predl-target config (indexFile =
	// predl/full/...); its PatchConfig[] holds a from-3.5.3 patch manifest
	// at a distinct path. Local install version is 3.5.3 (planted below) so
	// CheckForPredownload's own fetch uses the PATCH path, while
	// mkFetchFull(idx.Predownload.Config, ...) — bound to the top-level
	// config — must resolve to the FULL predl path.
	index := `{
	  "default":{"version":"3.5.3","cdnList":[{"url":"PLACEHOLDER/","P":0}],"config":{"version":"3.5.3","indexFile":"default/full/indexFile.json","baseUrl":"default/zip/"}},
	  "predownload":{"version":"3.6.0","cdnList":[{"url":"PLACEHOLDER/","P":0}],"config":{
	    "version":"3.6.0","indexFile":"predl/full/indexFile.json","baseUrl":"predl/zip/","patchType":"patch",
	    "patchConfig":[{"version":"3.5.3","indexFile":"predl/patch/indexFile.json","baseUrl":"predl/patchzip/"}]
	  }},
	  "predownloadSwitch":1
	}`
	// Patch manifest with an unknown applyTypes value → forces whole-plan fallback.
	patchManifest := `{"applyTypes":["group","zip"],"resource":[]}`
	predlFullManifest := `{"resource":[{"dest":"predl-full.pak","md5":"abc","size":42}]}`

	var defaultFullHit, predlFullHit bool
	mux := http.NewServeMux()
	var srv *httptest.Server
	mux.HandleFunc("/index.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(strings.ReplaceAll(index, "PLACEHOLDER", srv.URL)))
	})
	mux.HandleFunc("/predl/patch/indexFile.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Write([]byte(patchManifest))
	})
	mux.HandleFunc("/predl/full/indexFile.json", func(w http.ResponseWriter, _ *http.Request) {
		predlFullHit = true
		w.Write([]byte(predlFullManifest))
	})
	mux.HandleFunc("/default/full/indexFile.json", func(w http.ResponseWriter, _ *http.Request) {
		defaultFullHit = true
		w.Write([]byte(`{"resource":[]}`))
	})
	srv = httptest.NewServer(mux)
	defer srv.Close()

	orig := indexJSONURL
	indexJSONURL = func() string { return srv.URL + "/index.json" }
	defer func() { indexJSONURL = orig }()

	installDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(installDir, "launcherDownloadConfig.json"), []byte(`{"version":"3.5.3"}`), 0o644); err != nil {
		t.Fatalf("plant launcherDownloadConfig.json: %v", err)
	}

	p := New(Settings{}, nil)
	p.httpClient = srv.Client()
	p.SetResolvedPaths(map[core.GameID]string{gid: installDir})

	plan, err := p.CheckForPredownload(context.Background(), gid, nil)
	if err != nil {
		t.Fatalf("CheckForPredownload: %v", err)
	}
	if defaultFullHit {
		t.Error("default (live) full indexFile was fetched — plan gate B1 violated")
	}
	if !predlFullHit {
		t.Fatal("predl full indexFile was never fetched — expected whole-plan fallback to use idx.Predownload.Config")
	}
	if len(plan.Files) != 1 || plan.Files[0].Path != "predl-full.pak" {
		t.Errorf("plan.Files = %+v, want [predl-full.pak] from the predl FULL manifest", plan.Files)
	}
}

// TestBuildPlan_OrphanKrpdiffNeverBecomesPlainTask (C2 — 2026-08-20 incident
// replay guard): a resource entry named *.krpdiff that is NOT consumed as
// any group's own diff (manifest drift / orphan) must never surface as a
// plain (non-Ephemeral) FileTask — that's exactly the incident: a krpdiff
// renamed straight into the game dir as if it were a real game file. The
// fix excludes such entries from the plan entirely (with a Warn log)
// instead of guessing.
func TestBuildPlan_OrphanKrpdiffNeverBecomesPlainTask(t *testing.T) {
	srcReal := []byte("SRC_REAL_CONTENT")
	dstReal := []byte("DST_REAL_CONTENT_LONGER")
	diffReal := []byte("DIFF_BYTES_REAL")

	idxFile := &indexFileRaw{
		ApplyTypes: []string{"group"},
		Resource: []manifestFileRaw{
			{Dest: "real.krpdiff", MD5: md5Hex(diffReal), Size: int64(len(diffReal))},
			// orphan.krpdiff: present in resource[], but NOT referenced by
			// any GroupInfos entry below — manifest drift.
			{Dest: "orphan.krpdiff", MD5: "orphan-hash", Size: 12345},
		},
		GroupInfos: []groupInfoRaw{
			{
				Dest:     "real.krpdiff",
				SrcFiles: []manifestFileRaw{{Dest: "real.dat", MD5: md5Hex(srcReal), Size: int64(len(srcReal))}},
				DstFiles: []manifestFileRaw{{Dest: "real.dat", MD5: md5Hex(dstReal), Size: int64(len(dstReal))}},
			},
		},
	}

	dir := t.TempDir()
	writeLocalFile(t, dir, "real.dat", srcReal)
	// orphan.krpdiff intentionally not planted locally — its absence must
	// not matter: it's excluded before ever reaching the hash batch.

	p := testProvider()
	files, groups, _, _, err := p.buildFileAndPatchPlan(context.Background(), dir, testPatchCDN, testPatchBaseURL, testFullCDN, testFullBaseURL, idxFile, nil, nil)
	if err != nil {
		t.Fatalf("buildFileAndPatchPlan: %v", err)
	}
	if len(groups) != 1 {
		t.Fatalf("groups = %+v, want 1 (real.krpdiff)", groups)
	}

	var sawOrphan, sawRealEphemeral bool
	for _, f := range files {
		if f.Path == "orphan.krpdiff" {
			sawOrphan = true
		}
		if strings.HasSuffix(f.Path, ".krpdiff") && !f.Ephemeral {
			t.Errorf("task %+v: a .krpdiff path must always be Ephemeral=true", f)
		}
		if f.Path == "real.krpdiff" && f.Ephemeral {
			sawRealEphemeral = true
		}
	}
	if sawOrphan {
		t.Error("orphan.krpdiff produced a task at all — must be excluded entirely (manifest drift)")
	}
	if !sawRealEphemeral {
		t.Error("real.krpdiff never produced an Ephemeral task")
	}
}

// TestBuildPlan_MissingResourceForGroupDegradesToFullDownload (I6): a 1:1
// group whose local content matches src (would normally form a PatchGroup)
// but whose Dest has no matching resource[] entry (manifest drift — no
// krpdiff to download) must degrade to an ordinary full dst download
// instead of silently vanishing or panicking. The resulting task must use
// the FULL config base (C1 applies here too — it's still a dst fallback).
func TestBuildPlan_MissingResourceForGroupDegradesToFullDownload(t *testing.T) {
	src := []byte("SRC_CONTENT_FOR_MISSING_RESOURCE")
	dst := []byte("DST_CONTENT_LONGER_VERSION")

	idxFile := &indexFileRaw{
		ApplyTypes: []string{"group"},
		// Resource intentionally omits "missing.krpdiff" entirely.
		GroupInfos: []groupInfoRaw{
			{
				Dest:     "missing.krpdiff",
				SrcFiles: []manifestFileRaw{{Dest: "file.dat", MD5: md5Hex(src), Size: int64(len(src))}},
				DstFiles: []manifestFileRaw{{Dest: "file.dat", MD5: md5Hex(dst), Size: int64(len(dst))}},
			},
		},
	}

	dir := t.TempDir()
	writeLocalFile(t, dir, "file.dat", src)

	p := testProvider()
	files, groups, _, _, err := p.buildFileAndPatchPlan(context.Background(), dir, testPatchCDN, testPatchBaseURL, testFullCDN, testFullBaseURL, idxFile, nil, nil)
	if err != nil {
		t.Fatalf("buildFileAndPatchPlan: %v", err)
	}
	if len(groups) != 0 {
		t.Errorf("groups = %+v, want none (no resource entry → no PatchGroup)", groups)
	}
	if len(files) != 1 {
		t.Fatalf("files = %+v, want exactly 1 dst full-download task", files)
	}
	f := files[0]
	if f.Path != "file.dat" {
		t.Errorf("Path = %q, want file.dat", f.Path)
	}
	if f.Ephemeral {
		t.Error("Ephemeral = true, want false (full download, not a diff)")
	}
	if f.Hash != md5Hex(dst) || f.Size != int64(len(dst)) {
		t.Errorf("task = %+v, want hash=%s size=%d", f, md5Hex(dst), len(dst))
	}
	wantURL := fileURL(testFullCDN, testFullBaseURL, manifestFileRaw{Dest: "file.dat"})
	if f.URL != wantURL {
		t.Errorf("URL = %q, want %q (full config base — C1 applies to this degrade path too)", f.URL, wantURL)
	}
}

// TestBuildPlan_RealFixtureStructuralClassification (M7, overlaps I3): runs
// the builder over the REAL 3.5.3→3.6.0 trimmed fixture
// (testdata/indexfile_353_to_360_trimmed.json) end-to-end. The fixture's 3
// real 1:1 groups (pakchunk0/1/103) carry multi-GB recorded sizes/hashes
// for actual game data we don't have and can't practically hash in a unit
// test — their src/dst size+md5 are overridden to small synthetic
// local-testable content. Everything else — group arity, Dest
// cross-references into resource[], the real multi-group's shape (4 src /
// 4 dst, non-bijective), and deleteFiles coverage of the multi-group's
// src-only entries — is exactly the real fixture data, so this exercises
// the real routing/classification structure, including I3 (src-only
// entries ARE covered by deleteFiles → no escalation).
func TestBuildPlan_RealFixtureStructuralClassification(t *testing.T) {
	data, err := os.ReadFile("testdata/indexfile_353_to_360_trimmed.json")
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}
	var idxFile indexFileRaw
	if err := json.Unmarshal(data, &idxFile); err != nil {
		t.Fatalf("parse fixture: %v", err)
	}

	dir := t.TempDir()

	oneToOne := map[string][]byte{
		"Client/Content/Paks/pakchunk0-WindowsNoEditor.pak":   []byte("SYN_SRC_pakchunk0"),
		"Client/Content/Paks/pakchunk1-WindowsNoEditor.pak":   []byte("SYN_SRC_pakchunk1"),
		"Client/Content/Paks/pakchunk103-WindowsNoEditor.pak": []byte("SYN_SRC_pakchunk103"),
	}
	patched := 0
	for gi := range idxFile.GroupInfos {
		g := &idxFile.GroupInfos[gi]
		if len(g.SrcFiles) != 1 || len(g.DstFiles) != 1 || g.SrcFiles[0].Dest != g.DstFiles[0].Dest {
			continue // the real multi-group (group_41) — left completely alone
		}
		content, ok := oneToOne[g.SrcFiles[0].Dest]
		if !ok {
			t.Fatalf("fixture shape changed: unexpected 1:1 group for %q", g.SrcFiles[0].Dest)
		}
		g.SrcFiles[0].Size = int64(len(content))
		g.SrcFiles[0].MD5 = md5Hex(content)
		g.DstFiles[0].MD5 = "dst-md5-" + g.Dest // never matched locally; dst content not planted
		writeLocalFile(t, dir, g.SrcFiles[0].Dest, content)
		patched++
	}
	if patched != 3 {
		t.Fatalf("patched %d one-to-one groups, want 3 — fixture shape changed?", patched)
	}

	p := testProvider()
	files, groups, dels, _, err := p.buildFileAndPatchPlan(context.Background(), dir, testPatchCDN, testPatchBaseURL, testFullCDN, testFullBaseURL, &idxFile, nil, nil)
	if err != nil {
		// A nil fetchFull here means: if hasUncoveredSrcOnly ever ignored
		// deleteFiles (I3), the multi-group's src-only entries (real
		// fixture: pakchunk18/73 .sig, covered by the real deleteFiles)
		// would incorrectly escalate to whole-plan fallback, which needs
		// fetchFull (nil) and returns an error — so "no error" is itself
		// part of the I3 assertion.
		t.Fatalf("buildFileAndPatchPlan: %v (src-only entries covered by deleteFiles must NOT escalate)", err)
	}

	if len(dels) != len(idxFile.DeleteFiles) {
		t.Errorf("deleteFiles = %v, want the fixture's %v (no escalation happened)", dels, idxFile.DeleteFiles)
	}

	if len(groups) != 3 {
		t.Fatalf("groups = %+v, want 3 (the fixture's 3 real 1:1 groups)", groups)
	}
	wantDiffPaths := map[string]bool{
		"3.5.3_3.6.0_group_0_1786181737304.krpdiff": true,
		"3.5.3_3.6.0_group_1_1786182955371.krpdiff": true,
		"3.5.3_3.6.0_group_3_1786183044329.krpdiff": true,
	}
	for _, g := range groups {
		if !wantDiffPaths[g.DiffPath] {
			t.Errorf("unexpected PatchGroup DiffPath %q", g.DiffPath)
		}
	}

	// The real multi-group (group_41) expands to its 4 dstFiles as fallback
	// tasks — none planted locally, so all 4 need download, URL'd via the
	// FULL config base (C1).
	multiDstPaths := map[string]bool{
		"Client/Content/Paks/pakchunk112-WindowsNoEditor.sig": true,
		"Client/Content/Paks/pakchunk43-WindowsNoEditor.sig":  true,
		"ClearThirdParty.exe":                                 true,
		"Client/Binaries/Win64/AntiCheatExpert/ACE-BASE.sys":  true,
	}
	foundMultiDst := 0
	wantPrefix := testFullCDN + testFullBaseURL
	for _, f := range files {
		if !multiDstPaths[f.Path] {
			continue
		}
		foundMultiDst++
		if f.Ephemeral {
			t.Errorf("multi-group dst task %q: Ephemeral = true, want false", f.Path)
		}
		if !strings.HasPrefix(f.URL, wantPrefix) {
			t.Errorf("multi-group dst task %q URL = %q, want full-config prefix %q", f.Path, f.URL, wantPrefix)
		}
	}
	if foundMultiDst != 4 {
		t.Errorf("multi-group dst tasks found = %d, want 4", foundMultiDst)
	}

	// C2: the real fixture's orphan krpdiff resource entry ("...group_2...",
	// not referenced by any GroupInfos entry) must never surface as a task.
	for _, f := range files {
		if f.Path == "3.5.3_3.6.0_group_2_1786183027972.krpdiff" {
			t.Errorf("orphan krpdiff resource entry produced a task: %+v", f)
		}
		if strings.HasSuffix(f.Path, ".krpdiff") && !f.Ephemeral {
			t.Errorf("task %+v: a .krpdiff path must always be Ephemeral=true", f)
		}
	}

	// General resource file (the exe; carries fromFolder) still uses the
	// PATCH cdn — C1 only applies to group-fallback dst files.
	for _, f := range files {
		if f.Path == "Client/Binaries/Win64/Client-Win64-Shipping.exe" && !strings.HasPrefix(f.URL, testPatchCDN) {
			t.Errorf("general resource file URL = %q, want patch CDN prefix %q", f.URL, testPatchCDN)
		}
	}
}

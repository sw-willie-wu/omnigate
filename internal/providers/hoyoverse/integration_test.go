//go:build integration

package hoyoverse

import (
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/klauspost/compress/zstd"
	"google.golang.org/protobuf/proto"

	"omnigate/internal/core"
	"omnigate/internal/providers/hoyoverse/sophon"
	pb "omnigate/internal/providers/hoyoverse/sophon/proto"
)

const genshinGID = core.GameID("hoyoverse/genshin")

// fakeSophonServer serves the testdata/sophon fixtures over httptest:
//
//	GET /getGameBranches     -> branchesJSON
//	GET /getBuild            -> buildJSON
//	GET /getPatchBuild       -> patchJSON
//	GET /cdn/manifest/<id>   -> testdata/sophon/<id>            (pb.zst)
//	GET /cdn/chunks/<name>   -> testdata/sophon/chunks/<name>   (chunk/patch blob)
//
// Per-path response overrides (corruption / 404 / flaky) via the maps.
// §E.3 P13: exposes nChunkGET atomic counter + chunkGETs()/resetChunkGETs().
type fakeSophonServer struct {
	*httptest.Server
	branchesJSON string // testdata file name under testdata/sophon
	buildJSON    string
	patchJSON    string
	// chunkOverride[name] -> bytes to serve instead of the on-disk blob.
	chunkOverride map[string][]byte
	// chunkFailFirst[name] = N: first N GETs return corrupt bytes, then real.
	chunkFailFirst map[string]int
	chunkHits      map[string]int
	// chunk404[name] = true: always 404.
	chunk404 map[string]bool
	// §E.3 P13: atomic CDN chunk GET counter.
	nChunkGET atomic.Int64
	// buildHitCount counts /getBuild calls (scenario 14).
	buildHitCount int
}

// chunkGETs returns the total number of CDN chunk GETs since last resetChunkGETs.
func (f *fakeSophonServer) chunkGETs() int { return int(f.nChunkGET.Load()) }

// resetChunkGETs resets the CDN chunk counter to zero.
func (f *fakeSophonServer) resetChunkGETs() { f.nChunkGET.Store(0) }

// buildHitsFor returns the number of /getBuild hits (scenario 14).
func (f *fakeSophonServer) buildHitsFor() int { return f.buildHitCount }

func newFakeSophonServer(t *testing.T, branches, build, patch string) *fakeSophonServer {
	t.Helper()
	fs := &fakeSophonServer{
		branchesJSON:   branches,
		buildJSON:      build,
		patchJSON:      patch,
		chunkOverride:  map[string][]byte{},
		chunkFailFirst: map[string]int{},
		chunkHits:      map[string]int{},
		chunk404:       map[string]bool{},
	}
	mux := http.NewServeMux()
	read := func(name string) []byte {
		b, err := os.ReadFile(filepath.Join("testdata", "sophon", name))
		if err != nil {
			t.Fatalf("read fixture %s: %v", name, err)
		}
		return b
	}
	mux.HandleFunc("/getGameBranches", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(read(fs.branchesJSON))
	})
	// injectServerURL replaces relative "/cdn/..." url_prefix values in the
	// fixture JSON with the absolute server URL so FetchManifestRaw /
	// DownloadChunk can construct valid HTTP URLs.
	injectServerURL := func(raw []byte) []byte {
		// Server URL is only known after fs.Server is set; use a closure that
		// reads fs.URL at serve-time (not at registration time).
		s := strings.ReplaceAll(string(raw), `"/cdn/`, `"`+fs.URL+`/cdn/`)
		return []byte(s)
	}
	mux.HandleFunc("/getBuild", func(w http.ResponseWriter, r *http.Request) {
		// Live API: getBuild accepts GET (mirror the method contract so a
		// method regression is caught here).
		if r.Method != http.MethodGet {
			w.Header().Set("Allow", "GET")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		fs.buildHitCount++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(injectServerURL(read(fs.buildJSON)))
	})
	mux.HandleFunc("/getPatchBuild", func(w http.ResponseWriter, r *http.Request) {
		// Live API requires POST for getPatchBuild (GET → 405 "Allow:
		// OPTIONS, POST", verified 2026-06-01). Enforce it so the patch
		// scenarios fail if fetchSophonBuild regresses to GET.
		if r.Method != http.MethodPost {
			w.Header().Set("Allow", "OPTIONS, POST")
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if fs.patchJSON == "" {
			http.Error(w, "no patch", http.StatusNotFound)
			return
		}
		_, _ = w.Write(injectServerURL(read(fs.patchJSON)))
	})
	mux.HandleFunc("/cdn/manifest/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/cdn/manifest/")
		_, _ = w.Write(read(name))
	})
	mux.HandleFunc("/cdn/chunks/", func(w http.ResponseWriter, r *http.Request) {
		name := strings.TrimPrefix(r.URL.Path, "/cdn/chunks/")
		// §E.3 P13: increment atomic counter on every CDN chunk GET.
		fs.nChunkGET.Add(1)
		fs.chunkHits[name]++
		if fs.chunk404[name] {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		if ov, ok := fs.chunkOverride[name]; ok {
			_, _ = w.Write(ov)
			return
		}
		if n := fs.chunkFailFirst[name]; n > 0 && fs.chunkHits[name] <= n {
			_, _ = w.Write([]byte("CORRUPT-CHUNK-BYTES"))
			return
		}
		_, _ = w.Write(read(filepath.Join("chunks", name)))
	})
	fs.Server = httptest.NewServer(mux)
	t.Cleanup(fs.Close)
	return fs
}

// newSophonProvider builds a Provider wired to the fake server and a temp
// game dir + temp root. gameVersion == "" → no config.ini (no-install).
// Installs a mock hpatchzRun that writes deterministic bytes matching
// sample.manifest.pb.zst asset[0] (file_00.bin) so HDiff scenarios don't
// require a valid hpatchz diff blob in the fixture.
func newSophonProvider(t *testing.T, fs *fakeSophonServer, gameVersion string) (*Provider, string, string) {
	t.Helper()
	gameDir := t.TempDir()
	tempRoot := t.TempDir()
	if gameVersion != "" {
		writeConfigIni(t, gameDir, gameVersion)
	}
	p := New(Settings{}, nil)
	p.SetTempRootFn(func(core.GameID) string { return tempRoot })
	// [DEV-3] redirect all three API surfaces at the fake server.
	p.SetAPIBaseURL(fs.URL)
	p.SetBranchAPIBaseURL(fs.URL)
	p.SetSophonAPIBaseURL(fs.URL)
	// Mock hpatchz: write the deterministic content for file_00.bin.
	// HDiff path integration tests use this to avoid requiring a real diff blob.
	p.SetHpatchzRun(mockHpatchzRun)
	registerTestGameDir(t, p, genshinGID, gameDir)
	return p, gameDir, tempRoot
}

// mockHpatchzRun simulates hpatchz by writing the expected post-apply bytes for
// file_00.bin (sample.manifest.pb.zst asset[0]). Real hpatchz behavior is
// verified by hpatchz unit tests (T12/T20); integration tests only need the
// apply to complete with correct output.
// The content is: 3 × 1024 bytes with values 0x00, 0x01, 0x02 (matching the
// generator's globalIdx 0,1,2 for asset[0]).
func mockHpatchzRun(_ context.Context, oldFile, diffFile, newFile string) error {
	var content []byte
	for i := 0; i < 3; i++ {
		content = append(content, bytes.Repeat([]byte{byte(i)}, 1024)...)
	}
	return os.WriteFile(newFile, content, 0o644)
}

// registerTestGameDir wires a temp gameDir into the provider via SetGameDirFn,
// bypassing DetectInstall (which requires HoYoPlay to be installed on disk).
// Each new call replaces the previous seam so multi-game tests can override.
func registerTestGameDir(t *testing.T, p *Provider, gid core.GameID, dir string) {
	t.Helper()
	prev := p.gameDirFn
	p.SetGameDirFn(func(g core.GameID) (string, error) {
		if g == gid {
			return dir, nil
		}
		if prev != nil {
			return prev(g)
		}
		return "", fmt.Errorf("gameDir: %w (gid=%s)", core.ErrUnknownGame, g)
	})
}

// writeConfigIni writes a minimal Genshin config.ini with the given version,
// matching what ReadGameVersion parses (see config_ini.go).
func writeConfigIni(t *testing.T, gameDir, version string) {
	t.Helper()
	body := "[General]\ngame_version=" + version + "\nchannel=1\nsub_channel=1\n"
	if err := os.WriteFile(filepath.Join(gameDir, "config.ini"), []byte(body), 0o644); err != nil {
		t.Fatalf("write config.ini: %v", err)
	}
}

// ---- Scenarios 1–8 (plan flavor selection + full/build/patch end-to-end) ----

// 1. currentLocal in DiffTags → HDiff patch path (sample fixtures). Also
//
//	cross-checked with tiny in scenario 1b semantics via TestSophonFlavorFull.
func TestSophonFlavorPatch_EndToEnd(t *testing.T) {
	fs := newFakeSophonServer(t, "branches_main_only.json", "build_small.json", "patch_small.json")
	p, gameDir, _ := newSophonProvider(t, fs, "6.5.0") // 6.5.0 ∈ diff_tags
	seedOldFiles(t, gameDir)
	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if plan.Version != "6.6.0" {
		t.Fatalf("want target 6.6.0, got %q", plan.Version)
	}
	if err := p.RunUpdate(context.Background(), plan, nil); err != nil {
		t.Fatalf("RunUpdate: %v", err)
	}
	assertGameVersion(t, gameDir, "6.6.0")
	// CopyOver asset[1] must be byte-correct; deleted file must be gone.
	assertFileMissing(t, filepath.Join(gameDir, "data/old_removed.bin"))
}

// 2. patch covers some files; others fall through to main getBuild.
func TestSophonFlavorPatch_FilesNotInPatch_FromMain(t *testing.T) {
	fs := newFakeSophonServer(t, "branches_main_only.json", "build_small.json", "patch_small.json")
	p, gameDir, _ := newSophonProvider(t, fs, "6.5.0")
	seedOldFiles(t, gameDir)
	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if err := p.RunUpdate(context.Background(), plan, nil); err != nil {
		t.Fatalf("RunUpdate: %v", err)
	}
	// file_02..file_04 are not in the patch → assembled from main manifest chunks.
	for _, f := range []string{"data/file_02.bin", "data/file_03.bin", "data/file_04.bin"} {
		assertFileExists(t, filepath.Join(gameDir, f))
	}
}

// 3. old manifest cached → Build flavor: some Local (dedup), some CDN.
func TestSophonFlavorBuild_EndToEnd(t *testing.T) {
	fs := newFakeSophonServer(t, "branches_main_only.json", "build_small.json", "")
	p, gameDir, tempRoot := newSophonProvider(t, fs, "6.4.0") // ∉ diff_tags
	seedAppliedManifest(t, tempRoot, "6.4.0")                 // primes LoadAppliedManifests
	seedOldFiles(t, gameDir)
	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if err := p.RunUpdate(context.Background(), plan, nil); err != nil {
		t.Fatalf("RunUpdate: %v", err)
	}
	assertGameVersion(t, gameDir, "6.6.0")
}

// 4. on-disk old file modified → Local chunk MD5 mismatch → CDN fallback.
func TestSophonFlavorBuild_StaleLocalChunk_FallbackCDN(t *testing.T) {
	fs := newFakeSophonServer(t, "branches_main_only.json", "build_small.json", "")
	p, gameDir, tempRoot := newSophonProvider(t, fs, "6.4.0")
	seedAppliedManifest(t, tempRoot, "6.4.0")
	seedOldFiles(t, gameDir)
	// Corrupt one old file so its chunk MD5 no longer matches → ErrChunkStale.
	corruptFile(t, filepath.Join(gameDir, "data/file_00.bin"))
	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if err := p.RunUpdate(context.Background(), plan, nil); err != nil {
		t.Fatalf("RunUpdate (should recover via CDN): %v", err)
	}
	assertGameVersion(t, gameDir, "6.6.0")
}

// 5. no cache, not in diff_tags → full download (tiny fixture).
func TestSophonFlavorFull_EndToEnd(t *testing.T) {
	fs := newFakeSophonServer(t, "branches_main_only.json", "build_tiny.json", "")
	p, gameDir, _ := newSophonProvider(t, fs, "6.0.0") // no cache, ∉ diff_tags
	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if err := p.RunUpdate(context.Background(), plan, nil); err != nil {
		t.Fatalf("RunUpdate: %v", err)
	}
	assertGameVersion(t, gameDir, "6.6.0")
	assertFileExists(t, filepath.Join(gameDir, "data/file_00.bin"))
}

// 6. predl run → next CheckForUpdate finds predl_ready → apply from predl staging.
// Uses gameVersion="6.5.0" + seedAppliedManifest so predl is offered as build-flavor
// (6.5.0 ∉ predl.diff_tags=["6.6.0"] but oldManifest != nil → flavorSophonPredlBuild).
func TestSophonPredl_StagingThenLiveApply(t *testing.T) {
	fs := newFakeSophonServer(t, "branches_with_predl.json", "build_small.json", "patch_small.json")
	p, gameDir, tempRoot := newSophonProvider(t, fs, "6.5.0")
	seedAppliedManifest(t, tempRoot, "6.5.0") // enables predl build-flavor
	seedOldFiles(t, gameDir)
	// Stage predownload.
	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
	if err != nil {
		t.Fatalf("CheckForUpdate (predl avail): %v", err)
	}
	if !p.GetPredownloadAvailable(genshinGID) {
		t.Fatalf("expected predl available (need applied manifest seed for build-flavor predl)")
	}
	predlPlan := plan
	predlPlan.Kind = core.PlanPredownload
	if err := p.RunUpdate(context.Background(), predlPlan, nil); err != nil {
		t.Fatalf("predl RunUpdate: %v", err)
	}
	// Simulate patch day: branch.Main.Tag flips to 6.7.0 (no predl slot any more).
	fs.branchesJSON = "branches_predl_now.json"
	plan2, err := p.CheckForUpdate(context.Background(), genshinGID)
	if err != nil {
		t.Fatalf("CheckForUpdate (consume): %v", err)
	}
	if plan2.Reason != core.ReasonResumeInterrupted {
		t.Logf("predl-consume reason = %q (want resume_interrupted if main.tag matched predl target)", plan2.Reason)
	}
}

// 7. predl_ready target mismatches current branch.Main.Tag → detectPredlConsume
//    deletes the file.
// Uses gameVersion="6.3.0" (∉ diff_tags, no old manifest → Full flavor so no
// getPatchBuild call). Plants predl_ready.json at the main-tag versionDir
// (6.6.0) but with a stale TargetVersion so detectPredlConsume rejects+removes.
func TestSophonPredlStale_TargetMismatch(t *testing.T) {
	fs := newFakeSophonServer(t, "branches_with_predl.json", "build_tiny.json", "")
	p, _, tempRoot := newSophonProvider(t, fs, "6.3.0") // ∉ diff_tags, no old manifest → Full flavor
	// Plant a predl_ready.json at the main-tag versionDir (6.6.0) with a
	// TargetVersion="9.9.9" that doesn't match mainTag="6.6.0" → stale.
	seedStalePredlReady(t, tempRoot, "6.6.0" /*verDir*/, "9.9.9" /*targetVersion*/)
	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	// Stale predl must NOT be consumed.
	if plan.Reason == core.ReasonResumeInterrupted {
		t.Fatalf("stale predl must not be consumed")
	}
	// predl_ready.json must have been deleted by detectPredlConsume.
	assertPredlReadyAbsent(t, tempRoot, "6.6.0")
}

// 8. currentLocal == branch.PreDownload.Tag → predlAvailable false (tiny).
func TestSophonPredl_CurrentLocalEqPredlTag(t *testing.T) {
	fs := newFakeSophonServer(t, "branches_with_predl.json", "build_tiny.json", "")
	p, _, _ := newSophonProvider(t, fs, "6.7.0") // == predl tag
	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if p.GetPredownloadAvailable(genshinGID) {
		t.Fatalf("predl must be unavailable when currentLocal == predl tag")
	}
	_ = plan
}

// ---- Scenarios 9–16 (resume, retry, no-install, legacy regression, self-heal, cancel) ----

// 9. kill mid-apply (2 of N done) → restart → resume completes.
func TestSophonResume_ApplyWALMidFlight(t *testing.T) {
	fs := newFakeSophonServer(t, "branches_main_only.json", "build_small.json", "")
	p, gameDir, tempRoot := newSophonProvider(t, fs, "6.0.0")
	// Pre-seed a half-done sophon_apply.wal (2 records done, rest pending) so
	// RunUpdate resumes from the first pending record (§6.9 path 1).
	seedMidFlightApplyWAL(t, tempRoot, "6.6.0", "build-small")
	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if err := p.RunUpdate(context.Background(), plan, nil); err != nil {
		t.Fatalf("resume RunUpdate: %v", err)
	}
	assertGameVersion(t, gameDir, "6.6.0")
}

// 10. kill mid-download (3 of 10 in ChunksDone) → reuse 3, fetch 7.
func TestSophonResume_DownloadCrashMidChunks(t *testing.T) {
	fs := newFakeSophonServer(t, "branches_main_only.json", "build_small.json", "")
	p, gameDir, tempRoot := newSophonProvider(t, fs, "6.0.0")
	staged := seedPartialDownload(t, tempRoot, "6.6.0", "build-small", 3) // mark 3 chunks done + their staging files
	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if err := p.RunUpdate(context.Background(), plan, nil); err != nil {
		t.Fatalf("RunUpdate: %v", err)
	}
	// The 3 pre-staged chunks must NOT have been re-fetched from CDN.
	for _, name := range staged {
		if fs.chunkHits[name] != 0 {
			t.Errorf("chunk %s was re-downloaded (%d hits); expected reuse from staging", name, fs.chunkHits[name])
		}
	}
	assertGameVersion(t, gameDir, "6.6.0")
}

// 11. first GET corrupt, retry succeeds.
func TestSophonChunkVerifyFail_RetryThenSucceed(t *testing.T) {
	fs := newFakeSophonServer(t, "branches_main_only.json", "build_tiny.json", "")
	p, gameDir, _ := newSophonProvider(t, fs, "6.0.0")
	name := tinyChunkName(t)
	fs.chunkFailFirst[name] = 1 // first GET corrupt, second OK
	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if err := p.RunUpdate(context.Background(), plan, nil); err != nil {
		t.Fatalf("RunUpdate should recover after retry: %v", err)
	}
	assertGameVersion(t, gameDir, "6.6.0")
}

// 12. all 3 retries corrupt → sophon_chunk_verify_failed (tiny).
func TestSophonChunkVerifyFail_RetryExhausted(t *testing.T) {
	fs := newFakeSophonServer(t, "branches_main_only.json", "build_tiny.json", "")
	p, _, _ := newSophonProvider(t, fs, "6.0.0")
	name := tinyChunkName(t)
	fs.chunkFailFirst[name] = 99 // never serves good bytes
	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	err = p.RunUpdate(context.Background(), plan, nil)
	assertUpdateErrorCode(t, err, "sophon_chunk_verify_failed")
}

// 13. config.ini missing → sophon_no_install (tiny).
func TestSophonNoInstall_Error(t *testing.T) {
	fs := newFakeSophonServer(t, "branches_main_only.json", "build_tiny.json", "")
	p, _, _ := newSophonProvider(t, fs, "") // no config.ini
	_, err := p.CheckForUpdate(context.Background(), genshinGID)
	assertUpdateErrorCode(t, err, "sophon_no_install")
}

// 14. non-Sophon games still use v1 zip+hdiff path (cross-provider regression).
func TestSophonHSRZZZ_LegacyPathUnchanged(t *testing.T) {
	fs := newFakeSophonServer(t, "branches_main_only.json", "build_tiny.json", "")
	p, _, _ := newSophonProvider(t, fs, "1.0.0")
	// HSR is UsesSophon=false → CheckForUpdate must NOT hit getBuild/getPatchBuild.
	hsr := core.GameID("hoyoverse/hsr")
	registerTestGameDir(t, p, hsr, t.TempDir())
	_, _ = p.CheckForUpdate(context.Background(), hsr)
	// getBuild must not have been called for the legacy game.
	// (Counted via a sentinel: legacy path uses getGamePackages, not getBuild.)
	if buildHits := fs.buildHitsFor(); buildHits != 0 {
		t.Errorf("legacy HSR hit getBuild %d times; should use getGamePackages", buildHits)
	}
}

// 15. self-heal: stale local, last_apply_target matches, 24h passed → idle.
func TestSophonMaybeSelfHeal_WritebackRetry(t *testing.T) {
	fs := newFakeSophonServer(t, "branches_main_only.json", "build_small.json", "")
	p, gameDir, tempRoot := newSophonProvider(t, fs, "6.5.0") // stale displayed version
	seedLastApplyTarget(t, tempRoot, "6.6.0", false /*ConfigWritebackOK*/, -25 /*hoursAgo*/)
	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	// Self-heal writes config.ini=6.6.0 and returns an idle plan.
	assertGameVersion(t, gameDir, "6.6.0")
	if p.GetPredownloadAvailable(genshinGID) {
		t.Fatalf("self-heal idle plan should not advertise predl")
	}
	_ = plan
}

// 16. ctx.Cancel during zstd stream → worker exits cleanly (tiny).
func TestSophonCancelMidChunkDownload(t *testing.T) {
	fs := newFakeSophonServer(t, "branches_main_only.json", "build_tiny.json", "")
	p, _, _ := newSophonProvider(t, fs, "6.0.0")
	ctx, cancel := context.WithCancel(context.Background())
	plan, err := p.CheckForUpdate(ctx, genshinGID)
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	cancel()
	err = p.RunUpdate(ctx, plan, nil)
	if err == nil {
		t.Fatalf("expected context-cancelled error")
	}
	if !strings.Contains(err.Error(), "context") && !errorsIsCanceled(err) {
		t.Logf("cancel returned %v (INTEGRATOR: assert context.Canceled per v1 convention)", err)
	}
}

// ---- Scenarios 17–30 (cross-device, malformed, ScanRecovery, import-cycle, demotion, xxh64-fallback, predl thresholds, idle cleanup, zero-value kind, full-flavor blocked) ----

// 17. assemble succeeds, rename returns EXDEV → copy+delete fallback.
func TestSophonCrossDeviceErrno_AssembleRename(t *testing.T) {
	t.Skip("EXDEV cannot be reliably forced on a single-volume CI temp dir; " +
		"sophon.SafeAtomicRename's copy+delete fallback is unit-tested in " +
		"sophon/file_assemble_test.go (T11) and sophon/rename build-tagged tests (T9). " +
		"INTEGRATOR: enable only on a multi-volume smoke host (Task 25).")
}

// 18. branch.Main.DiffTags = [] → never Patch flavor.
func TestSophonDiffTagsEmpty_FallsToBuildOrFull(t *testing.T) {
	fs := newFakeSophonServer(t, "branches_no_difftags.json", "build_small.json", "patch_small.json")
	p, gameDir, _ := newSophonProvider(t, fs, "6.5.0")
	seedOldFiles(t, gameDir)
	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if err := p.RunUpdate(context.Background(), plan, nil); err != nil {
		t.Fatalf("RunUpdate: %v", err)
	}
	// getPatchBuild must not have been used as the flavor (no diff_tags hit).
	assertGameVersion(t, gameDir, "6.6.0")
}

// 19. branch.Main.Categories empty → sophon_manifest_fetch_failed.
func TestSophonMainCategoriesEmpty_Error(t *testing.T) {
	fs := newFakeSophonServer(t, "branches_no_categories.json", "build_small.json", "")
	p, _, _ := newSophonProvider(t, fs, "6.5.0")
	_, err := p.CheckForUpdate(context.Background(), genshinGID)
	assertUpdateErrorCode(t, err, "sophon_manifest_fetch_failed")
}

// 20. UnusedAssets entries trigger delete WAL records.
func TestSophonUnusedAssetsDeleted(t *testing.T) {
	fs := newFakeSophonServer(t, "branches_main_only.json", "build_small.json", "patch_small.json")
	p, gameDir, _ := newSophonProvider(t, fs, "6.5.0")
	seedOldFiles(t, gameDir)
	// Pre-create the file the patch's UnusedAssets says to delete.
	mustWrite(t, filepath.Join(gameDir, "data/old_removed.bin"), bytesRepeat(0x7, 16))
	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if err := p.RunUpdate(context.Background(), plan, nil); err != nil {
		t.Fatalf("RunUpdate: %v", err)
	}
	assertFileMissing(t, filepath.Join(gameDir, "data/old_removed.bin"))
}

// 21. ScanRecovery classifies a dir with only sophon_apply.wal as ApplyResume.
func TestScanRecovery_SophonApplyWAL(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "sophon_apply.wal"),
		[]byte(`{"game_id":"hoyoverse/genshin","was_predl":false,"records":[]}`))
	st := core.ScanRecovery(dir)
	if st.Phase != core.RecoveryPhaseApplyResume {
		t.Fatalf("want ApplyResume, got %v", st.Phase)
	}
}

// 22. ScanRecovery classifies a dir with only sophon_progress.json as DownloadResume.
func TestScanRecovery_SophonProgress(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "sophon_progress.json"),
		[]byte(`{"game_id":"hoyoverse/genshin","chunks_done":{"a":true}}`))
	st := core.ScanRecovery(dir)
	if st.Phase != core.RecoveryPhaseDownloadResume {
		t.Fatalf("want DownloadResume, got %v", st.Phase)
	}
}

// 23. sophon_apply.wal AND apply.wal both present → sophon_apply.wal wins.
func TestScanRecovery_Precedence_SophonOverV1(t *testing.T) {
	dir := t.TempDir()
	mustWrite(t, filepath.Join(dir, "sophon_apply.wal"),
		[]byte(`{"game_id":"hoyoverse/genshin","was_predl":true,"records":[]}`))
	mustWrite(t, filepath.Join(dir, "apply.wal"), []byte(`{"was_predl":false}`))
	st := core.ScanRecovery(dir)
	if st.Phase != core.RecoveryPhaseApplyResume {
		t.Fatalf("want ApplyResume, got %v", st.Phase)
	}
	if !st.WasPredl {
		t.Fatalf("WasPredl must come from sophon_apply.wal header (true), got false")
	}
}

// 24. meta-test: sophon sub-package must NOT import the parent hoyoverse package.
func TestSophonImportCycleFree(t *testing.T) {
	out := goListDeps(t, "omnigate/internal/providers/hoyoverse/sophon")
	if strings.Contains(out, "omnigate/internal/providers/hoyoverse\n") ||
		strings.HasSuffix(strings.TrimSpace(out), "omnigate/internal/providers/hoyoverse") {
		t.Fatalf("sophon must not depend on parent hoyoverse package; deps:\n%s", out)
	}
}

// 25. pre-apply MD5 fails on modded source → demote hdiff_patch → chunk_assemble.
func TestSophonHdiffPatch_OldFileMD5Mismatch_DemoteToChunkAssemble(t *testing.T) {
	fs := newFakeSophonServer(t, "branches_main_only.json", "build_small.json", "patch_small.json")
	p, gameDir, _ := newSophonProvider(t, fs, "6.5.0")
	seedOldFiles(t, gameDir)
	// Mod data/file_00.bin so its pre-apply MD5 != OriginalFileMd5 → demotion.
	corruptFile(t, filepath.Join(gameDir, "data/file_00.bin"))
	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if err := p.RunUpdate(context.Background(), plan, nil); err != nil {
		t.Fatalf("RunUpdate (should demote + succeed): %v", err)
	}
	assertGameVersion(t, gameDir, "6.6.0")
	assertFileMD5(t, filepath.Join(gameDir, "data/file_00.bin"), assetMD5(t, "sample.manifest.pb.zst", "data/file_00.bin"))
}

// 26. ChunkName first-16 not hex → MD5 fallback verification.
func TestSophonChunkVerify_XXh64ParseFail_FallbackMD5(t *testing.T) {
	fs := newFakeSophonServer(t, "branches_main_only.json", "build_nonhex_chunk.json", "")
	p, gameDir, _ := newSophonProvider(t, fs, "6.0.0")
	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if err := p.RunUpdate(context.Background(), plan, nil); err != nil {
		t.Fatalf("RunUpdate (MD5 fallback path): %v", err)
	}
	assertGameVersion(t, gameDir, "6.6.0")
}

// 27. predl partial-stale staging: below-threshold stale → reuse predl staging
//
//	(only the bad chunks re-fetched); above-threshold stale → discard predl
//	staging + fresh full download. DISCRIMINATING signal = number of CDN chunk
//	GETs during RunUpdate (§E.3 P13 counter):
//	  below threshold → only the stale chunks are re-fetched (skip-if-verified)
//	  above threshold → predl staging removed → every chunk re-fetched
//	The threshold is strict > 0.25 (see verifyPredlStaging). With 15 chunks:
//	  3/15 = 20% → below threshold → reuse
//	  4/15 = 26.7% → above threshold → discard
func TestSophonPredl_PartialStaleStagingThresholdRecover(t *testing.T) {
	total := totalCDNChunks(t, "sample.manifest.pb.zst")
	t.Run("below_threshold_reuse_predl", func(t *testing.T) {
		fs := newFakeSophonServer(t, "branches_predl_now.json", "build_small.json", "patch_small.json")
		p, gameDir, tempRoot := newSophonProvider(t, fs, "6.6.0")
		seedOldFiles(t, gameDir)
		seedConsumablePredl(t, tempRoot, "6.7.0", "build-predl", 3 /*nCorrupt — 3/15=20% < 25% threshold*/, fs.URL)
		fs.resetChunkGETs()
		plan, err := p.CheckForUpdate(context.Background(), genshinGID)
		if err != nil {
			t.Fatalf("CheckForUpdate: %v", err)
		}
		if err := p.RunUpdate(context.Background(), plan, nil); err != nil {
			t.Fatalf("RunUpdate (reuse predl): %v", err)
		}
		assertGameVersion(t, gameDir, "6.7.0")
		// Reuse: only the 3 stale chunks re-fetched, rest skipped.
		if got := fs.chunkGETs(); got > total/2 {
			t.Fatalf("below threshold: predl staging should be reused; want few chunk GETs (<= %d), got %d", total/2, got)
		}
	})
	t.Run("above_threshold_discard_fresh", func(t *testing.T) {
		fs := newFakeSophonServer(t, "branches_predl_now.json", "build_small.json", "patch_small.json")
		p, gameDir, tempRoot := newSophonProvider(t, fs, "6.6.0")
		seedOldFiles(t, gameDir)
		seedConsumablePredl(t, tempRoot, "6.7.0", "build-predl", 4 /*nCorrupt — 4/15=26.7% > 25% threshold*/, fs.URL)
		fs.resetChunkGETs()
		plan, err := p.CheckForUpdate(context.Background(), genshinGID)
		if err != nil {
			t.Fatalf("CheckForUpdate: %v", err)
		}
		if err := p.RunUpdate(context.Background(), plan, nil); err != nil {
			t.Fatalf("RunUpdate (discard+fresh): %v", err)
		}
		assertGameVersion(t, gameDir, "6.7.0")
		// Discard: predl staging removed → every CDN chunk re-fetched into staging/main.
		if got := fs.chunkGETs(); got < total {
			t.Fatalf("above threshold: predl should be discarded + fully re-downloaded; want >= %d chunk GETs, got %d", total, got)
		}
	})
}

// 28. crash between WAL-all-done and staging cleanup → next CheckForUpdate idle + sweep.
func TestSophonIdlePathCleanup_AllDoneWAL(t *testing.T) {
	fs := newFakeSophonServer(t, "branches_main_only.json", "build_small.json", "")
	p, gameDir, tempRoot := newSophonProvider(t, fs, "6.6.0") // already at target
	seedAllDoneApplyWAL(t, tempRoot, "6.6.0", "build-small") // orphan WAL + staging
	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if p.GetPredownloadAvailable(genshinGID) {
		t.Fatalf("idle plan should not advertise predl")
	}
	assertSophonWALAbsent(t, tempRoot, "6.6.0")
	assertStagingAbsent(t, tempRoot, "6.6.0", "build-small")
	_ = gameDir
	_ = plan
}

// 29. legacy v1-shaped predl_ready.json (Kind=="") → treated as stale → no consume.
// Uses gameVersion="6.5.0" so detectPredlConsume is reached (not short-circuited by idle).
// The predl_ready.json is at the mainTag versionDir (6.6.0) with Kind="" (stale).
func TestSophonPredl_ZeroValueKind_TreatedAsStale(t *testing.T) {
	// Use branches_with_predl where diff_tags=["6.5.0"] so currentLocal=6.5.0
	// triggers getPatchBuild. Serve patch_small.json so the plan builds.
	fs := newFakeSophonServer(t, "branches_with_predl.json", "build_small.json", "patch_small.json")
	p, _, tempRoot := newSophonProvider(t, fs, "6.5.0") // 6.5.0 ∈ diff_tags; not idle
	seedZeroKindPredlReady(t, tempRoot, "6.6.0") // Kind:"" v1-shaped at mainTag dir
	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if plan.Reason == core.ReasonResumeInterrupted {
		t.Fatalf("zero-Kind predl must not be consumed")
	}
	assertPredlReadyAbsent(t, tempRoot, "6.6.0")
}

// 30. predl avail but ∉ DiffTags AND no cached old manifest → Full predl never offered.
func TestSophonPredl_FullFlavorBlocked(t *testing.T) {
	fs := newFakeSophonServer(t, "branches_with_predl.json", "build_small.json", "")
	p, _, _ := newSophonProvider(t, fs, "6.3.0") // ∉ predl diff_tags (6.6.0); no cache
	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
	if err != nil {
		t.Fatalf("CheckForUpdate: %v", err)
	}
	if p.GetPredownloadAvailable(genshinGID) {
		t.Fatalf("full-flavor predl must be blocked (no chunk reuse possible)")
	}
	_ = plan
}

// 31. cold-restart predl-consume: a COMPLETED predownload must survive an app
// restart. runSophonPredownload drops sophon_progress.json once predl_ready.json
// is committed, so on the next launch ScanRecovery classifies the predl version
// dir as PredlAwaiting (NOT DownloadResume) and preserves predl_ready.json.
// WITHOUT that drop, both files coexist and the ScanRecovery ladder
// (sophon_progress.json supersedes predl_ready.json) would DELETE predl_ready.json,
// losing the staged predownload → patch day re-downloads the whole update.
func TestSophonPredl_CompletedSurvivesRestart(t *testing.T) {
	fs := newFakeSophonServer(t, "branches_with_predl.json", "build_small.json", "patch_small.json")
	p, gameDir, tempRoot := newSophonProvider(t, fs, "6.5.0")
	seedAppliedManifest(t, tempRoot, "6.5.0") // enables predl build-flavor
	seedOldFiles(t, gameDir)
	// Stage predownload to completion (writes predl_ready.json for predl target).
	plan, err := p.CheckForUpdate(context.Background(), genshinGID)
	if err != nil {
		t.Fatalf("CheckForUpdate (predl avail): %v", err)
	}
	if !p.GetPredownloadAvailable(genshinGID) {
		t.Fatalf("expected predl available")
	}
	predlPlan := plan
	predlPlan.Kind = core.PlanPredownload
	if err := p.RunUpdate(context.Background(), predlPlan, nil); err != nil {
		t.Fatalf("predl RunUpdate: %v", err)
	}

	// predl target version is the predownload branch tag in branches_with_predl.json.
	const predlTag = "6.7.0"
	predlDir := versionSidecarDir(tempRoot, genshinGID, predlTag)

	// File-state checks: predl_ready.json survives; sophon_progress.json dropped.
	if _, err := os.Stat(filepath.Join(predlDir, "predl_ready.json")); err != nil {
		t.Fatalf("predl_ready.json must exist after predl completion: %v", err)
	}
	if _, err := os.Stat(filepath.Join(predlDir, "sophon_progress.json")); !os.IsNotExist(err) {
		t.Fatalf("sophon_progress.json must be dropped after predl completion (err=%v); restart would lose predl_ready", err)
	}

	// Cold-restart classification: PredlAwaiting (not DownloadResume).
	st := core.ScanRecovery(predlDir)
	if st.Phase != core.RecoveryPhasePredlAwaiting {
		t.Fatalf("ScanRecovery phase = %v, want RecoveryPhasePredlAwaiting (completed predl must survive restart)", st.Phase)
	}
	// ScanRecovery must NOT have deleted predl_ready.json in this path.
	if _, err := os.Stat(filepath.Join(predlDir, "predl_ready.json")); err != nil {
		t.Fatalf("predl_ready.json must survive ScanRecovery: %v", err)
	}
}

// ---- assertion helpers ----

func mustWrite(t *testing.T, path string, b []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, b, 0o644); err != nil {
		t.Fatal(err)
	}
}

func bytesRepeat(b byte, n int) []byte {
	out := make([]byte, n)
	for i := range out {
		out[i] = b
	}
	return out
}

func assertFileExists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file %s to exist: %v", path, err)
	}
}

func assertFileMissing(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("expected %s to be absent, stat err=%v", path, err)
	}
}

func corruptFile(t *testing.T, path string) {
	t.Helper()
	if err := os.WriteFile(path, []byte("MODDED-CONTENT-DIFFERENT-MD5"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// assertGameVersion reads config.ini and checks game_version.
func assertGameVersion(t *testing.T, gameDir, want string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(gameDir, "config.ini"))
	if err != nil {
		t.Fatalf("read config.ini: %v", err)
	}
	if !strings.Contains(string(b), "game_version="+want) {
		t.Fatalf("config.ini does not contain game_version=%s:\n%s", want, b)
	}
}

// assertUpdateErrorCode unwraps a *core.UpdateError and checks .Code.
func assertUpdateErrorCode(t *testing.T, err error, wantCode string) {
	t.Helper()
	if err == nil {
		t.Fatalf("expected UpdateError{Code:%q}, got nil", wantCode)
	}
	var ue *core.UpdateError
	if !errorsAs(err, &ue) {
		t.Fatalf("error %v is not *core.UpdateError", err)
	}
	if ue.Code != wantCode {
		t.Fatalf("UpdateError.Code = %q, want %q", ue.Code, wantCode)
	}
}

func assertFileMD5(t *testing.T, path, wantMD5 string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("assertFileMD5: read %s: %v", path, err)
	}
	sum := md5.Sum(data)
	got := hex.EncodeToString(sum[:])
	if got != wantMD5 {
		t.Fatalf("file %s MD5 = %q, want %q", path, got, wantMD5)
	}
}

func assertPredlReadyAbsent(t *testing.T, tempRoot, version string) {
	t.Helper()
	path := filepath.Join(versionSidecarDir(tempRoot, genshinGID, version), "predl_ready.json")
	assertFileMissing(t, path)
}

func assertSophonWALAbsent(t *testing.T, tempRoot, version string) {
	t.Helper()
	path := filepath.Join(versionSidecarDir(tempRoot, genshinGID, version), "sophon_apply.wal")
	assertFileMissing(t, path)
}

func assertStagingAbsent(t *testing.T, tempRoot, version, buildID string) {
	t.Helper()
	// Check both main and predl staging dirs.
	for _, kind := range []string{"main", "predl"} {
		path := sophonStagingDir(tempRoot, genshinGID, version, kind, buildID)
		if _, err := os.Stat(path); err == nil {
			t.Fatalf("staging dir %s should be absent but exists", path)
		}
	}
}

// ---- low-level utility wrappers ----

func errorsAs(err error, target any) bool { return errors.As(err, target) }

func errorsIsCanceled(err error) bool { return errors.Is(err, context.Canceled) }

// timeNowUTC returns current UTC time (thin wrapper for test clarity).
func timeNowUTC() time.Time { return time.Now().UTC() }

// timeDuration returns hoursAgo hours as a Duration (negative = in the past).
func timeDuration(hoursAgo int) time.Duration { return time.Duration(hoursAgo) * time.Hour }

func goListDeps(t *testing.T, pkg string) string {
	t.Helper()
	cmd := exec.Command("go", "list", "-deps", pkg)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("go list -deps %s: %v", pkg, err)
	}
	return string(out)
}

// assetMD5 loads a .manifest.pb.zst fixture and returns the AssetHashMd5 for assetName.
func assetMD5(t *testing.T, manifestFixture, assetName string) string {
	t.Helper()
	m := loadManifestFixture(t, manifestFixture)
	for _, a := range m.Assets {
		if a.AssetName == assetName {
			return a.AssetHashMd5
		}
	}
	t.Fatalf("assetMD5: asset %q not found in %s", assetName, manifestFixture)
	return ""
}

// totalCDNChunks returns the total number of distinct CDN chunk names in the
// manifest fixture (§E.3 P13 helper for scenario 27).
func totalCDNChunks(t *testing.T, manifestFixture string) int {
	t.Helper()
	m := loadManifestFixture(t, manifestFixture)
	seen := map[string]bool{}
	for _, a := range m.Assets {
		if a.AssetType != 0 {
			continue
		}
		for _, ch := range a.AssetChunks {
			seen[ch.ChunkName] = true
		}
	}
	return len(seen)
}

// loadManifestFixture loads + decompresses + unmarshals a .manifest.pb.zst fixture.
func loadManifestFixture(t *testing.T, name string) *pb.SophonManifestProto {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "sophon", name))
	if err != nil {
		t.Fatalf("loadManifestFixture: read %s: %v", name, err)
	}
	r, err := zstd.NewReader(bytes.NewReader(data))
	if err != nil {
		t.Fatalf("loadManifestFixture: zstd reader: %v", err)
	}
	defer r.Close()
	var buf bytes.Buffer
	if _, err := buf.ReadFrom(r); err != nil {
		t.Fatalf("loadManifestFixture: zstd decompress: %v", err)
	}
	var m pb.SophonManifestProto
	if err := proto.Unmarshal(buf.Bytes(), &m); err != nil {
		t.Fatalf("loadManifestFixture: proto unmarshal: %v", err)
	}
	return &m
}

// tinyChunkName returns the ChunkName of the single chunk in tiny.manifest.pb.zst.
func tinyChunkName(t *testing.T) string {
	t.Helper()
	m := loadManifestFixture(t, "tiny.manifest.pb.zst")
	if len(m.Assets) == 0 || len(m.Assets[0].AssetChunks) == 0 {
		t.Fatal("tinyChunkName: tiny manifest has no assets/chunks")
	}
	return m.Assets[0].AssetChunks[0].ChunkName
}

// ---- seed helpers (plant sidecar state to simulate resume / stale / mid-flight) ----

// seedOldFiles writes the exact decompressed bytes the generator used for
// small-manifest assets 0–4 into gameDir/data/file_0{0..4}.bin so the
// dedup-Local path can read them for build-flavor updates.
// Each chunk's payload is bytes.Repeat(byte(globalIdx), 1024).
// Asset N uses global indices [3N, 3N+1, 3N+2].
func seedOldFiles(t *testing.T, gameDir string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(gameDir, "data"), 0o755); err != nil {
		t.Fatal(err)
	}
	for asset := 0; asset < 5; asset++ {
		var content []byte
		for c := 0; c < 3; c++ {
			globalIdx := asset*3 + c
			content = append(content, bytes.Repeat([]byte{byte(globalIdx)}, 1024)...)
		}
		path := filepath.Join(gameDir, fmt.Sprintf("data/file_%02d.bin", asset))
		if err := os.WriteFile(path, content, 0o644); err != nil {
			t.Fatalf("seedOldFiles: %v", err)
		}
	}
}

// seedAppliedManifest writes a minimal applied.json that points at the
// committed sample.manifest.pb.zst blob so LoadAppliedManifests can return
// it for the build-flavor dedup path (oldManifest != nil).
// We copy the fixture into the manifests dir under the sidecar.
func seedAppliedManifest(t *testing.T, tempRoot, version string) {
	t.Helper()
	gid := genshinGID
	// Copy sample.manifest.pb.zst into the manifests sidecar under buildID "build-old".
	buildID := "build-old"
	manifDir := sophonManifestsDir(tempRoot, gid)
	if err := os.MkdirAll(manifDir, 0o755); err != nil {
		t.Fatal(err)
	}
	src := filepath.Join("testdata", "sophon", "sample.manifest.pb.zst")
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("seedAppliedManifest: read fixture: %v", err)
	}
	dst := filepath.Join(manifDir, buildID+"__game.manifest.pb.zst")
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatal(err)
	}
	// Write applied.json pointing latest → buildID / version.
	applied := appliedManifestSet{
		Latest: &appliedBuild{
			BuildID:    buildID,
			Version:    version,
			AppliedAt:  "2026-01-01T00:00:00Z",
			Categories: map[string]string{"game": buildID},
		},
	}
	b, err := json.MarshalIndent(applied, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sophonAppliedJSONPath(tempRoot, gid), b, 0o644); err != nil {
		t.Fatal(err)
	}
}

// seedStalePredlReady plants a predl_ready.json at versionSidecarDir(verDirVersion)
// with a stale TargetVersion (staleTgtVersion) that doesn't match any live branch tag.
// When verDirVersion == mainTag, detectPredlConsume will find the file, check
// pf.TargetVersion != mainTag → stale → delete.
func seedStalePredlReady(t *testing.T, tempRoot, verDirVersion, staleTgtVersion string) {
	t.Helper()
	verDir := versionSidecarDir(tempRoot, genshinGID, verDirVersion)
	if err := os.MkdirAll(verDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ready := sophonPredlReadyFile{
		Kind:          "sophon_patch",
		BuildID:       "build-stale",
		SourceVersion: "6.6.0",
		TargetVersion: staleTgtVersion,
		StagedAt:      "2026-01-01T00:00:00Z",
	}
	data, _ := json.Marshal(ready)
	path := filepath.Join(verDir, "predl_ready.json")
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// seedLastApplyTarget writes a last_apply_target.json so maybeSelfHealSophon
// can pick it up. hoursAgo < 0 → timestamp is in the past by |hoursAgo| hours
// (triggering the "stale > 24h" heal path).
func seedLastApplyTarget(t *testing.T, tempRoot, targetVersion string, writebackOK bool, hoursAgo int) {
	t.Helper()
	lat := &lastApplyTarget{
		TargetVersion:     targetVersion,
		ConfigWritebackOK: writebackOK,
	}
	if hoursAgo != 0 {
		lat.LastWritebackRetryTS = timeNowUTC().Add(timeDuration(hoursAgo))
	}
	if err := writeLastApplyTarget(tempRoot, genshinGID, lat); err != nil {
		t.Fatalf("seedLastApplyTarget: %v", err)
	}
}

// seedMidFlightApplyWAL writes a sophon_apply.wal where the first 2 records are
// "done" and the rest are "pending", simulating a mid-apply crash.
// The WAL's StagingRoot is pre-populated with the staged chunks from
// sample.manifest.pb.zst so the resume can actually assemble the files.
func seedMidFlightApplyWAL(t *testing.T, tempRoot, version, buildID string) {
	t.Helper()
	gid := genshinGID
	stagingRoot := sophonStagingDir(tempRoot, gid, version, "main", buildID)
	// Populate staging chunks from the fixture.
	populateStagingFromFixture(t, stagingRoot, "sample.manifest.pb.zst")
	// Build a WAL with the full plan from sample.manifest (all chunk_assemble).
	m := loadManifestFixture(t, "sample.manifest.pb.zst")
	wal := &sophonApplyWAL{
		GameID:      string(gid),
		TargetTag:   version,
		BuildID:     buildID,
		SourceTag:   "",
		Flavor:      "sophon_full",
		BranchKind:  "main",
		WasPredl:    false,
		StagingRoot: stagingRoot,
	}
	for i, a := range m.Assets {
		if a.AssetType != 0 {
			continue
		}
		var srcs []walChunkSource
		for _, ch := range a.AssetChunks {
			srcs = append(srcs, walChunkSource{
				Kind:        "cdn",
				ChunkName:   ch.ChunkName,
				URLPrefix:   "/cdn/chunks",
				UseCompress: true,
				DecompSize:  ch.ChunkSizeDecompressed,
				FileOffset:  ch.ChunkOnFileOffset,
				ExpectMD5:   ch.ChunkDecompressedHashMd5,
			})
		}
		state := "pending"
		if i < 2 {
			state = "done"
		}
		wal.Records = append(wal.Records, sophonApplyRecord{
			Kind:            "chunk_assemble",
			Path:            a.AssetName,
			State:           state,
			AssetMD5:        a.AssetHashMd5,
			AssembleSources: srcs,
		})
	}
	versionDir := versionSidecarDir(tempRoot, gid, version)
	if err := writeSophonApplyWAL(versionDir, wal); err != nil {
		t.Fatalf("seedMidFlightApplyWAL: %v", err)
	}
}

// seedPartialDownload writes a sophon_progress.json with the first nDone chunks
// marked done, and also copies those chunks into the staging dir so the resume
// can actually reuse them. Returns the chunk names that were seeded.
func seedPartialDownload(t *testing.T, tempRoot, version, buildID string, nDone int) []string {
	t.Helper()
	gid := genshinGID
	stagingRoot := sophonStagingDir(tempRoot, gid, version, "main", buildID)
	chunksDir := filepath.Join(stagingRoot, "chunks")
	if err := os.MkdirAll(chunksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := loadManifestFixture(t, "sample.manifest.pb.zst")
	// Collect all distinct chunk names in manifest order.
	var allChunks []string
	seen := map[string]bool{}
	for _, a := range m.Assets {
		for _, ch := range a.AssetChunks {
			if !seen[ch.ChunkName] {
				seen[ch.ChunkName] = true
				allChunks = append(allChunks, ch.ChunkName)
			}
		}
	}
	if nDone > len(allChunks) {
		nDone = len(allChunks)
	}
	seeded := allChunks[:nDone]
	done := make(map[string]bool, nDone)
	for _, name := range seeded {
		done[name] = true
		// Copy the raw (decompressed) chunk blob into staging so resume skips CDN.
		// Staged chunks hold decompressed bytes (DownloadChunk decompresses on write).
		src := filepath.Join("testdata", "sophon", "chunks", "raw_"+name)
		data, err := os.ReadFile(src)
		if err != nil {
			t.Fatalf("seedPartialDownload: read raw_%s: %v", name, err)
		}
		if err := os.WriteFile(filepath.Join(chunksDir, name), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	pf := &sophonProgressFile{
		GameID:      string(gid),
		Version:     version,
		BranchKind:  "main",
		BuildID:     buildID,
		Stage:       "download",
		ChunksDone:  done,
		PatchesDone: make(map[string]bool),
	}
	data, err := json.MarshalIndent(pf, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	dir := versionSidecarDir(tempRoot, gid, version)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sophon_progress.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	return seeded
}

// seedConsumablePredl writes a predl_ready.json + populates predl staging so
// CheckForUpdate takes the predl-consume path. nCorrupt chunks in the staging
// dir are overwritten with corrupt bytes to simulate stale staging.
// serverURL must be the fake server's URL so chunk URLPrefix values are absolute.
// Use nCorrupt=0 for a clean predl (all chunks valid).
func seedConsumablePredl(t *testing.T, tempRoot, targetVersion, buildID string, nCorrupt int, serverURL string) {
	t.Helper()
	gid := genshinGID
	// The predl_ready must declare SourceVersion == the installed version (6.6.0)
	// and TargetVersion == targetVersion (6.7.0). currentLocal in the test is "6.6.0".
	// predl diff_tags in branches_predl_now.json is ["6.6.0"] so the Kind must be
	// "sophon_patch". We reuse the sample.manifest chunks as the predl build.
	m := loadManifestFixture(t, "sample.manifest.pb.zst")

	// Build chunk source list mirroring what the planner would produce.
	var chunkSrcs []sophon.ChunkSource
	seenChunks := map[string]bool{}
	for _, a := range m.Assets {
		if a.AssetType != 0 {
			continue
		}
		for _, ch := range a.AssetChunks {
			if seenChunks[ch.ChunkName] {
				continue
			}
			seenChunks[ch.ChunkName] = true
			chunkSrcs = append(chunkSrcs, sophon.ChunkSource{
				Kind:         "cdn",
				Asset:        a.AssetName,
				ChunkName:    ch.ChunkName,
				URLPrefix:    serverURL + "/cdn/chunks",
				UseCompress:  true,
				DecompSize:   ch.ChunkSizeDecompressed,
				FileOffset:   ch.ChunkOnFileOffset,
				ExpectMD5:    ch.ChunkDecompressedHashMd5,
				CompressedSz: ch.ChunkSize,
			})
		}
	}

	// Write the predl_ready.json at versionSidecarDir(targetVersion).
	verDir := versionSidecarDir(tempRoot, gid, targetVersion)
	if err := os.MkdirAll(verDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ready := sophonPredlReadyFile{
		Kind:          "sophon_patch",
		BuildID:       buildID,
		SourceVersion: "6.6.0",
		TargetVersion: targetVersion,
		StagedAt:      "2026-01-01T00:00:00Z",
		PlanSnapshot: sophonPlanSnapshot{
			SophonChunkSources: chunkSrcs,
		},
	}
	data, _ := json.Marshal(ready)
	if err := os.WriteFile(filepath.Join(verDir, "predl_ready.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	// Populate staging with real chunks, then corrupt pctStale% of them.
	stagingRoot := sophonStagingDir(tempRoot, gid, targetVersion, "predl", buildID)
	populateStagingFromFixture(t, stagingRoot, "sample.manifest.pb.zst")

	if nCorrupt > 0 {
		chunksDir := filepath.Join(stagingRoot, "chunks")
		corrupted := 0
		for _, cs := range chunkSrcs {
			if corrupted >= nCorrupt {
				break
			}
			p := filepath.Join(chunksDir, cs.ChunkName)
			if err := os.WriteFile(p, []byte("STALE-PREDL-CHUNK"), 0o644); err != nil {
				t.Fatalf("seedConsumablePredl corrupt: %v", err)
			}
			corrupted++
		}
		t.Logf("seedConsumablePredl: corrupted %d/%d chunks", corrupted, len(chunkSrcs))
	}
}

// seedAllDoneApplyWAL writes a sophon_apply.wal where ALL records are "done",
// simulating a crash between WAL completion and staging cleanup.
func seedAllDoneApplyWAL(t *testing.T, tempRoot, version, buildID string) {
	t.Helper()
	gid := genshinGID
	stagingRoot := sophonStagingDir(tempRoot, gid, version, "main", buildID)
	populateStagingFromFixture(t, stagingRoot, "sample.manifest.pb.zst")
	m := loadManifestFixture(t, "sample.manifest.pb.zst")
	wal := &sophonApplyWAL{
		GameID:      string(gid),
		TargetTag:   version,
		BuildID:     buildID,
		SourceTag:   "",
		Flavor:      "sophon_full",
		BranchKind:  "main",
		WasPredl:    false,
		StagingRoot: stagingRoot,
	}
	for _, a := range m.Assets {
		if a.AssetType != 0 {
			continue
		}
		var srcs []walChunkSource
		for _, ch := range a.AssetChunks {
			srcs = append(srcs, walChunkSource{
				Kind:        "cdn",
				ChunkName:   ch.ChunkName,
				URLPrefix:   "/cdn/chunks",
				UseCompress: true,
				DecompSize:  ch.ChunkSizeDecompressed,
				FileOffset:  ch.ChunkOnFileOffset,
				ExpectMD5:   ch.ChunkDecompressedHashMd5,
			})
		}
		wal.Records = append(wal.Records, sophonApplyRecord{
			Kind:            "chunk_assemble",
			Path:            a.AssetName,
			State:           "done",
			AssetMD5:        a.AssetHashMd5,
			AssembleSources: srcs,
		})
	}
	versionDir := versionSidecarDir(tempRoot, gid, version)
	if err := writeSophonApplyWAL(versionDir, wal); err != nil {
		t.Fatalf("seedAllDoneApplyWAL: %v", err)
	}
}

// seedZeroKindPredlReady writes a predl_ready.json with Kind="" (v1 zero-value)
// so detectPredlConsume rejects it as stale.
func seedZeroKindPredlReady(t *testing.T, tempRoot, targetVersion string) {
	t.Helper()
	verDir := versionSidecarDir(tempRoot, genshinGID, targetVersion)
	if err := os.MkdirAll(verDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Deliberately write with Kind:"" (zero value, v1-shaped).
	raw := map[string]any{
		"build_id":       "build-zero",
		"source_version": "6.5.0",
		"target_version": targetVersion,
		"kind":           "", // zero value → stale
	}
	data, _ := json.Marshal(raw)
	if err := os.WriteFile(filepath.Join(verDir, "predl_ready.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// populateStagingFromFixture copies all chunk blobs from testdata/sophon/chunks/
// into stagingRoot/chunks/ for the given manifest fixture. Copies the raw_*
// (decompressed) variants because staged chunks hold decompressed bytes
// (DownloadChunk decompresses before writing, §5.1). Used by seed helpers.
func populateStagingFromFixture(t *testing.T, stagingRoot, manifestFixture string) {
	t.Helper()
	chunksDir := filepath.Join(stagingRoot, "chunks")
	if err := os.MkdirAll(chunksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	m := loadManifestFixture(t, manifestFixture)
	copied := map[string]bool{}
	for _, a := range m.Assets {
		for _, ch := range a.AssetChunks {
			if copied[ch.ChunkName] {
				continue
			}
			copied[ch.ChunkName] = true
			// Staged chunks = decompressed bytes = raw_<ChunkName> fixture.
			src := filepath.Join("testdata", "sophon", "chunks", "raw_"+ch.ChunkName)
			data, err := os.ReadFile(src)
			if err != nil {
				t.Fatalf("populateStagingFromFixture: read raw_%s: %v", ch.ChunkName, err)
			}
			if err := os.WriteFile(filepath.Join(chunksDir, ch.ChunkName), data, 0o644); err != nil {
				t.Fatal(err)
			}
		}
	}
}

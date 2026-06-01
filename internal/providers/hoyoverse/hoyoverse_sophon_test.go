package hoyoverse

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"omnigate/internal/core"
	"omnigate/internal/providers/hoyoverse/sophon"
)

// testMD5Hex returns the hex-encoded MD5 of b. Used by test helpers.
func testMD5Hex(b []byte) string {
	sum := md5.Sum(b)
	return hex.EncodeToString(sum[:])
}

func TestMaybeSelfHealSophon_ClockSkewParity(t *testing.T) {
	gid := core.GameID("hoyoverse/genshin")
	mk := func(t *testing.T, retryTS time.Time) (*Provider, string, string) {
		tempRoot := t.TempDir()
		gameDir := t.TempDir()
		// minimal config.ini at old version
		if err := os.WriteFile(filepath.Join(gameDir, "config.ini"), []byte("[General]\ngame_version=6.5.0\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		lat := &lastApplyTarget{TargetVersion: "6.6.0", LastWritebackRetryTS: retryTS}
		if err := writeLastApplyTarget(tempRoot, gid, lat); err != nil {
			t.Fatal(err)
		}
		p := New(Settings{}, nil)
		return p, tempRoot, gameDir
	}
	now := time.Now().UTC()

	// forward <24h → false
	p, tempRoot, gameDir := mk(t, now.Add(-1*time.Hour))
	if healed := p.maybeSelfHealSophon("6.5.0", "6.6.0", gameDir, tempRoot, gid); healed {
		t.Fatalf("forward<24h → healed true, want false")
	}
	// rewind >24h → false + sidecar removed
	p, tempRoot, gameDir = mk(t, now.Add(48*time.Hour))
	if healed := p.maybeSelfHealSophon("6.5.0", "6.6.0", gameDir, tempRoot, gid); healed {
		t.Fatalf("rewind>24h → healed true, want false")
	}
	latPath := filepath.Join(gameSidecarDir(tempRoot, gid), "last_apply_target.json")
	if _, err := os.Stat(latPath); err == nil {
		t.Fatalf("rewind>24h should remove last_apply_target.json")
	}
	// stale >24h forward → heal true
	p, tempRoot, gameDir = mk(t, now.Add(-48*time.Hour))
	if healed := p.maybeSelfHealSophon("6.5.0", "6.6.0", gameDir, tempRoot, gid); !healed {
		t.Fatalf("stale>24h → healed false, want true")
	}
	if v, _ := ReadGameVersion(gameDir); v != "6.6.0" {
		t.Fatalf("heal should write game_version=6.6.0, got %q", v)
	}
}

func TestCheckForUpdateSophon_NoInstall(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"retcode":0,"message":"","data":{"game_branches":[{"game":{"id":"gopR6Cufr3","biz":"hk4e_global"},"main":{"package_id":"pkg","branch":"main","tag":"6.6.0","categories":[{"category_id":"10016","matching_field":"game","type":"CATEGORY_TYPE_RESOURCE"}]}}]}}`))
	}))
	defer srv.Close()
	p := New(Settings{}, nil)
	p.SetBranchAPIBaseURL(srv.URL)
	tempRoot := t.TempDir()
	gameDir := t.TempDir() // no config.ini → currentLocal == "" → sophon_no_install
	p.SetTempRootFn(func(core.GameID) string { return tempRoot })

	_, err := p.checkForUpdateSophon(context.Background(), core.GameID("hoyoverse/genshin"), gameDir, tempRoot)
	if err == nil {
		t.Fatal("expected error for no-install, got nil")
	}
	ue, ok := err.(*core.UpdateError)
	if !ok {
		t.Fatalf("expected *core.UpdateError, got %T: %v", err, err)
	}
	if ue.Code != "sophon_no_install" {
		t.Fatalf("expected sophon_no_install, got %q", ue.Code)
	}
}

func TestCheckForUpdateSophon_Idle(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"retcode":0,"message":"","data":{"game_branches":[{"game":{"id":"gopR6Cufr3","biz":"hk4e_global"},"main":{"package_id":"pkg","branch":"main","tag":"6.6.0","categories":[{"category_id":"10016","matching_field":"game","type":"CATEGORY_TYPE_RESOURCE"}]}}]}}`))
	}))
	defer srv.Close()
	p := New(Settings{}, nil)
	p.SetBranchAPIBaseURL(srv.URL)
	tempRoot := t.TempDir()
	gameDir := t.TempDir()
	// write config.ini at the same version as main.tag → idle
	if err := os.WriteFile(filepath.Join(gameDir, "config.ini"), []byte("[General]\ngame_version=6.6.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p.SetTempRootFn(func(core.GameID) string { return tempRoot })

	plan, err := p.checkForUpdateSophon(context.Background(), core.GameID("hoyoverse/genshin"), gameDir, tempRoot)
	if err != nil {
		t.Fatalf("idle path: unexpected error: %v", err)
	}
	if plan.Reason != core.ReasonUnspecified {
		t.Fatalf("idle: want ReasonUnspecified, got %v", plan.Reason)
	}
	if plan.Kind != core.PlanUpdate {
		t.Fatalf("idle: want PlanUpdate kind, got %v", plan.Kind)
	}
}

func TestCheckForUpdateSophon_EmptyCategories(t *testing.T) {
	// branch with empty categories → sophon_manifest_fetch_failed
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"retcode":0,"message":"","data":{"game_branches":[{"game":{"id":"gopR6Cufr3","biz":"hk4e_global"},"main":{"package_id":"pkg","branch":"main","tag":"6.6.0","categories":[]}}]}}`))
	}))
	defer srv.Close()
	p := New(Settings{}, nil)
	p.SetBranchAPIBaseURL(srv.URL)
	tempRoot := t.TempDir()
	gameDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(gameDir, "config.ini"), []byte("[General]\ngame_version=6.5.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	p.SetTempRootFn(func(core.GameID) string { return tempRoot })

	_, err := p.checkForUpdateSophon(context.Background(), core.GameID("hoyoverse/genshin"), gameDir, tempRoot)
	if err == nil {
		t.Fatal("expected error for empty categories, got nil")
	}
	ue, ok := err.(*core.UpdateError)
	if !ok {
		t.Fatalf("expected *core.UpdateError, got %T: %v", err, err)
	}
	if ue.Code != "sophon_manifest_fetch_failed" {
		t.Fatalf("expected sophon_manifest_fetch_failed, got %q", ue.Code)
	}
}

func TestCheckForUpdateSophon_PredlConsume(t *testing.T) {
	// Set up a predl_ready.json so detectPredlConsume returns true → ReasonResumeInterrupted
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// main tag 6.6.0, diffTags includes 6.5.0 so predl-patch is valid
		w.Write([]byte(`{"retcode":0,"message":"","data":{"game_branches":[{"game":{"id":"gopR6Cufr3","biz":"hk4e_global"},"main":{"package_id":"pkg","branch":"main","tag":"6.6.0","diff_tags":["6.5.0"],"categories":[{"category_id":"10016","matching_field":"game","type":"CATEGORY_TYPE_RESOURCE"}]}}]}}`))
	}))
	defer srv.Close()

	p := New(Settings{}, nil)
	p.SetBranchAPIBaseURL(srv.URL)
	tempRoot := t.TempDir()
	gameDir := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")

	// config.ini at 6.5.0 → not idle, not healed
	if err := os.WriteFile(filepath.Join(gameDir, "config.ini"), []byte("[General]\ngame_version=6.5.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Write a predl_ready.json for 6.6.0/6.5.0 sophon_patch
	verDir := versionSidecarDir(tempRoot, gid, "6.6.0")
	if err := os.MkdirAll(verDir, 0o755); err != nil {
		t.Fatal(err)
	}
	ready := sophonPredlReadyFile{
		Kind:          "sophon_patch",
		BuildID:       "build-abc",
		SourceVersion: "6.5.0",
		TargetVersion: "6.6.0",
		PlanSnapshot: sophonPlanSnapshot{
			SophonChunkSources: []sophon.ChunkSource{{Kind: "cdn", ChunkName: "chunk1"}},
		},
	}
	data, _ := json.Marshal(ready)
	if err := os.WriteFile(filepath.Join(verDir, "predl_ready.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}

	p.SetTempRootFn(func(core.GameID) string { return tempRoot })

	plan, err := p.checkForUpdateSophon(context.Background(), gid, gameDir, tempRoot)
	if err != nil {
		t.Fatalf("predl-consume path: unexpected error: %v", err)
	}
	if plan.Reason != core.ReasonResumeInterrupted {
		t.Fatalf("predl-consume: want ReasonResumeInterrupted, got %v", plan.Reason)
	}
}

func TestVerifyPredlStaging_Threshold(t *testing.T) {
	root := t.TempDir()
	chunksDir := filepath.Join(root, "chunks")
	if err := os.MkdirAll(chunksDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// 100 CDN chunks; write good blobs for all, each using MD5 verify path
	// (chunk name "chunk-NNN" does not start with 16 hex chars so stagedChunkOK
	// falls back to ExpectMD5).
	srcs := make([]sophon.ChunkSource, 0, 100)
	for i := 0; i < 100; i++ {
		name := fmt.Sprintf("chunk-%03d", i)
		body := []byte(fmt.Sprintf("payload-%03d", i))
		if err := os.WriteFile(filepath.Join(chunksDir, name), body, 0o644); err != nil {
			t.Fatal(err)
		}
		srcs = append(srcs, sophon.ChunkSource{
			Kind:       sophon.SourceCDN,
			ChunkName:  name,
			DecompSize: int64(len(body)),
			ExpectMD5:  testMD5Hex(body),
		})
	}

	corrupt := func(n int) {
		for i := 0; i < n; i++ {
			_ = os.WriteFile(filepath.Join(chunksDir, fmt.Sprintf("chunk-%03d", i)), []byte("bad"), 0o644)
		}
	}
	// 24% corrupt → keep (discard==false)
	corrupt(24)
	discard, err := verifyPredlStaging(root, srcs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if discard {
		t.Fatalf("24%% CDN fail: want discard=false, got true")
	}
	// exactly 25% corrupt → keep (threshold is strict > 0.25, so 25/100 must NOT discard)
	corrupt(25)
	discard, err = verifyPredlStaging(root, srcs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if discard {
		t.Fatalf("25%% CDN fail: want discard=false (not > 0.25), got true")
	}
	// 26% corrupt → discard (discard==true)
	corrupt(26)
	discard, err = verifyPredlStaging(root, srcs, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !discard {
		t.Fatalf("26%% CDN fail: want discard=true, got false")
	}
}

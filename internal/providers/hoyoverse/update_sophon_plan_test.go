package hoyoverse

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"sync"
	"testing"

	"github.com/klauspost/compress/zstd"
	"google.golang.org/protobuf/proto"

	"omnigate/internal/core"
	"omnigate/internal/providers/hoyoverse/sophon"
	pb "omnigate/internal/providers/hoyoverse/sophon/proto"
)

// ---------------------------------------------------------------------------
// Test scaffolding (INTEGRATOR-NOTE T18-D)
// ---------------------------------------------------------------------------

// storedBlobs holds the in-memory zstd-proto blobs served by /cdn/<id>.
// Keyed by manifest id string. Protected by the httptest server's handler
// closure capture.
type buildServerStore struct {
	mu    sync.Mutex
	blobs map[string][]byte
}

func (s *buildServerStore) put(id string, blob []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.blobs[id] = blob
}

func (s *buildServerStore) get(id string) ([]byte, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.blobs[id]
	return v, ok
}

// zstdProtoMarshal marshals a proto message then zstd-compresses it.
func zstdProtoMarshal(t *testing.T, m proto.Message) []byte {
	t.Helper()
	raw, err := proto.Marshal(m)
	if err != nil {
		t.Fatalf("proto.Marshal: %v", err)
	}
	var buf bytes.Buffer
	enc, err := zstd.NewWriter(&buf)
	if err != nil {
		t.Fatalf("zstd.NewWriter: %v", err)
	}
	if _, err := enc.Write(raw); err != nil {
		t.Fatalf("zstd write: %v", err)
	}
	if err := enc.Close(); err != nil {
		t.Fatalf("zstd close: %v", err)
	}
	return buf.Bytes()
}

// writeBuildEnvelope writes a JSON getBuild-style response into w.
// mainManifest is zstd-proto marshalled, stored in store keyed by manifestID,
// and referenced by the envelope. cdnPrefix is the URL prefix for both
// manifest_download and chunk_download (same server).
// CRITICAL: manifest_download.compression == 1 so FetchManifestRaw returns
// the raw zstd bytes for the §E.2 P2 assertion.
func writeBuildEnvelope(
	t *testing.T,
	w http.ResponseWriter,
	store *buildServerStore,
	srvURL string,
	tag, buildID, manifestID string,
	m *pb.SophonManifestProto,
) {
	t.Helper()
	blob := zstdProtoMarshal(t, m)
	store.put(manifestID, blob)

	type manifestEntry struct {
		CategoryID    string `json:"category_id"`
		MatchingField string `json:"matching_field"`
		Manifest      struct {
			ID             string `json:"id"`
			Checksum       string `json:"checksum"`
			CompressedSize int    `json:"compressed_size"`
		} `json:"manifest"`
		ManifestDownload struct {
			URLPrefix   string `json:"url_prefix"`
			Compression int    `json:"compression"` // 1 = truthy
		} `json:"manifest_download"`
		ChunkDownload struct {
			URLPrefix   string `json:"url_prefix"`
			Compression int    `json:"compression"`
		} `json:"chunk_download"`
		DiffDownload struct {
			URLPrefix string `json:"url_prefix"`
		} `json:"diff_download"`
	}

	entry := manifestEntry{
		CategoryID:    "10016",
		MatchingField: "game",
	}
	entry.Manifest.ID = manifestID
	entry.Manifest.CompressedSize = len(blob)
	entry.ManifestDownload.URLPrefix = srvURL + "/cdn"
	entry.ManifestDownload.Compression = 1 // CRITICAL: truthy so FetchManifestRaw returns wire bytes
	entry.ChunkDownload.URLPrefix = srvURL + "/cdn"
	entry.ChunkDownload.Compression = 1
	entry.DiffDownload.URLPrefix = srvURL + "/cdn"

	type responseData struct {
		BuildID   string          `json:"build_id"`
		Tag       string          `json:"tag"`
		Manifests []manifestEntry `json:"manifests"`
	}
	type envelope struct {
		Retcode int          `json:"retcode"`
		Data    responseData `json:"data"`
	}
	resp := envelope{Retcode: 0, Data: responseData{BuildID: buildID, Tag: tag, Manifests: []manifestEntry{entry}}}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// writePatchEnvelope writes a JSON getPatchBuild-style response into w.
// patchManifest is zstd-proto marshalled, stored in store keyed by patchID.
func writePatchEnvelope(
	t *testing.T,
	w http.ResponseWriter,
	store *buildServerStore,
	srvURL string,
	tag, buildID, patchManifestID string,
	m *pb.SophonPatchProto,
) {
	t.Helper()
	blob := zstdProtoMarshal(t, m)
	store.put(patchManifestID, blob)

	type manifestEntry struct {
		CategoryID    string `json:"category_id"`
		MatchingField string `json:"matching_field"`
		Manifest      struct {
			ID             string `json:"id"`
			Checksum       string `json:"checksum"`
			CompressedSize int    `json:"compressed_size"`
		} `json:"manifest"`
		ManifestDownload struct {
			URLPrefix   string `json:"url_prefix"`
			Compression int    `json:"compression"`
		} `json:"manifest_download"`
		ChunkDownload struct {
			URLPrefix   string `json:"url_prefix"`
			Compression int    `json:"compression"`
		} `json:"chunk_download"`
		DiffDownload struct {
			URLPrefix string `json:"url_prefix"`
		} `json:"diff_download"`
	}

	entry := manifestEntry{
		CategoryID:    "10016",
		MatchingField: "game",
	}
	entry.Manifest.ID = patchManifestID
	entry.Manifest.CompressedSize = len(blob)
	entry.ManifestDownload.URLPrefix = srvURL + "/cdn"
	entry.ManifestDownload.Compression = 1
	entry.ChunkDownload.URLPrefix = srvURL + "/cdn"
	entry.ChunkDownload.Compression = 1
	entry.DiffDownload.URLPrefix = srvURL + "/cdn"

	type responseData struct {
		BuildID   string          `json:"build_id"`
		Tag       string          `json:"tag"`
		PatchID   string          `json:"patch_id"`
		Manifests []manifestEntry `json:"manifests"`
	}
	type envelope struct {
		Retcode int          `json:"retcode"`
		Data    responseData `json:"data"`
	}
	resp := envelope{Retcode: 0, Data: responseData{
		BuildID: buildID, Tag: tag, PatchID: patchManifestID,
		Manifests: []manifestEntry{entry},
	}}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

// serveStoredManifest serves the stored zstd blob for /cdn/<id>.
func serveStoredManifest(t *testing.T, w http.ResponseWriter, r *http.Request, store *buildServerStore) {
	t.Helper()
	// r.URL.Path is like "/cdn/manifest-id" — strip the "/cdn/" prefix.
	id := r.URL.Path
	if len(id) > 5 && id[:5] == "/cdn/" {
		id = id[5:]
	}
	blob, ok := store.get(id)
	if !ok {
		t.Logf("serveStoredManifest: unknown id %q", id)
		w.WriteHeader(http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	_, _ = w.Write(blob)
}

// newSophonBuildServer serves a getBuild envelope for "game" + serves the
// zstd-protobuf manifest at /cdn/<manifest.ID>. If patchManifest != nil it
// ALSO serves getPatchBuild + the patch manifest.
// CRITICAL: distinct manifest IDs for build vs patch so /cdn serves the right blob.
func newSophonBuildServer(t *testing.T, tag string, mainManifest *pb.SophonManifestProto, patchManifest *pb.SophonPatchProto) *httptest.Server {
	t.Helper()
	store := &buildServerStore{blobs: make(map[string][]byte)}

	const buildManifestID = "manifest-main-id"
	const patchManifestID = "manifest-patch-id"

	// We need the server URL to embed in envelopes. Use httptest.NewUnstartedServer
	// and start it after registering handlers that capture the srv variable.
	mux := http.NewServeMux()

	// srv is captured by closure; we assign it just before Start.
	var srvURL string

	mux.HandleFunc("/getBuild", func(w http.ResponseWriter, r *http.Request) {
		writeBuildEnvelope(t, w, store, srvURL, tag, "bid-main", buildManifestID, mainManifest)
	})
	if patchManifest != nil {
		mux.HandleFunc("/getPatchBuild", func(w http.ResponseWriter, r *http.Request) {
			writePatchEnvelope(t, w, store, srvURL, tag, "bid-main", patchManifestID, patchManifest)
		})
	}
	mux.HandleFunc("/cdn/", func(w http.ResponseWriter, r *http.Request) {
		serveStoredManifest(t, w, r, store)
	})

	srv := httptest.NewServer(mux)
	srvURL = srv.URL
	// Pre-populate blobs: the handlers do it lazily on first request, but the
	// buildManifest blob must be stored for /cdn/ lookups. The handlers write it
	// on the first /getBuild request, so lazy is fine — but pre-populate to
	// be safe when /cdn/ is hit before /getBuild (e.g. in patch path where
	// both /getPatchBuild and /getBuild are called).
	// Actually the closures capture store+srvURL and write blobs on demand, so
	// no pre-population needed. Just ensure the test calls /getBuild before /cdn/.
	// The fetch order in buildSophonBuildPlan is: getBuild → then manifest fetch,
	// which is correct.
	return srv
}

// newTestProvider returns a Provider with a real http.Client (no nil client panic).
func newTestProvider(t *testing.T) *Provider {
	t.Helper()
	p := New(Settings{}, nil)
	p.httpClient = &http.Client{}
	return p
}

// ---------------------------------------------------------------------------
// Tests
// ---------------------------------------------------------------------------

func TestMapFoldersToMatchingFields(t *testing.T) {
	got := mapFoldersToMatchingFields([]string{"Chinese", "English(US)", "Japanese", "Korean", "Unknown"})
	want := []string{"en-us", "ja-jp", "ko-kr", "zh-cn"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("mapFolders = %v, want %v", got, want)
	}
	empty := mapFoldersToMatchingFields(nil)
	if empty == nil || len(empty) != 0 {
		t.Fatalf("empty input → %v, want non-nil empty", empty)
	}
}

func TestBuildSophonBuildPlan_FullAllCDN(t *testing.T) {
	asset := &pb.SophonManifestAssetProperty{
		AssetName:    "GenshinImpact_Data/file_a.bin",
		AssetType:    0,
		AssetSize:    20,
		AssetHashMd5: "deadbeef",
		AssetChunks: []*pb.SophonManifestAssetChunk{
			{ChunkName: "chunk0", ChunkDecompressedHashMd5: "m0", ChunkOnFileOffset: 0, ChunkSize: 5, ChunkSizeDecompressed: 10},
			{ChunkName: "chunk1", ChunkDecompressedHashMd5: "m1", ChunkOnFileOffset: 10, ChunkSize: 5, ChunkSizeDecompressed: 10},
		},
	}
	man := &pb.SophonManifestProto{Assets: []*pb.SophonManifestAssetProperty{asset}}
	srv := newSophonBuildServer(t, "6.6.0", man, nil)
	defer srv.Close()

	p := newTestProvider(t)
	p.SetSophonAPIBaseURL(srv.URL)

	gameDir := t.TempDir()
	tempRoot := t.TempDir()
	branch := &sophon.BranchInfo{Main: sophon.BranchSlot{
		PackageID:  "pkg",
		Tag:        "6.6.0",
		Categories: []sophon.Category{{ID: "10016", MatchingField: "game", Type: "CATEGORY_TYPE_RESOURCE"}},
	}}
	gp, predlAvail, err := buildSophonPlan(context.Background(), p, branch, "hoyoverse/genshin", "6.5.0", nil, gameDir, tempRoot)
	if err != nil {
		t.Fatalf("buildSophonPlan: %v", err)
	}
	if gp.flavor != flavorSophonFull {
		t.Fatalf("flavor = %v, want flavorSophonFull", gp.flavor)
	}
	if len(gp.sophonChunkSources) != 2 {
		t.Fatalf("chunk sources = %d, want 2", len(gp.sophonChunkSources))
	}
	for _, s := range gp.sophonChunkSources {
		if s.Kind != sophon.SourceCDN {
			t.Fatalf("chunk kind = %q, want cdn", s.Kind)
		}
	}
	if gp.sophonBuildID == "" {
		t.Fatalf("sophonBuildID empty")
	}
	if predlAvail {
		t.Fatalf("predlAvail = true, want false (no PreDownload)")
	}
	// §E.2 P1 assertion
	if gp.sophonAssetMD5["GenshinImpact_Data/file_a.bin"] != "deadbeef" {
		t.Errorf("sophonAssetMD5 not populated (§E.2 P1): %v", gp.sophonAssetMD5)
	}
	// §E.2 P2 assertion
	if len(gp.sophonRawManifests["game"]) == 0 {
		t.Error("sophonRawManifests[game] empty (§E.2 P2)")
	}
}

func TestBuildSophonPatchPlan_HybridPatchAndMainFallThrough(t *testing.T) {
	main := &pb.SophonManifestProto{Assets: []*pb.SophonManifestAssetProperty{
		{AssetName: "file_a", AssetType: 0, AssetSize: 10, AssetHashMd5: "AA",
			AssetChunks: []*pb.SophonManifestAssetChunk{{ChunkName: "ca", ChunkDecompressedHashMd5: "ca", ChunkOnFileOffset: 0, ChunkSize: 5, ChunkSizeDecompressed: 10}}},
		{AssetName: "file_b", AssetType: 0, AssetSize: 10, AssetHashMd5: "BB",
			AssetChunks: []*pb.SophonManifestAssetChunk{{ChunkName: "cb", ChunkDecompressedHashMd5: "cb", ChunkOnFileOffset: 0, ChunkSize: 5, ChunkSizeDecompressed: 10}}},
		{AssetName: "file_c", AssetType: 0, AssetSize: 10, AssetHashMd5: "CC",
			AssetChunks: []*pb.SophonManifestAssetChunk{{ChunkName: "cc", ChunkDecompressedHashMd5: "cc", ChunkOnFileOffset: 0, ChunkSize: 5, ChunkSizeDecompressed: 10}}},
	}}
	patch := &pb.SophonPatchProto{
		PatchAssets: []*pb.SophonPatchAssetProperty{
			{AssetName: "file_a", AssetSize: 10, AssetHashMd5: "AA", AssetInfos: []*pb.SophonPatchAssetInfo{
				{VersionTag: "6.5.0", Chunk: &pb.SophonPatchAssetChunk{PatchName: "pa", PatchOffset: 0, PatchLength: 4, OriginalFileName: "file_a", OriginalFileMd5: "OLDA"}}}},
			{AssetName: "file_b", AssetSize: 10, AssetHashMd5: "BB", AssetInfos: []*pb.SophonPatchAssetInfo{
				{VersionTag: "6.5.0", Chunk: &pb.SophonPatchAssetChunk{PatchName: "pb", PatchOffset: 0, PatchLength: 10, OriginalFileName: ""}}}},
		},
		UnusedAssets: []*pb.SophonUnusedAssetProperty{
			{VersionTag: "6.5.0", AssetInfos: []*pb.SophonUnusedAssetInfo{
				{Assets: []*pb.SophonUnusedAssetFile{{FileName: "old_dead.bin", FileMd5: "DEAD"}}}}},
		},
	}
	srv := newSophonBuildServer(t, "6.6.0", main, patch)
	defer srv.Close()
	p := newTestProvider(t)
	p.SetSophonAPIBaseURL(srv.URL)

	gameDir := t.TempDir()
	gp := &genshinPlan{
		sophonPatchAssetsFromMain: map[string][]sophon.ChunkSource{},
		sophonAssetMD5:            map[string]string{},
		sophonRawManifests:        map[string][]byte{},
	}
	mainSlot := sophon.BranchSlot{PackageID: "pkg", Tag: "6.6.0", Branch: "main",
		Categories: []sophon.Category{{ID: "10016", MatchingField: "game"}}}
	cats := []sophon.Category{{ID: "10016", MatchingField: "game"}}
	if err := buildSophonPatchPlan(context.Background(), p, gp, mainSlot, "ddxf6vlr1reo", cats, "6.5.0", nil, gameDir); err != nil {
		t.Fatalf("buildSophonPatchPlan: %v", err)
	}
	if len(gp.sophonPatches) != 2 {
		t.Fatalf("patches = %d, want 2", len(gp.sophonPatches))
	}
	var nPatch, nCopy int
	for _, pi := range gp.sophonPatches {
		switch pi.Method {
		case sophon.MethodPatch:
			nPatch++
		case sophon.MethodCopyOver:
			nCopy++
		}
	}
	if nPatch != 1 || nCopy != 1 {
		t.Fatalf("methods patch=%d copy=%d, want 1/1", nPatch, nCopy)
	}
	if len(gp.sophonDeletes) != 1 || gp.sophonDeletes[0].Path != "old_dead.bin" {
		t.Fatalf("deletes = %+v, want 1×old_dead.bin", gp.sophonDeletes)
	}
	if len(gp.sophonChunkSources) == 0 {
		t.Fatalf("expected file_c fall-through chunk sources")
	}
	if _, ok := gp.sophonPatchAssetsFromMain["file_a"]; !ok {
		t.Fatalf("sophonPatchAssetsFromMain missing file_a")
	}
	if _, ok := gp.sophonPatchAssetsFromMain["file_b"]; !ok {
		t.Fatalf("sophonPatchAssetsFromMain missing file_b")
	}
	// §E.2 P1 assertion
	if gp.sophonAssetMD5["file_c"] != "CC" {
		t.Errorf("sophonAssetMD5[file_c] = %q, want CC (fall-through §E.2 P1)", gp.sophonAssetMD5["file_c"])
	}
	// §E.2 P2 assertion
	if len(gp.sophonRawManifests["game"]) == 0 {
		t.Error("sophonRawManifests[game] empty (§E.2 P2)")
	}
}

func TestBuildSophonPlan_PredlFullBlocked(t *testing.T) {
	main := &pb.SophonManifestProto{Assets: []*pb.SophonManifestAssetProperty{
		{AssetName: "f", AssetType: 0, AssetSize: 10, AssetHashMd5: "FF",
			AssetChunks: []*pb.SophonManifestAssetChunk{{ChunkName: "c", ChunkDecompressedHashMd5: "c", ChunkSize: 5, ChunkSizeDecompressed: 10}}}}}
	srv := newSophonBuildServer(t, "6.6.0", main, nil)
	defer srv.Close()
	p := newTestProvider(t)
	p.SetSophonAPIBaseURL(srv.URL)
	branch := &sophon.BranchInfo{
		Main:        sophon.BranchSlot{PackageID: "pkg", Tag: "6.6.0", Categories: []sophon.Category{{ID: "10016", MatchingField: "game"}}},
		PreDownload: sophon.BranchSlot{PackageID: "pkg2", Tag: "6.7.0", Categories: []sophon.Category{{ID: "10016", MatchingField: "game"}}},
	}
	_, predlAvail, err := buildSophonPlan(context.Background(), p, branch, "hoyoverse/genshin", "6.5.0", nil, t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("buildSophonPlan: %v", err)
	}
	if predlAvail {
		t.Fatalf("predlAvail = true, want false (no DiffTags hit, no old manifest)")
	}
}

func TestDetectPredlConsume(t *testing.T) {
	tempRoot := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")
	mainTag := "6.6.0"
	diffTags := []string{"6.5.0"}

	// (a) ENOENT
	consume, pf := detectPredlConsume(tempRoot, gid, "6.5.0", mainTag, diffTags)
	if consume || pf != nil {
		t.Fatalf("ENOENT → (%v,%v), want (false,nil)", consume, pf)
	}

	// (b) good
	writePredlReady(t, tempRoot, gid, mainTag, &sophonPredlReadyFile{
		Kind: "sophon_patch", BuildID: "bid", SourceVersion: "6.5.0", TargetVersion: "6.6.0",
	})
	consume, pf = detectPredlConsume(tempRoot, gid, "6.5.0", mainTag, diffTags)
	if !consume || pf == nil {
		t.Fatalf("good → (%v,%v), want (true,non-nil)", consume, pf)
	}

	// (c) source mismatch → cleanup
	writePredlReady(t, tempRoot, gid, mainTag, &sophonPredlReadyFile{
		Kind: "sophon_patch", BuildID: "bid", SourceVersion: "6.4.0", TargetVersion: "6.6.0",
	})
	consume, _ = detectPredlConsume(tempRoot, gid, "6.5.0", mainTag, diffTags)
	if consume {
		t.Fatalf("source mismatch → consume true, want false")
	}
	if _, err := os.Stat(filepath.Join(versionSidecarDir(tempRoot, gid, mainTag), "predl_ready.json")); !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected predl_ready.json removed on source mismatch")
	}

	// (d) zero-value Kind → stale
	writePredlReady(t, tempRoot, gid, mainTag, &sophonPredlReadyFile{SourceVersion: "6.5.0", TargetVersion: "6.6.0"})
	consume, _ = detectPredlConsume(tempRoot, gid, "6.5.0", mainTag, diffTags)
	if consume {
		t.Fatalf("zero Kind → consume true, want false")
	}
}

func writePredlReady(t *testing.T, tempRoot string, gid core.GameID, targetVer string, f *sophonPredlReadyFile) {
	t.Helper()
	dir := versionSidecarDir(tempRoot, gid, targetVer)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, _ := json.MarshalIndent(f, "", "  ")
	if err := os.WriteFile(filepath.Join(dir, "predl_ready.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

// ensure sort import is used (mapFoldersToMatchingFields uses it indirectly but
// we also use it directly in tests if needed)
var _ = sort.Strings

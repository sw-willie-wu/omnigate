package sophon

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/klauspost/compress/zstd"
	"google.golang.org/protobuf/proto"

	pb "omnigate/internal/providers/hoyoverse/sophon/proto"
)

func zstdCompress(t *testing.T, raw []byte) []byte {
	t.Helper()
	enc, err := zstd.NewWriter(nil)
	if err != nil {
		t.Fatalf("zstd writer: %v", err)
	}
	defer enc.Close()
	return enc.EncodeAll(raw, nil)
}

func newManifestServer(t *testing.T, body []byte) (*httptest.Server, string, string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/M1") {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	}))
	t.Cleanup(srv.Close)
	return srv, srv.URL, "M1"
}

func TestFetchManifestCompressed(t *testing.T) {
	want := &pb.SophonManifestProto{Assets: []*pb.SophonManifestAssetProperty{{
		AssetName: "f0", AssetSize: 9, AssetHashMd5: "h",
		AssetChunks: []*pb.SophonManifestAssetChunk{{ChunkName: "c0", ChunkSize: 3, ChunkSizeDecompressed: 9}},
	}}}
	raw, err := proto.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	srv, base, id := newManifestServer(t, zstdCompress(t, raw))
	_ = srv

	got, err := FetchManifest(context.Background(), http.DefaultClient, ManifestIdentity{
		Manifest:         ManifestFileInfo{ID: id, Checksum: "ck"},
		ManifestDownload: ManifestDownloadInfo{URLPrefix: base, Compression: true},
	})
	if err != nil {
		t.Fatalf("FetchManifest: %v", err)
	}
	if len(got.Assets) != 1 || got.Assets[0].AssetName != "f0" ||
		got.Assets[0].AssetChunks[0].ChunkSizeDecompressed != 9 {
		t.Fatalf("decoded manifest: %+v", got.Assets)
	}
}

func TestFetchManifestRaw(t *testing.T) {
	want := &pb.SophonManifestProto{Assets: []*pb.SophonManifestAssetProperty{{AssetName: "raw0"}}}
	raw, err := proto.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	srv, base, id := newManifestServer(t, raw)
	_ = srv

	got, err := FetchManifest(context.Background(), http.DefaultClient, ManifestIdentity{
		Manifest:         ManifestFileInfo{ID: id},
		ManifestDownload: ManifestDownloadInfo{URLPrefix: base, Compression: false},
	})
	if err != nil {
		t.Fatalf("FetchManifest raw: %v", err)
	}
	if len(got.Assets) != 1 || got.Assets[0].AssetName != "raw0" {
		t.Fatalf("decoded raw manifest: %+v", got.Assets)
	}
}

func TestFetchPatchManifestCompressed(t *testing.T) {
	want := &pb.SophonPatchProto{PatchAssets: []*pb.SophonPatchAssetProperty{{AssetName: "p0", AssetSize: 5}}}
	raw, err := proto.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	srv, base, id := newManifestServer(t, zstdCompress(t, raw))
	_ = srv

	got, err := FetchPatchManifest(context.Background(), http.DefaultClient, ManifestIdentity{
		Manifest:         ManifestFileInfo{ID: id},
		ManifestDownload: ManifestDownloadInfo{URLPrefix: base, Compression: true},
	})
	if err != nil {
		t.Fatalf("FetchPatchManifest: %v", err)
	}
	if len(got.PatchAssets) != 1 || got.PatchAssets[0].AssetName != "p0" {
		t.Fatalf("decoded patch: %+v", got.PatchAssets)
	}
}

func TestFetchManifestHTTPError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "nope", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)
	_, err := FetchManifest(context.Background(), http.DefaultClient, ManifestIdentity{
		Manifest:         ManifestFileInfo{ID: "M1"},
		ManifestDownload: ManifestDownloadInfo{URLPrefix: srv.URL, Compression: true},
	})
	if err == nil {
		t.Fatal("expected error on HTTP 500")
	}
}

func TestFetchManifestRawReturnsWireBytes(t *testing.T) {
	want := &pb.SophonManifestProto{Assets: []*pb.SophonManifestAssetProperty{{AssetName: "wire0"}}}
	raw, err := proto.Marshal(want)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	wire := zstdCompress(t, raw)
	srv, base, id := newManifestServer(t, wire)
	_ = srv
	m, gotWire, err := FetchManifestRaw(context.Background(), http.DefaultClient, ManifestIdentity{
		Manifest:         ManifestFileInfo{ID: id},
		ManifestDownload: ManifestDownloadInfo{URLPrefix: base, Compression: true},
	})
	if err != nil {
		t.Fatalf("FetchManifestRaw: %v", err)
	}
	if len(m.Assets) != 1 || m.Assets[0].AssetName != "wire0" {
		t.Fatalf("decoded: %+v", m.Assets)
	}
	if !bytes.Equal(gotWire, wire) {
		t.Fatalf("raw wire bytes mismatch: got %d bytes, want %d (must be the .pb.zst body, before decompress)", len(gotWire), len(wire))
	}
}

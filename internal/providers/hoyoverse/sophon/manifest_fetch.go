package sophon

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"

	"github.com/klauspost/compress/zstd"
	"google.golang.org/protobuf/proto"

	pb "omnigate/internal/providers/hoyoverse/sophon/proto"
)

// fetchManifestWire GETs <ManifestDownload.URLPrefix>/<Manifest.ID> and returns
// BOTH the raw wire bytes (the .pb.zst body exactly as served, before any
// decompression) and the decompressed protobuf bytes. When
// ManifestDownload.Compression is set the wire body is zstd-compressed and
// decoded is its decompression; otherwise decoded == wire. The manifest
// checksum is NOT verified (Collapse parity).
func fetchManifestWire(ctx context.Context, hc *http.Client, id ManifestIdentity) (wire, decoded []byte, err error) {
	if hc == nil {
		hc = http.DefaultClient
	}
	u := id.ManifestDownload.URLPrefix + "/" + id.Manifest.ID
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, nil, fmt.Errorf("sophon: fetch manifest %q: http %d", id.Manifest.ID, resp.StatusCode)
	}
	wire, err = io.ReadAll(resp.Body)
	if err != nil {
		return nil, nil, fmt.Errorf("sophon: read manifest %q: %w", id.Manifest.ID, err)
	}
	decoded = wire
	if bool(id.ManifestDownload.Compression) {
		zr, zerr := zstd.NewReader(bytes.NewReader(wire))
		if zerr != nil {
			return nil, nil, fmt.Errorf("sophon: zstd reader for manifest %q: %w", id.Manifest.ID, zerr)
		}
		decoded, err = io.ReadAll(zr)
		zr.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("sophon: zstd decode manifest %q: %w", id.Manifest.ID, err)
		}
	}
	return wire, decoded, nil
}

// FetchManifestRaw fetches a getBuild category manifest, returning BOTH the
// decoded SophonManifestProto AND the raw wire bytes (the .pb.zst body as
// served, before decompression). The hoyoverse layer persists the raw bytes as
// the applied-manifest dedup cache so a later update rebuilds the per-asset MD5
// index without re-fetching (§E.2 P2).
func FetchManifestRaw(ctx context.Context, hc *http.Client, id ManifestIdentity) (*pb.SophonManifestProto, []byte, error) {
	wire, decoded, err := fetchManifestWire(ctx, hc, id)
	if err != nil {
		return nil, nil, err
	}
	var m pb.SophonManifestProto
	if err := proto.Unmarshal(decoded, &m); err != nil {
		return nil, nil, fmt.Errorf("sophon: unmarshal manifest %q: %w", id.Manifest.ID, err)
	}
	slog.Debug("sophon: manifest decoded (checksum not verified)",
		"manifest_id", id.Manifest.ID, "category", id.MatchingField,
		"declared_checksum", id.Manifest.Checksum, "assets", len(m.Assets))
	return &m, wire, nil
}

// FetchManifest fetches and decodes a SophonManifestProto, discarding the raw
// wire bytes. Use FetchManifestRaw when the .pb.zst body must be persisted.
func FetchManifest(ctx context.Context, hc *http.Client, id ManifestIdentity) (*pb.SophonManifestProto, error) {
	m, _, err := FetchManifestRaw(ctx, hc, id)
	return m, err
}

// FetchPatchManifest fetches and decodes a SophonPatchProto (getPatchBuild
// category). Patch manifests are not persisted (only main manifests feed the
// dedup cache), so no Raw variant is needed.
func FetchPatchManifest(ctx context.Context, hc *http.Client, id ManifestIdentity) (*pb.SophonPatchProto, error) {
	_, decoded, err := fetchManifestWire(ctx, hc, id)
	if err != nil {
		return nil, err
	}
	var m pb.SophonPatchProto
	if err := proto.Unmarshal(decoded, &m); err != nil {
		return nil, fmt.Errorf("sophon: unmarshal patch manifest %q: %w", id.Manifest.ID, err)
	}
	slog.Debug("sophon: patch manifest decoded (checksum not verified)",
		"manifest_id", id.Manifest.ID, "category", id.MatchingField,
		"declared_checksum", id.Manifest.Checksum, "patch_assets", len(m.PatchAssets))
	return &m, nil
}

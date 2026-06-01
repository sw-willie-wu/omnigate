// internal/providers/hoyoverse/sophon/dedup.go
package sophon

import (
	pb "omnigate/internal/providers/hoyoverse/sophon/proto"
)

// BuildPerAssetMD5Index builds a per-asset chunk-reuse index for Path-B
// chunk-from-disk dedup. It finds the asset named assetName in oldManifest and
// maps each of its chunks' ChunkDecompressedHashMd5 to a ChunkRef pointing at
// the old asset's on-disk location (path == old AssetName) and the chunk's
// ChunkOnFileOffset.
//
// Scope is strictly per-asset: a chunk belonging to another asset is never
// reachable from a query for assetName (Collapse SophonUpdate
// .GetChunkOldOffsetFromOld parity, spec §0 chunk-reuse keying).
//
// A nil manifest, an empty manifest, or a missing asset all yield an empty
// (non-nil) map. If the same MD5 appears on more than one chunk within the
// asset, the last chunk in AssetChunks order wins (documented; the chunks are
// byte-identical by MD5 so either offset is a valid source).
func BuildPerAssetMD5Index(oldManifest *pb.SophonManifestProto, assetName string) map[string]ChunkRef {
	idx := make(map[string]ChunkRef)
	if oldManifest == nil {
		return idx
	}
	for _, asset := range oldManifest.Assets {
		if asset == nil || asset.AssetName != assetName {
			continue
		}
		for _, chunk := range asset.AssetChunks {
			if chunk == nil {
				continue
			}
			idx[chunk.ChunkDecompressedHashMd5] = ChunkRef{
				OldFilePath: asset.AssetName,
				OldOffset:   chunk.ChunkOnFileOffset,
			}
		}
		// Per-asset scope: first matching asset only; stop scanning.
		break
	}
	return idx
}

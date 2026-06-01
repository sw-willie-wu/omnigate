package sophon

import (
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	pb "omnigate/internal/providers/hoyoverse/sophon/proto"
)

// benchMD5hex is a bench-local md5 helper (renamed from the plan's md5hex to
// avoid a duplicate-definition error with chunk_download_test.go's md5hex).
func benchMD5hex(b []byte) string { s := md5.Sum(b); return hex.EncodeToString(s[:]) }

func bigAsset(nChunks int) *pb.SophonManifestProto {
	a := &pb.SophonManifestAssetProperty{AssetName: "data/big.bin"}
	var off int64
	for i := 0; i < nChunks; i++ {
		a.AssetChunks = append(a.AssetChunks, &pb.SophonManifestAssetChunk{
			ChunkName:                fmt.Sprintf("chunk_%05d", i),
			ChunkDecompressedHashMd5: benchMD5hex([]byte(fmt.Sprintf("payload-%d", i))),
			ChunkOnFileOffset:        off,
			ChunkSizeDecompressed:    4096,
		})
		off += 4096
	}
	return &pb.SophonManifestProto{Assets: []*pb.SophonManifestAssetProperty{a}}
}

// BenchmarkBuildPerAssetMD5Index_LargeAsset: 500-chunk asset, expect < 5 ms.
func BenchmarkBuildPerAssetMD5Index_LargeAsset(b *testing.B) {
	m := bigAsset(500)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = BuildPerAssetMD5Index(m, "data/big.bin")
	}
}

// BenchmarkDedupLookup_1MOps: expect > 5M lookups/sec.
func BenchmarkDedupLookup_1MOps(b *testing.B) {
	m := bigAsset(1000)
	idx := BuildPerAssetMD5Index(m, "data/big.bin")
	keys := make([]string, 0, len(idx))
	for k := range idx {
		keys = append(keys, k)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = idx[keys[i%len(keys)]]
	}
}

// BenchmarkChunkAssemble_3GBFile: disk-bound sanity check. Uses a smaller
// synthetic file by default (set OMNIGATE_BENCH_3GB=1 for the full 3 GB run);
// CI runs the small variant to keep wall-time bounded.
func BenchmarkChunkAssemble_3GBFile(b *testing.B) {
	size := int64(64 << 20) // 64 MiB default
	if os.Getenv("OMNIGATE_BENCH_3GB") == "1" {
		size = 3 << 30
	}
	chunkSize := int64(4 << 20)
	dir := b.TempDir()
	payload := make([]byte, chunkSize)
	for i := range payload {
		payload[i] = byte(i)
	}
	var srcs []ChunkSource
	var off int64
	staging := filepath.Join(dir, "chunks")
	_ = os.MkdirAll(staging, 0o755)
	for off < size {
		name := fmt.Sprintf("c_%d", off)
		_ = os.WriteFile(filepath.Join(staging, name), payload, 0o644)
		srcs = append(srcs, ChunkSource{
			Kind: SourceCDN, ChunkName: name, FileOffset: off, DecompSize: chunkSize,
		})
		off += chunkSize
	}
	read := func(src ChunkSource) ([]byte, error) {
		return os.ReadFile(filepath.Join(staging, src.ChunkName))
	}
	b.SetBytes(size)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		out := filepath.Join(dir, fmt.Sprintf("out_%d.bin", i))
		if err := AssembleFile(out, size, srcs, read); err != nil {
			b.Fatal(err)
		}
	}
}

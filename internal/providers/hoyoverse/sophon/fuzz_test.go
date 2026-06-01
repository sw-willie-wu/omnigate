package sophon

import (
	"bytes"
	"io"
	"testing"

	"github.com/klauspost/compress/zstd"
	"google.golang.org/protobuf/proto"

	pb "omnigate/internal/providers/hoyoverse/sophon/proto"
)

func zstdComp(in []byte) []byte {
	var buf bytes.Buffer
	enc, _ := zstd.NewWriter(&buf)
	_, _ = enc.Write(in)
	_ = enc.Close()
	return buf.Bytes()
}

// FuzzSophonManifestProto_Parse: arbitrary (and seeded valid-zstd-proto) bytes
// through the manifest parse path must never panic.
func FuzzSophonManifestProto_Parse(f *testing.F) {
	good, _ := proto.Marshal(&pb.SophonManifestProto{
		Assets: []*pb.SophonManifestAssetProperty{{
			AssetName:   "data/x.bin",
			AssetChunks: []*pb.SophonManifestAssetChunk{{ChunkName: "c0", ChunkSize: 4}},
		}},
	})
	f.Add(zstdComp(good))
	f.Add([]byte{})
	f.Add([]byte("not-zstd"))
	f.Fuzz(func(t *testing.T, data []byte) {
		dec, err := zstd.NewReader(bytes.NewReader(data))
		if err != nil {
			return
		}
		defer dec.Close()
		raw, err := io.ReadAll(dec)
		if err != nil {
			return
		}
		var m pb.SophonManifestProto
		_ = proto.Unmarshal(raw, &m) // must not panic
	})
}

// FuzzSophonPatchProto_Parse: same for the patch proto.
func FuzzSophonPatchProto_Parse(f *testing.F) {
	good, _ := proto.Marshal(&pb.SophonPatchProto{
		PatchAssets: []*pb.SophonPatchAssetProperty{{AssetName: "data/x.bin"}},
	})
	f.Add(zstdComp(good))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		dec, err := zstd.NewReader(bytes.NewReader(data))
		if err != nil {
			return
		}
		defer dec.Close()
		raw, err := io.ReadAll(dec)
		if err != nil {
			return
		}
		var pp pb.SophonPatchProto
		_ = proto.Unmarshal(raw, &pp)
	})
}

// FuzzPatchBlobSlice: random PatchOffset/PatchLength against random blobs must
// never index out of bounds. Mirrors the slice extraction in §6.4 step 3 /
// §6.5 step 2 (read blob[off:off+len]).
func FuzzPatchBlobSlice(f *testing.F) {
	f.Add([]byte("hello world"), int64(2), int64(5))
	f.Add([]byte{}, int64(0), int64(0))
	f.Fuzz(func(t *testing.T, blob []byte, off, length int64) {
		// Bounds-safe extraction identical to the production guard the
		// implementer MUST use (clamp + validate before slicing).
		if off < 0 || length < 0 || off > int64(len(blob)) || off+length > int64(len(blob)) {
			return // production code returns an error here, never slices
		}
		_ = blob[off : off+length] // must not panic given the guard above
	})
}

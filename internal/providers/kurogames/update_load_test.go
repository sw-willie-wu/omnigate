//go:build load

package kurogames

import (
	"compress/gzip"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// loadFixtureFiles parses testdata/large_manifest.json.gz into []manifestFileRaw
// for the load benchmarks. Uses the actual indexFileRaw shape from
// update_manifest.go.
func loadFixtureFiles(b *testing.B) []manifestFileRaw {
	b.Helper()
	f, err := os.Open("testdata/large_manifest.json.gz")
	if err != nil {
		b.Fatalf("fixture missing — run `go run testdata/gen_large_manifest.go`: %v", err)
	}
	defer f.Close()
	gr, err := gzip.NewReader(f)
	if err != nil {
		b.Fatal(err)
	}
	defer gr.Close()
	var mf indexFileRaw
	if err := json.NewDecoder(gr).Decode(&mf); err != nil {
		b.Fatal(err)
	}
	if len(mf.Resource) != 1000 {
		b.Fatalf("fixture has %d entries, want 1000", len(mf.Resource))
	}
	return mf.Resource
}

// BenchmarkProgressAppend_1000Entries: 1000 sequential MarkComplete calls;
// spec §7.9 budget ≤50ms per iteration.
func BenchmarkProgressAppend_1000Entries(b *testing.B) {
	files := loadFixtureFiles(b)
	for i := 0; i < b.N; i++ {
		tmp := b.TempDir()
		ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
		if err := ps.Init("etag-bench"); err != nil {
			b.Fatal(err)
		}
		start := time.Now()
		for _, f := range files {
			if err := ps.MarkComplete(f.Dest, time.Now(), f.Size); err != nil {
				b.Fatal(err)
			}
		}
		elapsed := time.Since(start)
		if elapsed > 50*time.Millisecond {
			b.Logf("iter %d: 1000 MarkComplete took %v, want ≤50ms (spec §7.9)", i, elapsed)
		}
	}
}

// BenchmarkSidecarParse_LargeProgress: parse 1000-entry progress.json;
// spec §7.9 budget ≤50ms per iteration.
func BenchmarkSidecarParse_LargeProgress(b *testing.B) {
	files := loadFixtureFiles(b)
	tmp := b.TempDir()
	ps := newProgressStore(tmp, "kurogames/wuwa", "3.4.0")
	if err := ps.Init("etag-bench"); err != nil {
		b.Fatal(err)
	}
	for _, f := range files {
		if err := ps.MarkComplete(f.Dest, time.Now(), f.Size); err != nil {
			b.Fatal(err)
		}
	}
	progressPath := filepath.Join(ps.dir(), "progress.json")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		start := time.Now()
		pf, err := loadProgressFile(progressPath)
		elapsed := time.Since(start)
		if err != nil {
			b.Fatal(err)
		}
		if len(pf.Entries) != 1000 {
			b.Fatalf("entries = %d, want 1000", len(pf.Entries))
		}
		if elapsed > 50*time.Millisecond {
			b.Logf("iter %d: parse 1000-entry progress took %v, want ≤50ms (spec §7.9)", i, elapsed)
		}
	}
}

// BenchmarkApplyLoop_RenameOnly: 1000 zero-byte file renames; baseline cost.
func BenchmarkApplyLoop_RenameOnly(b *testing.B) {
	files := loadFixtureFiles(b)
	for i := 0; i < b.N; i++ {
		srcRoot := b.TempDir()
		dstRoot := b.TempDir()
		for _, f := range files {
			p := filepath.Join(srcRoot, f.Dest)
			if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
				b.Fatal(err)
			}
			if err := os.WriteFile(p, nil, 0o644); err != nil {
				b.Fatal(err)
			}
		}
		b.ResetTimer()
		start := time.Now()
		for _, f := range files {
			src := filepath.Join(srcRoot, f.Dest)
			dst := filepath.Join(dstRoot, f.Dest)
			if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
				b.Fatal(err)
			}
			if err := os.Rename(src, dst); err != nil {
				b.Fatal(err)
			}
		}
		elapsed := time.Since(start)
		b.Logf("iter %d: 1000 rename took %v", i, elapsed)
		_ = fmt.Sprint(elapsed)
	}
}

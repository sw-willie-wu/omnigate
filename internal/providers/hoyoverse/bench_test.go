package hoyoverse

import (
	"path/filepath"
	"testing"
	"time"

	"omnigate/internal/core"
)

func BenchmarkProgressStoreMarkComplete_1k(b *testing.B) {
	tmp := b.TempDir()
	gid := core.GameID("hoyoverse/genshin")
	ps, err := newProgressStore(tmp, gid, "5.7.0", "etag")
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N && i < 1000; i++ {
		_ = ps.MarkComplete("blob-"+filepath.Base(tmp), 1024, time.Now(), "abcd")
	}
}

func BenchmarkApplyWAL_500_Files(b *testing.B) {
	tmp := b.TempDir()
	pending := make([]string, 500)
	for i := 0; i < 500; i++ {
		pending[i] = "file-" + filepath.Base(tmp) + ".dll"
	}
	wal := &applyWAL{Pending: pending, GameID: "hoyoverse/genshin", Version: "5.7.0"}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = writeApplyWAL(tmp, wal)
	}
}

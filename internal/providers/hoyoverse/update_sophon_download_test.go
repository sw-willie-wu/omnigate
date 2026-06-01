package hoyoverse

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"omnigate/internal/core"
	"omnigate/internal/providers/hoyoverse/sophon"
)

// newTestProgressStore creates a fresh sophonProgressStore in a temp dir.
func newTestProgressStore(t *testing.T) (*sophonProgressStore, string) {
	t.Helper()
	tmp := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")
	st, err := newSophonProgressStore(tmp, gid, "6.6.0", "main", "buildTest")
	if err != nil {
		t.Fatalf("newSophonProgressStore: %v", err)
	}
	return st, tmp
}

// stubExec returns a sophonExecutors where all three funcs succeed immediately
// and write a marker file at `out` so tests can verify the path was exercised.
func stubExecWriteMarker() sophonExecutors {
	write := func(out string) error {
		if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
			return err
		}
		return os.WriteFile(out, []byte("marker"), 0o644)
	}
	return sophonExecutors{
		downloadChunk: func(_ context.Context, src sophon.ChunkSource, out string) error {
			return write(out)
		},
		readLocalChunk: func(_ string, src sophon.ChunkSource, out string) error {
			return write(out)
		},
		downloadPatch: func(_ context.Context, p sophon.PatchInstr, out string) error {
			return write(out)
		},
	}
}

// TestDownloadAllSophon_SkipsDone: chunk 0 pre-marked done; stub CDN returns
// success for chunks 1 & 2.  Assert no error, chunk-0 staging file NOT created.
func TestDownloadAllSophon_SkipsDone(t *testing.T) {
	store, tmp := newTestProgressStore(t)

	sources := []sophon.ChunkSource{
		{Kind: sophon.SourceCDN, ChunkName: "chunk-0", DecompSize: 100},
		{Kind: sophon.SourceCDN, ChunkName: "chunk-1", DecompSize: 200},
		{Kind: sophon.SourceCDN, ChunkName: "chunk-2", DecompSize: 300},
	}

	// Pre-mark chunk-0 done.
	if err := store.MarkChunkDone("chunk-0"); err != nil {
		t.Fatal(err)
	}

	stagingRoot := filepath.Join(tmp, "staging")
	exec := stubExecWriteMarker()

	var progressCalls int64
	if err := downloadAllSophon(
		context.Background(), store,
		t.TempDir(), stagingRoot,
		sources, nil, 4, exec,
		func(int64) { atomic.AddInt64(&progressCalls, 1) },
	); err != nil {
		t.Fatalf("downloadAllSophon: %v", err)
	}

	// chunk-0 staging file must NOT have been written by the pool.
	chunk0Path := filepath.Join(stagingRoot, "chunks", "chunk-0")
	if _, err := os.Stat(chunk0Path); !os.IsNotExist(err) {
		t.Errorf("expected chunk-0 staging file absent (skipped), got stat err: %v", err)
	}
	// chunks 1 & 2 must be present.
	for _, name := range []string{"chunk-1", "chunk-2"} {
		p := filepath.Join(stagingRoot, "chunks", name)
		if _, err := os.Stat(p); err != nil {
			t.Errorf("expected %s staging file present: %v", name, err)
		}
	}
	// Progress callback fired at least 3 times (1 skip + 2 downloads).
	if progressCalls < 3 {
		t.Errorf("expected >=3 progress calls, got %d", progressCalls)
	}
}

// TestDownloadAllSophon_DispatchByKind: mix of 2 CDN + 1 local + 1 patch; stub
// executors record which kind ran for each name; assert all four used correct path
// and store marks all done.
func TestDownloadAllSophon_DispatchByKind(t *testing.T) {
	store, tmp := newTestProgressStore(t)
	stagingRoot := filepath.Join(tmp, "staging")

	var mu sync.Mutex
	cdnCalled := map[string]bool{}
	localCalled := map[string]bool{}
	patchCalled := map[string]bool{}

	exec := sophonExecutors{
		downloadChunk: func(_ context.Context, src sophon.ChunkSource, out string) error {
			mu.Lock()
			cdnCalled[src.ChunkName] = true
			mu.Unlock()
			return os.WriteFile(out, []byte("cdn"), 0o644)
		},
		readLocalChunk: func(_ string, src sophon.ChunkSource, out string) error {
			mu.Lock()
			localCalled[src.ChunkName] = true
			mu.Unlock()
			return os.WriteFile(out, []byte("local"), 0o644)
		},
		downloadPatch: func(_ context.Context, p sophon.PatchInstr, out string) error {
			mu.Lock()
			patchCalled[p.PatchName] = true
			mu.Unlock()
			return os.WriteFile(out, []byte("patch"), 0o644)
		},
	}

	sources := []sophon.ChunkSource{
		{Kind: sophon.SourceCDN, ChunkName: "cdn-a", DecompSize: 10},
		{Kind: sophon.SourceCDN, ChunkName: "cdn-b", DecompSize: 10},
		{Kind: sophon.SourceLocal, ChunkName: "local-a", OldFile: "old.bin", DecompSize: 10},
	}
	patches := []sophon.PatchInstr{
		{Method: sophon.MethodPatch, PatchName: "patch-a", PatchSize: 10},
	}

	// Pre-create dirs so WriteFile in stub succeeds.
	_ = os.MkdirAll(filepath.Join(stagingRoot, "chunks"), 0o755)
	_ = os.MkdirAll(filepath.Join(stagingRoot, "patches"), 0o755)

	if err := downloadAllSophon(
		context.Background(), store,
		t.TempDir(), stagingRoot,
		sources, patches, 4, exec, nil,
	); err != nil {
		t.Fatalf("downloadAllSophon: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if !cdnCalled["cdn-a"] || !cdnCalled["cdn-b"] {
		t.Errorf("CDN chunks not dispatched: %v", cdnCalled)
	}
	if !localCalled["local-a"] {
		t.Errorf("local chunk not dispatched: %v", localCalled)
	}
	if !patchCalled["patch-a"] {
		t.Errorf("patch not dispatched: %v", patchCalled)
	}
	// All four should now be marked done.
	if !store.ChunkDone("cdn-a") || !store.ChunkDone("cdn-b") || !store.ChunkDone("local-a") {
		t.Error("not all chunks marked done in store")
	}
	if !store.PatchDone("patch-a") {
		t.Error("patch-a not marked done in store")
	}
}

// TestDownloadAllSophon_LocalStaleRequeuesCDN: readLocalChunk returns ErrChunkStale;
// downloadChunk must be called for that chunk and it ends up marked done.
func TestDownloadAllSophon_LocalStaleRequeuesCDN(t *testing.T) {
	store, tmp := newTestProgressStore(t)
	stagingRoot := filepath.Join(tmp, "staging")
	_ = os.MkdirAll(filepath.Join(stagingRoot, "chunks"), 0o755)

	var cdnCalled atomic.Bool
	exec := sophonExecutors{
		downloadChunk: func(_ context.Context, src sophon.ChunkSource, out string) error {
			cdnCalled.Store(true)
			return os.WriteFile(out, []byte("cdn-fallback"), 0o644)
		},
		readLocalChunk: func(_ string, src sophon.ChunkSource, out string) error {
			return sophon.ErrChunkStale // simulate stale local
		},
		downloadPatch: func(_ context.Context, p sophon.PatchInstr, out string) error {
			return nil
		},
	}

	sources := []sophon.ChunkSource{
		{Kind: sophon.SourceLocal, ChunkName: "stale-chunk", URLPrefix: "http://cdn", OldFile: "game.bin", DecompSize: 50},
	}

	if err := downloadAllSophon(
		context.Background(), store,
		t.TempDir(), stagingRoot,
		sources, nil, 2, exec, nil,
	); err != nil {
		t.Fatalf("downloadAllSophon: %v", err)
	}

	if !cdnCalled.Load() {
		t.Error("expected downloadChunk invoked for stale local chunk requeue")
	}
	if !store.ChunkDone("stale-chunk") {
		t.Error("stale-chunk not marked done after CDN fallback")
	}
}

// TestDownloadAllSophon_Cancel: many jobs; downloadChunk blocks until ctx cancelled;
// assert returns context.Canceled and pool drains (no goroutine leak).
func TestDownloadAllSophon_Cancel(t *testing.T) {
	store, tmp := newTestProgressStore(t)
	stagingRoot := filepath.Join(tmp, "staging")

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	cancelCtx, cancelFn := context.WithCancel(ctx)

	var started atomic.Int64
	exec := sophonExecutors{
		downloadChunk: func(ctx context.Context, src sophon.ChunkSource, out string) error {
			started.Add(1)
			// Signal that we are inside a worker, then block until cancelled.
			<-ctx.Done()
			return ctx.Err()
		},
		readLocalChunk: nil,
		downloadPatch:  nil,
	}

	const numChunks = 20
	sources := make([]sophon.ChunkSource, numChunks)
	for i := range sources {
		sources[i] = sophon.ChunkSource{Kind: sophon.SourceCDN, ChunkName: "c" + string(rune('a'+i%26)), DecompSize: 10}
	}

	done := make(chan error, 1)
	go func() {
		done <- downloadAllSophon(cancelCtx, store, t.TempDir(), stagingRoot, sources, nil, 4, exec, nil)
	}()

	// Wait for at least one worker to start, then cancel.
	deadline := time.Now().Add(2 * time.Second)
	for started.Load() == 0 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	cancelFn()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Errorf("expected context.Canceled, got %v", err)
		}
	case <-time.After(4 * time.Second):
		t.Fatal("downloadAllSophon did not return after cancel (goroutine leak?)")
	}
}

// TestDownloadAllSophon_ResumeFromPartial: create store, mark 3/5 chunks done +
// persist; reopen store; run pool; assert only 2 not-done chunks downloaded and
// progress includes the 3 skipped (their DecompSize).
func TestDownloadAllSophon_ResumeFromPartial(t *testing.T) {
	tmp := t.TempDir()
	gid := core.GameID("hoyoverse/genshin")

	// First session: create store and mark 3 chunks done.
	store1, err := newSophonProgressStore(tmp, gid, "6.6.0", "main", "buildTest")
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"chunk-0", "chunk-2", "chunk-4"} {
		if err := store1.MarkChunkDone(name); err != nil {
			t.Fatalf("MarkChunkDone(%s): %v", name, err)
		}
	}

	// Second session: reopen from same path — must pick up persisted progress.
	store2, err := newSophonProgressStore(tmp, gid, "6.6.0", "main", "buildTest")
	if err != nil {
		t.Fatal(err)
	}

	stagingRoot := filepath.Join(tmp, "staging")
	_ = os.MkdirAll(filepath.Join(stagingRoot, "chunks"), 0o755)

	var mu sync.Mutex
	downloaded := map[string]bool{}
	exec := sophonExecutors{
		downloadChunk: func(_ context.Context, src sophon.ChunkSource, out string) error {
			mu.Lock()
			downloaded[src.ChunkName] = true
			mu.Unlock()
			return os.WriteFile(out, []byte("bytes"), 0o644)
		},
		readLocalChunk: nil,
		downloadPatch:  nil,
	}

	sources := []sophon.ChunkSource{
		{Kind: sophon.SourceCDN, ChunkName: "chunk-0", DecompSize: 100},
		{Kind: sophon.SourceCDN, ChunkName: "chunk-1", DecompSize: 200},
		{Kind: sophon.SourceCDN, ChunkName: "chunk-2", DecompSize: 300},
		{Kind: sophon.SourceCDN, ChunkName: "chunk-3", DecompSize: 400},
		{Kind: sophon.SourceCDN, ChunkName: "chunk-4", DecompSize: 500},
	}

	// Collect all onProgress call values to verify skipped bytes are reported.
	var progressMu sync.Mutex
	var progressValues []int64
	if err := downloadAllSophon(
		context.Background(), store2,
		t.TempDir(), stagingRoot,
		sources, nil, 2, exec,
		func(v int64) {
			progressMu.Lock()
			progressValues = append(progressValues, v)
			progressMu.Unlock()
		},
	); err != nil {
		t.Fatalf("downloadAllSophon: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()

	// Only chunk-1 and chunk-3 should have been downloaded.
	for _, name := range []string{"chunk-0", "chunk-2", "chunk-4"} {
		if downloaded[name] {
			t.Errorf("%s was re-downloaded but should have been skipped (resume)", name)
		}
	}
	for _, name := range []string{"chunk-1", "chunk-3"} {
		if !downloaded[name] {
			t.Errorf("%s was NOT downloaded but should have been", name)
		}
	}

	// onProgress should have been called at least 5 times:
	// 3 skip calls (raw DecompSize: 100, 300, 500) + 2 worker calls (cumulative).
	progressMu.Lock()
	nCalls := len(progressValues)
	progressMu.Unlock()
	if nCalls < 5 {
		t.Errorf("expected >=5 progress calls (3 skips + 2 downloads), got %d", nCalls)
	}

	// Skipped DecompSize values (100, 300, 500) must appear in the call list.
	progressMu.Lock()
	seen := map[int64]bool{}
	for _, v := range progressValues {
		seen[v] = true
	}
	progressMu.Unlock()
	for _, want := range []int64{100, 300, 500} {
		if !seen[want] {
			t.Errorf("expected progress call with skipped DecompSize=%d, not found in %v", want, progressValues)
		}
	}
}

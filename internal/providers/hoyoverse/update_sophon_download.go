package hoyoverse

import (
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"sync"

	"omnigate/internal/core"
	"omnigate/internal/providers/hoyoverse/sophon"
)

type sophonJobKind int

const (
	jobChunkCDN   sophonJobKind = iota
	jobChunkLocal               // local-read; stale → requeue as CDN inline
	jobPatchBlob
)

type sophonJob struct {
	kind  sophonJobKind
	chunk sophon.ChunkSource // jobChunkCDN | jobChunkLocal
	patch sophon.PatchInstr  // jobPatchBlob
	out   string             // staging path
}

// sophonExecutors abstracts the three sophon-layer I/O primitives so the
// worker pool can be unit-tested without crafting verify-passing byte vectors
// (end-to-end verify is covered by Task 23). Production wiring uses
// defaultSophonExecutors.
type sophonExecutors struct {
	downloadChunk  func(ctx context.Context, src sophon.ChunkSource, out string) error
	readLocalChunk func(gameDir string, src sophon.ChunkSource, out string) error
	downloadPatch  func(ctx context.Context, p sophon.PatchInstr, out string) error
}

// defaultSophonExecutors wires the three real sophon-layer functions.
// Task 21 passes p.httpClientOrDefault() here.
func defaultSophonExecutors(hc *http.Client) sophonExecutors {
	return sophonExecutors{
		downloadChunk: func(ctx context.Context, src sophon.ChunkSource, out string) error {
			return sophon.DownloadChunk(ctx, hc, src, out)
		},
		readLocalChunk: func(gameDir string, src sophon.ChunkSource, out string) error {
			return sophon.ReadLocalChunk(gameDir, src, out)
		},
		downloadPatch: func(ctx context.Context, p sophon.PatchInstr, out string) error {
			return sophon.DownloadPatchBlob(ctx, hc, p, out)
		},
	}
}

// downloadAllSophon runs a 4-worker (configurable) pool over the plan's chunk
// sources + patch blobs, writing verified content into stagingRoot/chunks and
// stagingRoot/patches. Crash-resume: chunks/patches already in store are
// skipped. Local-chunk ErrChunkStale → requeue as a CDN job (the planner
// always populates ChunkName/URLPrefix on Local sources per §A.2). Duplicate
// chunk requests are tolerated (atomic rename + verify make them idempotent).
// Progress reports cumulative decompressed bytes (§5.3).
func downloadAllSophon(
	ctx context.Context,
	store *sophonProgressStore,
	gameDir, stagingRoot string,
	sources []sophon.ChunkSource,
	patches []sophon.PatchInstr,
	workers int,
	exec sophonExecutors,
	onProgress func(int64),
) error {
	if workers < 1 {
		workers = 1
	}
	chunksDir := filepath.Join(stagingRoot, "chunks")
	patchesDir := filepath.Join(stagingRoot, "patches")
	if err := os.MkdirAll(chunksDir, 0o755); err != nil {
		return err
	}
	if err := os.MkdirAll(patchesDir, 0o755); err != nil {
		return err
	}

	// progress accumulates cumulative decompressed bytes and emits the running
	// total via onProgress (§5.3). Declared before the job-building loop so
	// skip-done paths can route through the same accumulator as workers.
	var bytesDone int64
	var bytesMu sync.Mutex
	var doneMu sync.Mutex // guards store mutation (MarkChunkDone/MarkPatchDone already lock internally, but doneMu serialises the pair call + our read under one lock)
	progress := func(delta int64) {
		bytesMu.Lock()
		bytesDone += delta
		v := bytesDone
		bytesMu.Unlock()
		if onProgress != nil {
			onProgress(v)
		}
	}

	// Build job list (dedup chunks by ChunkName, patches by PatchName).
	var jobs []sophonJob
	seenChunk := map[string]bool{}
	for _, src := range sources {
		if seenChunk[src.ChunkName] {
			continue
		}
		seenChunk[src.ChunkName] = true
		if store.ChunkDone(src.ChunkName) { // OVERRIDE 2: singular ChunkDone (Task 15)
			progress(src.DecompSize)
			continue
		}
		kind := jobChunkCDN
		if src.Kind == sophon.SourceLocal {
			kind = jobChunkLocal
		}
		jobs = append(jobs, sophonJob{kind: kind, chunk: src, out: filepath.Join(chunksDir, src.ChunkName)})
	}
	seenPatch := map[string]bool{}
	for _, p := range patches {
		if seenPatch[p.PatchName] {
			continue
		}
		seenPatch[p.PatchName] = true
		if store.PatchDone(p.PatchName) { // OVERRIDE 2: singular PatchDone (Task 15)
			progress(p.PatchSize)
			continue
		}
		// §E pin 11: one patch-blob job per unique PatchName regardless of
		// Method (both MethodPatch and MethodCopyOver need the blob on disk).
		jobs = append(jobs, sophonJob{kind: jobPatchBlob, patch: p, out: filepath.Join(patchesDir, p.PatchName)})
	}
	if len(jobs) == 0 {
		return nil
	}

	jobCh := make(chan sophonJob)
	errCh := make(chan error, workers)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobCh {
				if err := runSophonJob(ctx, job, gameDir, chunksDir, exec, store, &doneMu, progress); err != nil {
					select {
					case errCh <- err:
					default:
					}
					return
				}
			}
		}()
	}

	go func() {
		defer close(jobCh)
		for _, j := range jobs {
			select {
			case <-ctx.Done():
				return
			case jobCh <- j:
			}
		}
	}()

	wg.Wait()
	close(errCh)
	if err := ctx.Err(); err != nil {
		return err
	}
	for err := range errCh {
		if err != nil {
			return err
		}
	}
	return nil
}

// runSophonJob executes one job, marking the store on success.
// OVERRIDE 3: markChunkDone/markPatchDone return error; no separate Persist().
func runSophonJob(
	ctx context.Context,
	job sophonJob,
	gameDir, chunksDir string,
	exec sophonExecutors,
	store *sophonProgressStore,
	doneMu *sync.Mutex,
	progress func(int64),
) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	switch job.kind {
	case jobChunkCDN:
		if err := exec.downloadChunk(ctx, job.chunk, job.out); err != nil {
			return wrapSophonChunkErr(job.chunk.ChunkName, err)
		}
		if err := markChunkDone(store, doneMu, job.chunk.ChunkName); err != nil {
			return err
		}
		progress(job.chunk.DecompSize)
	case jobChunkLocal:
		err := exec.readLocalChunk(gameDir, job.chunk, job.out)
		if errors.Is(err, sophon.ErrChunkStale) {
			// Requeue inline as CDN (planner populated ChunkName/URLPrefix on Local sources).
			if cdnErr := exec.downloadChunk(ctx, job.chunk, job.out); cdnErr != nil {
				return wrapSophonChunkErr(job.chunk.ChunkName, cdnErr)
			}
		} else if err != nil {
			return wrapSophonChunkErr(job.chunk.ChunkName, err)
		}
		if err := markChunkDone(store, doneMu, job.chunk.ChunkName); err != nil {
			return err
		}
		progress(job.chunk.DecompSize)
	case jobPatchBlob:
		if err := exec.downloadPatch(ctx, job.patch, job.out); err != nil {
			return wrapSophonChunkErr(job.patch.PatchName, err)
		}
		if err := markPatchDone(store, doneMu, job.patch.PatchName); err != nil {
			return err
		}
		progress(job.patch.PatchSize)
	}
	return nil
}

// markChunkDone locks doneMu and calls MarkChunkDone (which persists internally
// per Task 15; no separate Persist() call needed — OVERRIDE 3).
func markChunkDone(store *sophonProgressStore, mu *sync.Mutex, name string) error {
	mu.Lock()
	defer mu.Unlock()
	return store.MarkChunkDone(name) // Task 15 MarkChunkDone persists internally; no separate Persist().
}

// markPatchDone locks doneMu and calls MarkPatchDone (persists internally per Task 15).
func markPatchDone(store *sophonProgressStore, mu *sync.Mutex, name string) error {
	mu.Lock()
	defer mu.Unlock()
	return store.MarkPatchDone(name)
}

// wrapSophonChunkErr converts a verify-exhausted error into the user-facing
// retryable code; ctx errors and stale errors pass through unchanged.
func wrapSophonChunkErr(name string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, sophon.ErrChunkVerify) {
		return &core.UpdateError{
			Code:      "sophon_chunk_verify_failed",
			Params:    map[string]string{"file": name},
			Retryable: true,
		}
	}
	return err
}

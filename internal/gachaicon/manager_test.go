package gachaicon

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"omnigate/internal/core"
)

func TestServeIcon_FetchesThenCachesOnDisk(t *testing.T) {
	var hits int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("PNGDATA"))
	}))
	defer srv.Close()
	m := NewManager(t.TempDir(), nil)
	idx := NewIndex()
	idx.put(core.GameID("hoyoverse/genshin"), "綾華", Entry{ID: "1", IconRef: "X", Kind: "char"})
	m.swap(core.GameID("hoyoverse/genshin"), idx)
	m.urlFn = func(gid core.GameID, kind, ref string) string { return srv.URL }
	b, mime, err := m.ServeIcon(core.GameID("hoyoverse/genshin"), "char", "1")
	if err != nil || string(b) != "PNGDATA" || mime != "image/png" {
		t.Fatalf("serve = %q %q %v", b, mime, err)
	}
	if _, _, err := m.ServeIcon(core.GameID("hoyoverse/genshin"), "char", "1"); err != nil {
		t.Fatal(err)
	}
	if hits != 1 {
		t.Errorf("network hits = %d, want 1 (disk cache hit on 2nd)", hits)
	}
}

func TestServeIcon_UnknownID_Errors(t *testing.T) {
	m := NewManager(t.TempDir(), nil)
	m.swap(core.GameID("hoyoverse/genshin"), NewIndex())
	if _, _, err := m.ServeIcon(core.GameID("hoyoverse/genshin"), "char", "999"); err == nil {
		t.Errorf("expected error for unknown id")
	}
}

func TestServeIcon_WriteFailureStillServes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("OK"))
	}))
	defer srv.Close()
	// Use a cacheDir whose parent is a regular FILE so MkdirAll/WriteFile fail
	// reliably on Windows (a NUL-byte path is not portable here).
	bad := filepath.Join(t.TempDir(), "afile")
	if err := os.WriteFile(bad, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	m := NewManager(bad, nil) // cacheDir = <bad-file>/.cache/gachaicons → unwritable
	idx := NewIndex()
	idx.put(core.GameID("hoyoverse/genshin"), "a", Entry{ID: "1", IconRef: "X", Kind: "char"})
	m.swap(core.GameID("hoyoverse/genshin"), idx)
	m.urlFn = func(core.GameID, string, string) string { return srv.URL }
	if b, _, err := m.ServeIcon(core.GameID("hoyoverse/genshin"), "char", "1"); err != nil || string(b) != "OK" {
		t.Fatalf("write-fail path should still serve: %q %v", b, err)
	}
}

func TestSnapshotSwap_ConcurrentReadsNoCrash(t *testing.T) {
	m := NewManager(t.TempDir(), nil)
	gid := core.GameID("hoyoverse/genshin")
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); m.snapshot(gid) }()
		wg.Add(1)
		go func() { defer wg.Done(); m.swap(gid, NewIndex()) }()
	}
	wg.Wait()
}

func TestServeIcon_ConcurrentSameID_SingleFetch(t *testing.T) {
	var mu sync.Mutex
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		hits++
		mu.Unlock()
		w.Header().Set("Content-Type", "image/png")
		w.Write([]byte("PNGDATA"))
	}))
	defer srv.Close()
	m := NewManager(t.TempDir(), nil)
	idx := NewIndex()
	idx.put(core.GameID("hoyoverse/genshin"), "a", Entry{ID: "1", IconRef: "X", Kind: "char"})
	m.swap(core.GameID("hoyoverse/genshin"), idx)
	m.urlFn = func(core.GameID, string, string) string { return srv.URL }
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			b, _, err := m.ServeIcon(core.GameID("hoyoverse/genshin"), "char", "1")
			if err != nil || string(b) != "PNGDATA" {
				t.Errorf("serve=%q %v", b, err)
			}
		}()
	}
	wg.Wait()
	if hits != 1 {
		t.Errorf("hits=%d want 1 (single-flight)", hits)
	}
}

// --- Step 5: Warm / WarmAsync / persistence / callbacks ---

func TestWarm_InFlightGuard_SingleFetch(t *testing.T) {
	m := NewManager(t.TempDir(), nil)
	gid := core.GameID("hoyoverse/genshin")
	var mu sync.Mutex
	calls := 0
	release := make(chan struct{})
	started := make(chan struct{})
	m.fetchFn = func(_ *Manager, _ core.GameID) (*Index, error) {
		mu.Lock()
		calls++
		mu.Unlock()
		started <- struct{}{}
		<-release // block so the in-flight flag stays set
		return NewIndex(), nil
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() { defer wg.Done(); _ = m.Warm(gid) }()
	<-started // first fetch is in-flight, warming flag is set
	// Second Warm runs synchronously while the first is blocked → must be guarded.
	if err := m.Warm(gid); err != nil {
		t.Fatalf("guarded Warm err=%v", err)
	}
	mu.Lock()
	got := calls
	mu.Unlock()
	if got != 1 {
		t.Errorf("during-flight calls=%d want 1 (in-flight guard)", got)
	}
	close(release)
	wg.Wait()
	mu.Lock()
	got = calls
	mu.Unlock()
	if got != 1 {
		t.Errorf("final calls=%d want 1", got)
	}
}

func TestWarm_InvokesOnWarmOnce_NilCallbackSafe(t *testing.T) {
	gid := core.GameID("hoyoverse/genshin")

	// nil callback: must not panic.
	m0 := NewManager(t.TempDir(), nil)
	m0.fetchFn = func(_ *Manager, _ core.GameID) (*Index, error) { return NewIndex(), nil }
	if err := m0.Warm(gid); err != nil {
		t.Fatalf("Warm with nil callback err=%v", err)
	}

	m := NewManager(t.TempDir(), nil)
	m.fetchFn = func(_ *Manager, _ core.GameID) (*Index, error) { return NewIndex(), nil }
	var n int32
	m.SetOnWarm(func(core.GameID) { atomic.AddInt32(&n, 1) })
	if err := m.Warm(gid); err != nil {
		t.Fatalf("Warm err=%v", err)
	}
	if n != 1 {
		t.Errorf("onWarm called %d times, want 1", n)
	}
}

func TestWarm_PersistsAndLazyLoadsFromDisk(t *testing.T) {
	dir := t.TempDir()
	gid := core.GameID("kurogames/wutheringwaves")

	m := NewManager(dir, nil)
	m.fetchFn = func(_ *Manager, _ core.GameID) (*Index, error) {
		idx := NewIndex()
		idx.put(gid, "今汐", Entry{ID: "1404", IconRef: "T_IconRoleHead256_1404", Kind: "char"})
		idx.putEquip(gid, "裁春", Entry{ID: "21020026", IconRef: "w", Kind: "weapon"})
		return idx, nil
	}
	if err := m.Warm(gid); err != nil {
		t.Fatalf("Warm err=%v", err)
	}

	// A brand-new Manager over the SAME dir must lazy-load the persisted index.
	m2 := NewManager(dir, nil)
	if e, ok := m2.Resolve(gid, "今汐", false); !ok || e.ID != "1404" {
		t.Errorf("lazy-load char resolve = %+v ok=%v", e, ok)
	}
	if e, ok := m2.Resolve(gid, "裁春", true); !ok || e.ID != "21020026" || e.Kind != "weapon" {
		t.Errorf("lazy-load weapon resolve = %+v ok=%v", e, ok)
	}
	// EntryByID round-trips too (byID rebuilt on load).
	if e, ok := m2.snapshot(gid).EntryByID(gid, "1404"); !ok || e.IconRef != "T_IconRoleHead256_1404" {
		t.Errorf("lazy-load EntryByID = %+v ok=%v", e, ok)
	}
}

func TestWarmAsync_FreshSkipsFetch(t *testing.T) {
	m := NewManager(t.TempDir(), nil)
	gid := core.GameID("hoyoverse/genshin")
	// fetchFn signals on this buffered channel whenever it is invoked.
	fetched := make(chan struct{}, 1)
	m.fetchFn = func(_ *Manager, _ core.GameID) (*Index, error) {
		fetched <- struct{}{}
		return NewIndex(), nil
	}
	// First warm populates index + fetchedAt(now) and fires the legitimate signal.
	if err := m.Warm(gid); err != nil {
		t.Fatal(err)
	}
	<-fetched // drain the expected first-warm signal
	// WarmAsync on a FRESH index must NOT spawn another fetch. Any spurious warm
	// would send on the channel (from its background goroutine); none must arrive.
	m.WarmAsync(gid)
	select {
	case <-fetched:
		t.Error("fresh index must skip WarmAsync fetch, but fetchFn was called")
	case <-time.After(50 * time.Millisecond):
		// no fetch spawned — correct
	}
}

func TestNilManager_WarmAndWarmAsyncAreNoops(t *testing.T) {
	var m *Manager
	if err := m.Warm(core.GameID("hoyoverse/genshin")); err != nil {
		t.Errorf("nil Warm err=%v want nil", err)
	}
	m.WarmAsync(core.GameID("hoyoverse/genshin")) // must not panic
}

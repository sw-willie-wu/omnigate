package hypergryph

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestCurrentBgURL_FallsBackToDefaultWhenNoCache(t *testing.T) {
	// On Windows os.UserCacheDir reads %LOCALAPPDATA%; redirect it.
	t.Setenv("LOCALAPPDATA", t.TempDir())
	got := CurrentBgURL(nil)
	if got != defaultBgURL {
		t.Errorf("CurrentBgURL on empty FS = %q, want defaultBgURL", got)
	}
}

func TestFindCachedBgURL_PicksLastMatchInNewestFile(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LOCALAPPDATA", tmp)
	// %LOCALAPPDATA%\Games\<hash>\cache\Cache\data_N
	base := filepath.Join(tmp, "Games", "deadbeef", "cache", "Cache")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	// data_2 is older with one match.
	older := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	if err := os.WriteFile(filepath.Join(base, "data_2"),
		[]byte(`prefix https://gl-utils-public.hg-cdn.com/hg-utils/prod/AAAA/YDUTE5gscDZ229CW/aa/bb/00000000000000000000000000000001.webp suffix`),
		0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(filepath.Join(base, "data_2"), older, older); err != nil {
		t.Fatal(err)
	}
	// data_1 is newer with two matches; last match should win.
	if err := os.WriteFile(filepath.Join(base, "data_1"),
		[]byte(`A https://gl-utils-public.hg-cdn.com/hg-utils/prod/AAAA/YDUTE5gscDZ229CW/cc/dd/00000000000000000000000000000002.png middle `+
			`https://gl-utils-public.hg-cdn.com/hg-utils/prod/BBBB/YDUTE5gscDZ229CW/ee/ff/abababababababababababababababab.webp tail`),
		0o644); err != nil {
		t.Fatal(err)
	}
	got := findCachedBgURL(nil)
	want := "https://gl-utils-public.hg-cdn.com/hg-utils/prod/BBBB/YDUTE5gscDZ229CW/ee/ff/abababababababababababababababab.webp"
	if got != want {
		t.Errorf("findCachedBgURL = %q, want %q", got, want)
	}
}

func TestFindCachedBgURL_IgnoresOtherGameFolders(t *testing.T) {
	tmp := t.TempDir()
	t.Setenv("LOCALAPPDATA", tmp)
	base := filepath.Join(tmp, "Games", "x", "cache", "Cache")
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatal(err)
	}
	// POPUCOM's game-folder URL — should NOT match.
	if err := os.WriteFile(filepath.Join(base, "data_1"),
		[]byte(`https://gl-utils-public.hg-cdn.com/hg-utils/prod/AAAA/FtQqkyFLX4Z0bg8G/ab/cd/0000000000000000000000000000000c.webp`),
		0o644); err != nil {
		t.Fatal(err)
	}
	if got := findCachedBgURL(nil); got != "" {
		t.Errorf("findCachedBgURL matched non-Endfield URL: %q", got)
	}
}

package dirver

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMaxIn_PicksHighest(t *testing.T) {
	tmp := t.TempDir()
	mustMkdir(t, filepath.Join(tmp, "1.0.0"))
	mustMkdir(t, filepath.Join(tmp, "1.2.3"))
	mustMkdir(t, filepath.Join(tmp, "1.10.0"))
	mustMkdir(t, filepath.Join(tmp, "0.9.9"))

	got, err := MaxIn(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.10.0" {
		t.Errorf("MaxIn = %q, want 1.10.0 (numeric, not lex)", got)
	}
}

func TestMaxIn_HandlesFourPart(t *testing.T) {
	tmp := t.TempDir()
	mustMkdir(t, filepath.Join(tmp, "2.5.0.1"))
	mustMkdir(t, filepath.Join(tmp, "2.6.1.0"))
	mustMkdir(t, filepath.Join(tmp, "2.5.0.0"))

	got, err := MaxIn(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if got != "2.6.1.0" {
		t.Errorf("MaxIn = %q, want 2.6.1.0", got)
	}
}

func TestMaxIn_FiltersNonVersionDirs(t *testing.T) {
	tmp := t.TempDir()
	mustMkdir(t, filepath.Join(tmp, "1.2.3"))
	mustMkdir(t, filepath.Join(tmp, "Cache"))           // not a version
	mustMkdir(t, filepath.Join(tmp, "Wuthering Waves Game")) // not a version
	mustMkdir(t, filepath.Join(tmp, "kr_game_cache"))   // not a version
	// also place a file with a version-like name
	if err := os.WriteFile(filepath.Join(tmp, "2.0.0"), []byte("file"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := MaxIn(tmp)
	if err != nil {
		t.Fatal(err)
	}
	if got != "1.2.3" {
		t.Errorf("MaxIn = %q, want 1.2.3 (file named 2.0.0 must be ignored)", got)
	}
}

func TestMaxIn_EmptyReturnsEmpty(t *testing.T) {
	tmp := t.TempDir()
	got, err := MaxIn(tmp)
	if err != nil {
		t.Fatalf("expected nil error on empty dir, got %v", err)
	}
	if got != "" {
		t.Errorf("MaxIn empty dir = %q, want empty", got)
	}
}

func TestMaxIn_NonExistentReturnsEmpty(t *testing.T) {
	got, err := MaxIn(filepath.Join(os.TempDir(), "definitely-does-not-exist-omnigate"))
	if err != nil {
		t.Errorf("unexpected error on non-existent dir: %v", err)
	}
	if got != "" {
		t.Errorf("MaxIn non-existent = %q, want empty", got)
	}
}

func TestMaxIn_MixedThreeAndFourPart(t *testing.T) {
	// Defensive: a dir with both 3- and 4-part children should still find max
	tmp := t.TempDir()
	mustMkdir(t, filepath.Join(tmp, "1.2.3"))
	mustMkdir(t, filepath.Join(tmp, "1.2.3.5"))
	got, err := MaxIn(tmp)
	if err != nil {
		t.Fatal(err)
	}
	// 1.2.3.5 > 1.2.3 (treating missing 4th part as 0)
	if got != "1.2.3.5" {
		t.Errorf("MaxIn = %q, want 1.2.3.5", got)
	}
}

func mustMkdir(t *testing.T, p string) {
	t.Helper()
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
}

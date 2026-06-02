package hypergryph

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestDetectInstall_FindsEndfield(t *testing.T) {
	tmp := t.TempDir()
	gameDir := filepath.Join(tmp, "games", "EndField Game")
	if err := os.MkdirAll(gameDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(gameDir, "Endfield.exe"), []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := DetectInstall(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d, want 1", len(got))
	}
	if got[0].GameID != "hypergryph/endfield" {
		t.Errorf("GameID = %q", got[0].GameID)
	}
	if got[0].InstallPath != gameDir {
		t.Errorf("InstallPath = %q", got[0].InstallPath)
	}
}

func TestDetectInstall_MissingPathReturnsEmpty(t *testing.T) {
	got, err := DetectInstall(context.Background(), `C:\does\not\exist`)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %d, want 0", len(got))
	}
}

func TestDetectInstall_FolderWithoutExeIsSkipped(t *testing.T) {
	tmp := t.TempDir()
	gameDir := filepath.Join(tmp, "games", "EndField Game")
	if err := os.MkdirAll(gameDir, 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := DetectInstall(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %d, want 0 (no exe)", len(got))
	}
}

func TestDefaultScan_UsesDefaultRoot(t *testing.T) {
	p := New(Settings{}, nil)
	got, err := p.DefaultScan(context.Background())
	if err != nil {
		t.Fatalf("DefaultScan err: %v", err)
	}
	if got == nil {
		t.Fatalf("DefaultScan returned nil map (want non-nil, possibly empty)")
	}
}

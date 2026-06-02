package kurogames

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"omnigate/internal/core"
)

func TestDetectInstall_FindsWuwa(t *testing.T) {
	tmp := t.TempDir()
	gameDir := filepath.Join(tmp, "Wuthering Waves Game")
	if err := os.MkdirAll(gameDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// game requires the exe to exist (consistent with M1 detect heuristic
	// being more than just folder presence — adjust if you choose folder-only)
	if err := os.WriteFile(filepath.Join(gameDir, "Wuthering Waves.exe"), []byte("stub"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := DetectInstall(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("got %d installed, want 1", len(got))
	}
	if got[0].GameID != "kurogames/wutheringwaves" {
		t.Errorf("GameID = %q", got[0].GameID)
	}
	if got[0].InstallPath != gameDir {
		t.Errorf("InstallPath = %q, want %q", got[0].InstallPath, gameDir)
	}
}

func TestDetectInstall_MissingPathReturnsEmpty(t *testing.T) {
	got, err := DetectInstall(context.Background(), `C:\path\that\definitely\does\not\exist`)
	if err != nil {
		t.Errorf("expected nil err, got %v", err)
	}
	if len(got) != 0 {
		t.Errorf("got %d installed, want 0", len(got))
	}
}

func TestDetectInstall_FolderWithoutExeIsSkipped(t *testing.T) {
	tmp := t.TempDir()
	gameDir := filepath.Join(tmp, "Wuthering Waves Game")
	if err := os.MkdirAll(gameDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// no .exe inside
	got, err := DetectInstall(context.Background(), tmp)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Errorf("got %d, want 0 (folder without exe should not count as installed)", len(got))
	}
	_ = core.GameID("") // silence unused import in some setups
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

package kurogames

import (
	"encoding/json"
	"omnigate/internal/core"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestProgress_WriteAndLoadEntry(t *testing.T) {
	tmp := t.TempDir()
	p := newProgressStore(tmp, "kurogames/wutheringwaves", "3.4.0")
	if err := p.Init("etag-abc"); err != nil {
		t.Fatal(err)
	}
	mt := time.Now().Truncate(time.Millisecond)
	if err := p.MarkComplete("Engine/foo.dll", mt, 12345); err != nil {
		t.Fatal(err)
	}
	loaded, err := core.LoadProgress(p.dir())
	if err != nil {
		t.Fatal(err)
	}
	if loaded.ETag != "etag-abc" {
		t.Errorf("ETag = %q", loaded.ETag)
	}
	e, ok := loaded.Entries["Engine/foo.dll"]
	if !ok {
		t.Fatal("entry not loaded")
	}
	if e.Size != 12345 || !e.MTime.Equal(mt) {
		t.Errorf("entry mismatch: %+v", e)
	}
}

func TestProgress_AtomicWrite(t *testing.T) {
	tmp := t.TempDir()
	p := newProgressStore(tmp, "kurogames/wutheringwaves", "3.4.0")
	if err := p.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	mainPath := filepath.Join(p.dir(), "progress.json")
	tmpPath := mainPath + ".tmp"
	if _, err := os.Stat(mainPath); err != nil {
		t.Errorf("progress.json missing: %v", err)
	}
	if _, err := os.Stat(tmpPath); err == nil {
		t.Errorf(".tmp should be cleaned")
	}
}

func TestProgress_RenameToPredlReady(t *testing.T) {
	tmp := t.TempDir()
	p := newProgressStore(tmp, "kurogames/wutheringwaves", "3.4.0")
	if err := p.Init("etag-1"); err != nil {
		t.Fatal(err)
	}
	if err := p.MarkComplete("a.dll", time.Now(), 1); err != nil {
		t.Fatal(err)
	}
	if err := p.RenameToPredlReady(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(p.dir(), "progress.json")); err == nil {
		t.Errorf("progress.json should be gone")
	}
	body, _ := os.ReadFile(filepath.Join(p.dir(), "predl_ready.json"))
	var v struct {
		ETag string `json:"etag"`
	}
	if err := json.Unmarshal(body, &v); err != nil {
		t.Fatalf("predl_ready.json malformed: %v", err)
	}
	if v.ETag != "etag-1" {
		t.Errorf("ETag = %q", v.ETag)
	}
}


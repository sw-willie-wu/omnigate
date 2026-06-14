package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestBrowseForImage_NilCtxGuard(t *testing.T) {
	a := &App{} // ctx nil
	got, err := a.BrowseForImage("")
	if err != nil || got != "" {
		t.Errorf("BrowseForImage nil-ctx = (%q,%v), want (\"\",nil)", got, err)
	}
}

func TestGetCustomBackground_EmptyPath(t *testing.T) {
	a := &App{settings: Settings{Games: map[string]GameSettings{}}}
	got, err := a.GetCustomBackground("hoyoverse/genshin")
	if err != nil || got != "" {
		t.Errorf("empty path = (%q,%v), want (\"\",nil)", got, err)
	}
}

func TestGetCustomBackground_Missing(t *testing.T) {
	a := &App{settings: Settings{Games: map[string]GameSettings{
		"hoyoverse/genshin": {BackgroundPath: `Z:\nope.png`},
	}}}
	if _, err := a.GetCustomBackground("hoyoverse/genshin"); err == nil {
		t.Error("missing file: want error, got nil")
	}
}

func TestGetCustomBackground_ReadsDataURL(t *testing.T) {
	tmp := t.TempDir()
	png := filepath.Join(tmp, "bg.png")
	if err := os.WriteFile(png, []byte("\x89PNG\r\n\x1a\nDATA"), 0o644); err != nil {
		t.Fatal(err)
	}
	a := &App{settings: Settings{Games: map[string]GameSettings{
		"hoyoverse/genshin": {BackgroundPath: png},
	}}}
	got, err := a.GetCustomBackground("hoyoverse/genshin")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(got, "data:image/png;base64,") {
		t.Errorf("data URL prefix wrong: %.40q", got)
	}
}

func TestGetCustomBackground_WebpAndBmpMime(t *testing.T) {
	tmp := t.TempDir()
	for ext, wantMime := range map[string]string{
		".webp": "image/webp", ".bmp": "image/bmp",
		".JPG": "image/jpeg", ".jpeg": "image/jpeg", ".PNG": "image/png", // case-insensitivity + jpg/jpeg alias
	} {
		f := filepath.Join(tmp, "x"+ext)
		if err := os.WriteFile(f, []byte("xx"), 0o644); err != nil {
			t.Fatal(err)
		}
		a := &App{settings: Settings{Games: map[string]GameSettings{"g/x": {BackgroundPath: f}}}}
		got, err := a.GetCustomBackground("g/x")
		if err != nil {
			t.Fatalf("%s: %v", ext, err)
		}
		if !strings.HasPrefix(got, "data:"+wantMime+";base64,") {
			t.Errorf("%s mime: %.30q want %s", ext, got, wantMime)
		}
	}
}

func TestGetCustomBackground_UnknownExtRejected(t *testing.T) {
	tmp := t.TempDir()
	f := filepath.Join(tmp, "x.gif")
	_ = os.WriteFile(f, []byte("x"), 0o644)
	a := &App{settings: Settings{Games: map[string]GameSettings{"g/x": {BackgroundPath: f}}}}
	if _, err := a.GetCustomBackground("g/x"); err == nil {
		t.Error("unknown ext: want error")
	}
}

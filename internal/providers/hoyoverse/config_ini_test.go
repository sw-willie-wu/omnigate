package hoyoverse

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadGameVersion_Happy(t *testing.T) {
	dir := t.TempDir()
	body := "[General]\nchannel=1\nsub_channel=1\ncps=mihoyo\ngame_version=5.6.0\n"
	if err := os.WriteFile(filepath.Join(dir, "config.ini"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadGameVersion(dir)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "5.6.0" {
		t.Errorf("got %q want 5.6.0", got)
	}
}

func TestReadGameVersion_MissingFile(t *testing.T) {
	_, err := ReadGameVersion(t.TempDir())
	if err == nil {
		t.Error("expected error for missing config.ini")
	}
}

func TestReadGameVersion_MissingGeneralSection(t *testing.T) {
	dir := t.TempDir()
	body := "[Other]\ngame_version=5.6.0\n"
	if err := os.WriteFile(filepath.Join(dir, "config.ini"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadGameVersion(dir)
	if err == nil {
		t.Error("expected error when [General] is absent")
	}
}

func TestReadGameVersion_MissingKey(t *testing.T) {
	dir := t.TempDir()
	body := "[General]\nchannel=1\n"
	if err := os.WriteFile(filepath.Join(dir, "config.ini"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ReadGameVersion(dir)
	if err == nil {
		t.Error("expected error when game_version key absent")
	}
}

func TestReadGameVersion_BOM(t *testing.T) {
	dir := t.TempDir()
	body := "\xef\xbb\xbf[General]\ngame_version=5.6.0\n"
	if err := os.WriteFile(filepath.Join(dir, "config.ini"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadGameVersion(dir)
	if err != nil {
		t.Fatalf("err with BOM: %v", err)
	}
	if got != "5.6.0" {
		t.Errorf("got %q want 5.6.0", got)
	}
}

func TestReadGameVersion_CRLF(t *testing.T) {
	dir := t.TempDir()
	body := "[General]\r\ngame_version=5.6.0\r\n"
	if err := os.WriteFile(filepath.Join(dir, "config.ini"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadGameVersion(dir)
	if err != nil {
		t.Fatalf("err with CRLF: %v", err)
	}
	if got != "5.6.0" {
		t.Errorf("got %q want 5.6.0", got)
	}
}

func TestReadGameVersion_CommentsAndKeyOnlyLines(t *testing.T) {
	dir := t.TempDir()
	body := "; comment line\n[General]\n# another comment\nflag_only_no_equals\ngame_version=5.6.0\n"
	if err := os.WriteFile(filepath.Join(dir, "config.ini"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err := ReadGameVersion(dir)
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if got != "5.6.0" {
		t.Errorf("got %q want 5.6.0", got)
	}
}

func TestWriteGameVersion_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	body := "[General]\nchannel=1\nsub_channel=1\ncps=mihoyo\ngame_version=5.6.0\n"
	if err := os.WriteFile(filepath.Join(dir, "config.ini"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteGameVersion(dir, "5.7.0"); err != nil {
		t.Fatalf("write err: %v", err)
	}
	got, err := ReadGameVersion(dir)
	if err != nil {
		t.Fatalf("read err: %v", err)
	}
	if got != "5.7.0" {
		t.Errorf("got %q want 5.7.0", got)
	}
	// Other keys preserved
	data, err := os.ReadFile(filepath.Join(dir, "config.ini"))
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"channel=1", "sub_channel=1", "cps=mihoyo"} {
		if !strings.Contains(string(data), key) {
			t.Errorf("expected key %q preserved; got:\n%s", key, string(data))
		}
	}
}

func TestWriteGameVersion_PreservesCRLFLineEndings(t *testing.T) {
	dir := t.TempDir()
	body := "[General]\r\nchannel=1\r\ngame_version=5.6.0\r\n"
	if err := os.WriteFile(filepath.Join(dir, "config.ini"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := WriteGameVersion(dir, "5.7.0"); err != nil {
		t.Fatalf("write err: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "config.ini"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "\r\n") {
		t.Errorf("expected CRLF line endings preserved; got:\n%q", string(data))
	}
}

func TestWriteGameVersion_MissingFileError(t *testing.T) {
	if err := WriteGameVersion(t.TempDir(), "5.7.0"); err == nil {
		t.Error("expected error when writing to missing config.ini")
	}
}

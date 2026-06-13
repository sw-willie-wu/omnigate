package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRotatingFileWritesToFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "omnigate.log")

	rf, err := OpenRotatingFile(path, 1<<20)
	if err != nil {
		t.Fatalf("OpenRotatingFile: %v", err)
	}
	if _, err := rf.Write([]byte("hello\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if err := rf.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(got) != "hello\n" {
		t.Fatalf("file contents = %q, want %q", got, "hello\n")
	}
}

func TestRotatingFileAppendsToExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "omnigate.log")
	if err := os.WriteFile(path, []byte("old\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	rf, err := OpenRotatingFile(path, 1<<20)
	if err != nil {
		t.Fatalf("OpenRotatingFile: %v", err)
	}
	rf.Write([]byte("new\n"))
	rf.Close()

	got, _ := os.ReadFile(path)
	if string(got) != "old\nnew\n" {
		t.Fatalf("file contents = %q, want %q", got, "old\nnew\n")
	}
}

func TestRotatingFileRotatesWhenOverCap(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "omnigate.log")

	// cap of 10 bytes; first write fits, second pushes over -> rotate first.
	rf, err := OpenRotatingFile(path, 10)
	if err != nil {
		t.Fatalf("OpenRotatingFile: %v", err)
	}
	rf.Write([]byte("aaaaaa\n")) // 7 bytes, fits
	rf.Write([]byte("bbbbbb\n")) // 7 + 7 = 14 > 10 -> rotate, then write into fresh file
	rf.Close()

	// Active log holds only the post-rotation write.
	active, _ := os.ReadFile(path)
	if string(active) != "bbbbbb\n" {
		t.Fatalf("active log = %q, want %q", active, "bbbbbb\n")
	}
	// Backup holds the pre-rotation content.
	backup, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatalf("backup not created: %v", err)
	}
	if string(backup) != "aaaaaa\n" {
		t.Fatalf("backup = %q, want %q", backup, "aaaaaa\n")
	}
}

func TestRotatingFileBackupOverwrittenOnSecondRotate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "omnigate.log")

	rf, _ := OpenRotatingFile(path, 10)
	rf.Write([]byte("first.\n")) // 7
	rf.Write([]byte("second\n")) // rotate -> .1 = "first."
	rf.Write([]byte("third.\n")) // rotate -> .1 = "second", active = "third."
	rf.Close()

	backup, _ := os.ReadFile(path + ".1")
	if strings.TrimSpace(string(backup)) != "second" {
		t.Fatalf("backup = %q, want it to hold the most recent rotated content %q", backup, "second\n")
	}
}

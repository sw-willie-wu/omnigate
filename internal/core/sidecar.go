package core

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// LoadProgress parses progress.json from the given dir.
func LoadProgress(dir string) (*ProgressFile, error) {
	return loadProgressFile(filepath.Join(dir, "progress.json"))
}

// LoadProgressFromPath parses a sidecar ProgressFile (progress.json or
// predl_ready.json — same schema) from an explicit path. Used by App
// layer's ResumeInterrupted ETag drift check.
func LoadProgressFromPath(path string) (*ProgressFile, error) {
	return loadProgressFile(path)
}

// ReadWALETag returns the ETag recorded in apply.wal's header line.
// Returns "" if file missing/unreadable/header malformed. Used by App
// layer's ResumeInterrupted ETag drift check.
//
// WAL format: line 1 is JSON header `{"etag":"<value>","plan_files":[...]}`;
// subsequent lines are `<relpath> OK\n` per applied file.
func ReadWALETag(path string) string {
	body, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	nl := bytes.IndexByte(body, '\n')
	if nl < 0 {
		nl = len(body)
	}
	var hdr struct {
		ETag string `json:"etag"`
	}
	if err := json.Unmarshal(body[:nl], &hdr); err != nil {
		return ""
	}
	return hdr.ETag
}

func loadProgressFile(path string) (*ProgressFile, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var pf ProgressFile
	if err := json.Unmarshal(body, &pf); err != nil {
		return nil, fmt.Errorf("progress json parse: %w", err)
	}
	return &pf, nil
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil || !errors.Is(err, os.ErrNotExist)
}

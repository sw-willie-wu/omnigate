package hoyoverse

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"omnigate/internal/core"
)

type applyWAL struct {
	GameID       string   `json:"game_id"`
	Version      string   `json:"version"`
	ManifestETag string   `json:"manifest_etag"`
	Pending      []string `json:"pending"`
	Done         []string `json:"done"`
	WasPredl     bool     `json:"was_predl"`
}

func writeApplyWAL(versionDir string, wal *applyWAL) error {
	walPath := filepath.Join(versionDir, "apply.wal")
	data, err := json.MarshalIndent(wal, "", "  ")
	if err != nil {
		return err
	}
	tmp := walPath + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, walPath); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if dir, err := os.Open(versionDir); err == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}

func readApplyWAL(versionDir string) (*applyWAL, error) {
	return loadJSONSidecar[applyWAL](filepath.Join(versionDir, "apply.wal"))
}

type extractedBlob struct {
	Extracted   bool      `json:"extracted"`
	ExtractedAt time.Time `json:"extracted_at,omitempty"`
}

type extractProgress struct {
	ManifestETag string                   `json:"manifest_etag"`
	Blobs        map[string]extractedBlob `json:"blobs"`
}

func writeExtractProgress(versionDir string, ep *extractProgress) error {
	path := filepath.Join(versionDir, "extract_progress.json")
	data, err := json.MarshalIndent(ep, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

func readExtractProgress(versionDir string) (*extractProgress, error) {
	return loadJSONSidecar[extractProgress](filepath.Join(versionDir, "extract_progress.json"))
}

// osRenameForApply is a test seam for cross-volume EXDEV simulation.
var osRenameForApply = os.Rename

// crossDeviceErrForTest holds the platform's cross-device errno value.
// Tests construct *os.LinkError{Err: crossDeviceErrForTest} to exercise
// the EXDEV terminal path. Set in cross_device_*.go init.
var crossDeviceErrForTest error

func applyOneRename(stagingDir, gameDir, rel string) error {
	src := filepath.Join(stagingDir, rel)
	dst := filepath.Join(gameDir, rel)
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	if err := osRenameForApply(src, dst); err != nil {
		if isCrossDevice(err) {
			return &core.UpdateError{
				Code: "cross_volume_midrun",
				Params: map[string]string{"path": rel, "src": src, "dst": dst, "err": err.Error()},
				Retryable: false,
			}
		}
		return fmt.Errorf("rename %s → %s: %w", src, dst, err)
	}
	return nil
}

func processDeletefiles(stagingDir, gameDir string) error {
	path := filepath.Join(stagingDir, "deletefiles.txt")
	f, err := os.Open(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("open deletefiles.txt: %w", err)
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		rel := strings.TrimSpace(scanner.Text())
		if rel == "" || strings.HasPrefix(rel, "#") {
			continue
		}
		target := filepath.Join(gameDir, rel)
		if err := os.Remove(target); err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue
			}
			return &core.UpdateError{
				Code: "apply_partial",
				Params: map[string]string{"path": rel, "err": err.Error()},
				Retryable: true,
			}
		}
	}
	return scanner.Err()
}

func runApplyPlanPatch(
	ctx context.Context,
	tempRoot, gameDir string,
	gid core.GameID,
	version string,
	wasPredl bool,
	manifestETag string,
	emit func(stage string, current, total int),
) error {
	versionDir := versionSidecarDir(tempRoot, gid, version)
	stagingDir := filepath.Join(versionDir, "staging")

	lock := newApplyLock()
	if err := lock.Acquire(versionDir); err != nil {
		return fmt.Errorf("acquire apply.lock: %w", err)
	}
	defer lock.Release()

	pending, err := scanStagingForApplyTargets(stagingDir, gameDir)
	if err != nil {
		return err
	}
	wal := &applyWAL{
		GameID:       string(gid),
		Version:      version,
		ManifestETag: manifestETag,
		Pending:      pending,
		Done:         []string{},
		WasPredl:     wasPredl,
	}
	if err := writeApplyWAL(versionDir, wal); err != nil {
		return err
	}

	emit("applying", 0, len(pending))
	for len(wal.Pending) > 0 {
		if err := ctx.Err(); err != nil {
			return err
		}
		rel := wal.Pending[0]
		if err := applyOneRename(stagingDir, gameDir, rel); err != nil {
			return err
		}
		wal.Done = append(wal.Done, rel)
		wal.Pending = wal.Pending[1:]
		emit("applying", len(wal.Done), len(wal.Done)+len(wal.Pending))
		if err := writeApplyWAL(versionDir, wal); err != nil {
			return err
		}
	}

	if err := processDeletefiles(stagingDir, gameDir); err != nil {
		return err
	}

	emit("cleanup", 0, 1)
	configWritebackOK := true
	if err := WriteGameVersion(gameDir, version); err != nil {
		configWritebackOK = false
		emit("config_writeback_warning", 0, 1)
	}

	audioLangs, _ := DetectInstalledLanguages(gameDir)
	lat := lastApplyTarget{
		TargetVersion:     version,
		AudioLanguages:    audioLangs,
		CompletionTS:      time.Now().UTC(),
		ConfigWritebackOK: configWritebackOK,
		ManifestETag:      manifestETag,
	}
	if err := writeLastApplyTarget(tempRoot, gid, &lat); err != nil {
		return fmt.Errorf("write last_apply_target: %w", err)
	}

	// Release the apply lock BEFORE removing versionDir: on Windows the held,
	// open apply.lock handle blocks deletion of the file (unlinkat "being used
	// by another process"). Release is idempotent, so the deferred Release above
	// is a safe no-op afterward.
	_ = lock.Release()
	if err := os.RemoveAll(versionDir); err != nil {
		return fmt.Errorf("cleanup versionDir: %w", err)
	}
	return nil
}

func runApplyPlanFull(
	ctx context.Context,
	tempRoot, gameDir string,
	gid core.GameID,
	version string,
	manifestETag string,
	zipBlobs []core.FileTask,
	emit func(stage string, current, total int),
) error {
	versionDir := versionSidecarDir(tempRoot, gid, version)

	lock := newApplyLock()
	if err := lock.Acquire(versionDir); err != nil {
		return fmt.Errorf("acquire apply.lock: %w", err)
	}
	defer lock.Release()

	ep, _ := readExtractProgress(versionDir)
	if ep == nil {
		ep = &extractProgress{ManifestETag: manifestETag, Blobs: make(map[string]extractedBlob)}
	}
	if ep.ManifestETag != manifestETag {
		_ = os.RemoveAll(versionDir)
		_ = os.MkdirAll(versionDir, 0o755)
		ep = &extractProgress{ManifestETag: manifestETag, Blobs: make(map[string]extractedBlob)}
	}

	emit("applying_full", 0, len(zipBlobs))
	for i, blob := range zipBlobs {
		if err := ctx.Err(); err != nil {
			return err
		}
		if eb := ep.Blobs[blob.URL]; eb.Extracted {
			emit("applying_full", i+1, len(zipBlobs))
			continue
		}
		zipPath := filepath.Join(versionDir, blob.Path)
		if err := extractArchiveToStaging(ctx, zipPath, gameDir); err != nil {
			return err
		}
		ep.Blobs[blob.URL] = extractedBlob{Extracted: true, ExtractedAt: time.Now().UTC()}
		if err := writeExtractProgress(versionDir, ep); err != nil {
			return err
		}
		emit("applying_full", i+1, len(zipBlobs))
	}

	emit("cleanup", 0, 1)
	configWritebackOK := true
	if err := WriteGameVersion(gameDir, version); err != nil {
		configWritebackOK = false
		emit("config_writeback_warning", 0, 1)
	}
	audioLangs, _ := DetectInstalledLanguages(gameDir)
	lat := lastApplyTarget{
		TargetVersion: version, AudioLanguages: audioLangs,
		CompletionTS: time.Now().UTC(), ConfigWritebackOK: configWritebackOK,
		ManifestETag: manifestETag,
	}
	if err := writeLastApplyTarget(tempRoot, gid, &lat); err != nil {
		return err
	}
	// Release the apply lock before cleanup (see runApplyPlanPatch note).
	_ = lock.Release()
	return os.RemoveAll(versionDir)
}

// scanStagingForApplyTargets walks stagingDir and returns relative paths of
// every regular file, EXCLUDING metadata files (hdiffmap.json, hdifffiles.txt,
// deletefiles.txt) and excluding `.hdiff` suffixed files. `.patched` suffixed
// files are renamed to drop the suffix; the final relative path is returned.
func scanStagingForApplyTargets(stagingDir, gameDir string) ([]string, error) {
	var out []string
	err := filepath.WalkDir(stagingDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() {
			return nil
		}
		rel, err := filepath.Rel(stagingDir, path)
		if err != nil {
			return err
		}
		base := filepath.Base(rel)
		switch base {
		case "hdiffmap.json", "hdifffiles.txt", "deletefiles.txt":
			return nil
		}
		if strings.HasSuffix(rel, ".hdiff") {
			return nil
		}
		if strings.HasSuffix(rel, ".patched") {
			finalRel := strings.TrimSuffix(rel, ".patched")
			finalPath := filepath.Join(stagingDir, finalRel)
			if err := os.MkdirAll(filepath.Dir(finalPath), 0o755); err != nil {
				return err
			}
			if err := os.Rename(path, finalPath); err != nil {
				return err
			}
			out = append(out, finalRel)
			return nil
		}
		out = append(out, rel)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

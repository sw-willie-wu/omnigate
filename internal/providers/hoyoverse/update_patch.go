package hoyoverse

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"omnigate/internal/patch/hpatchz"
	"omnigate/internal/providers/hoyoverse/sevenzip"
)

// sevenZipMagic is the 6-byte 7-Zip signature ("7z\xBC\xAF\x27\x1C").
var sevenZipMagic = []byte{0x37, 0x7A, 0xBC, 0xAF, 0x27, 0x1C}

// isSevenZip reports whether the file at path begins with the 7-Zip signature.
func isSevenZip(path string) (bool, error) {
	f, err := os.Open(path)
	if err != nil {
		return false, err
	}
	defer f.Close()
	head := make([]byte, len(sevenZipMagic))
	n, err := io.ReadFull(f, head)
	if err == io.EOF || err == io.ErrUnexpectedEOF {
		return false, nil // too short to be a 7z archive
	}
	if err != nil {
		return false, err
	}
	return bytes.Equal(head[:n], sevenZipMagic), nil
}

// extractArchiveToStaging extracts the package at archivePath into stagingDir,
// auto-detecting the container: HoYoverse legacy packages (HSR/ZZZ) ship as
// 7-Zip (LZMA2+BCJ); older/ZIP packages go through the archive/zip path.
func extractArchiveToStaging(ctx context.Context, archivePath, stagingDir string) error {
	is7z, err := isSevenZip(archivePath)
	if err != nil {
		return fmt.Errorf("probe archive %s: %w", archivePath, err)
	}
	if is7z {
		if err := os.MkdirAll(stagingDir, 0o755); err != nil {
			return err
		}
		return sevenzip.Extract(ctx, archivePath, stagingDir)
	}
	return extractZipToStaging(ctx, archivePath, stagingDir)
}

// hdifffilesEntry is one line in Genshin's legacy hdifffiles.txt: a JSON
// object with a single `remoteName` field. The legacy format does NOT
// carry source/target MD5 hashes — patch verification is "if hpatchz exits
// 0 we trust the result". (Modern hdiffmap.json carries hashes; see
// hdiffmapEntry.)
type hdifffilesEntry struct {
	RemoteName string `json:"remoteName"`
}

// parseHdifffiles parses the legacy `hdifffiles.txt` format: one JSON
// object per line. Empty lines are skipped. Returns error on malformed
// JSON or empty remoteName.
//
// Reference: HappyGenyuanImsactUpdate (YYHEggEgg) Patch.cs::Hdiff and
// Hoyo-Hdiff-Patcher (GesthosNetwork) — both confirm one-JSON-per-line
// with `remoteName` as the only field.
func parseHdifffiles(data []byte) ([]hdifffilesEntry, error) {
	var entries []hdifffilesEntry
	lines := bytes.Split(data, []byte{'\n'})
	for i, raw := range lines {
		line := bytes.TrimSpace(raw)
		if len(line) == 0 {
			continue
		}
		var e hdifffilesEntry
		if err := json.Unmarshal(line, &e); err != nil {
			return nil, fmt.Errorf("hdifffiles.txt line %d: %w", i+1, err)
		}
		if e.RemoteName == "" {
			return nil, fmt.Errorf("hdifffiles.txt line %d: empty remoteName", i+1)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// hdiffmapEntry is one entry of the modern HoYoverse hdiffmap.json. Field names
// are snake_case and the array is keyed `diff_map` (verified live against a real
// HSR 4.2.0→4.3.0 patch package, 2026-06-01). The patch file is applied to
// <gameDir>/source_file_name to produce target_file_name; source/target are
// verified by MD5 before/after.
type hdiffmapEntry struct {
	SourceFileName string `json:"source_file_name"`
	SourceFileMD5  string `json:"source_file_md5"`
	SourceFileSize int64  `json:"source_file_size"`
	TargetFileName string `json:"target_file_name"`
	TargetFileMD5  string `json:"target_file_md5"`
	TargetFileSize int64  `json:"target_file_size"`
	PatchFileName  string `json:"patch_file_name"`
	PatchFileMD5   string `json:"patch_file_md5"`
	PatchFileSize  int64  `json:"patch_file_size"`
}

type hdiffmap struct {
	Entries []hdiffmapEntry `json:"diff_map"`
}

func parseHdiffmap(data []byte) (*hdiffmap, error) {
	var hm hdiffmap
	if err := json.Unmarshal(data, &hm); err != nil {
		return nil, fmt.Errorf("hdiffmap parse: %w", err)
	}
	return &hm, nil
}

// extractZipToStaging extracts every file in <zipPath> to <stagingDir>,
// preserving directory structure. ctx-cancellable between entries.
func extractZipToStaging(ctx context.Context, zipPath, stagingDir string) error {
	r, err := zip.OpenReader(zipPath)
	if err != nil {
		return fmt.Errorf("open zip %s: %w", zipPath, err)
	}
	defer r.Close()

	if err := os.MkdirAll(stagingDir, 0o755); err != nil {
		return err
	}

	for _, f := range r.File {
		if err := ctx.Err(); err != nil {
			return err
		}
		clean := filepath.Clean(f.Name)
		if filepath.IsAbs(clean) || strings.HasPrefix(clean, "..") {
			return fmt.Errorf("unsafe zip entry: %s", f.Name)
		}
		dst := filepath.Join(stagingDir, clean)
		if f.FileInfo().IsDir() {
			if err := os.MkdirAll(dst, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			return err
		}
		rc, err := f.Open()
		if err != nil {
			return fmt.Errorf("zip entry open: %w", err)
		}
		out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
		if err != nil {
			rc.Close()
			return err
		}
		if _, err := io.Copy(out, rc); err != nil {
			rc.Close()
			out.Close()
			return err
		}
		rc.Close()
		out.Close()
	}
	return nil
}

func hasHdiffMetadata(stagingDir string) bool {
	for _, name := range []string{"hdiffmap.json", "hdifffiles.txt"} {
		if _, err := os.Stat(filepath.Join(stagingDir, name)); err == nil {
			return true
		}
	}
	return false
}

func verifySourceMD5(path, expectedHex string) error {
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open source: %w", err)
	}
	defer f.Close()
	h := md5.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("read source: %w", err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, expectedHex) {
		return fmt.Errorf("md5 mismatch: got %s want %s", got, expectedHex)
	}
	return nil
}

func verifyTargetMD5(path, expectedHex string) error {
	return verifySourceMD5(path, expectedHex)
}

// applyPatchZip orchestrates Stage E for one patch zip blob.
func applyPatchZip(
	ctx context.Context,
	zipPath, gameDir, stagingDir string,
	emit func(stage string, progress, total int),
) error {
	emit("extracting", 0, 1)
	if err := extractArchiveToStaging(ctx, zipPath, stagingDir); err != nil {
		return err
	}

	if !hasHdiffMetadata(stagingDir) {
		emit("extracting_audio", 1, 1)
		return nil
	}

	hdiffmapPath := filepath.Join(stagingDir, "hdiffmap.json")
	if data, err := os.ReadFile(hdiffmapPath); err == nil {
		hm, err := parseHdiffmap(data)
		if err != nil {
			return err
		}
		emit("patching", 0, len(hm.Entries))
		for i, entry := range hm.Entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			srcPath := filepath.Join(gameDir, entry.SourceFileName)
			patchPath := filepath.Join(stagingDir, entry.PatchFileName)
			stagedTargetPath := filepath.Join(stagingDir, entry.TargetFileName+".patched")

			if err := verifySourceMD5(srcPath, entry.SourceFileMD5); err != nil {
				return fmt.Errorf("source verify %s: %w", entry.SourceFileName, err)
			}
			if err := os.MkdirAll(filepath.Dir(stagedTargetPath), 0o755); err != nil {
				return err
			}
			if err := hpatchz.Run(ctx, srcPath, patchPath, stagedTargetPath); err != nil {
				return fmt.Errorf("hpatchz %s: %w", entry.SourceFileName, err)
			}
			emit("patching", i+1, len(hm.Entries))
		}
		emit("verifying_patches", 0, len(hm.Entries))
		for i, entry := range hm.Entries {
			if err := ctx.Err(); err != nil {
				return err
			}
			stagedTargetPath := filepath.Join(stagingDir, entry.TargetFileName+".patched")
			if err := verifyTargetMD5(stagedTargetPath, entry.TargetFileMD5); err != nil {
				return fmt.Errorf("target verify %s: %w", entry.TargetFileName, err)
			}
			emit("verifying_patches", i+1, len(hm.Entries))
		}
		return nil
	}

	hdifffilesPath := filepath.Join(stagingDir, "hdifffiles.txt")
	if data, err := os.ReadFile(hdifffilesPath); err == nil {
		entries, err := parseHdifffiles(data)
		if err != nil {
			return err
		}
		// Filter to entries whose source file is actually present in gameDir.
		// Genshin's hdifffiles.txt may list files from non-installed audio
		// packs or platform variants — silently skip absent sources (matches
		// reference impl YYHEggEgg/HappyGenyuanImsactUpdate behavior).
		type legacyTask struct{ src, patch, target, rel string }
		tasks := make([]legacyTask, 0, len(entries))
		for _, entry := range entries {
			srcPath := filepath.Join(gameDir, entry.RemoteName)
			if _, statErr := os.Stat(srcPath); statErr != nil {
				continue
			}
			patchPath := filepath.Join(stagingDir, entry.RemoteName+".hdiff")
			if _, statErr := os.Stat(patchPath); statErr != nil {
				continue
			}
			stagedTargetPath := filepath.Join(stagingDir, entry.RemoteName+".patched")
			tasks = append(tasks, legacyTask{srcPath, patchPath, stagedTargetPath, entry.RemoteName})
		}

		emit("patching", 0, len(tasks))
		for i, lt := range tasks {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := os.MkdirAll(filepath.Dir(lt.target), 0o755); err != nil {
				return err
			}
			if err := hpatchz.Run(ctx, lt.src, lt.patch, lt.target); err != nil {
				return fmt.Errorf("hpatchz %s: %w", lt.rel, err)
			}
			emit("patching", i+1, len(tasks))
		}
		// Legacy format ships no target hashes — skip verifying_patches stage.
		return nil
	} else if !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("read hdifffiles.txt: %w", err)
	}

	return fmt.Errorf("staging missing both hdiffmap.json and hdifffiles.txt")
}

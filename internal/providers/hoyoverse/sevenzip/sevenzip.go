// Package sevenzip extracts 7-Zip archives by shelling out to a bundled,
// embedded 7zr.exe (the standalone 7-Zip reduced extractor). HoYoverse legacy
// game/patch packages (HSR/ZZZ) are distributed as 7-Zip (LZMA2+BCJ, solid)
// archives, which Go's archive/zip cannot read. Mirrors the hpatchz sub-package
// pattern: embed the binary, extract once to a SHA-keyed temp path, run it.
package sevenzip

import (
	"context"
	"crypto/sha256"
	_ "embed"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

//go:embed third_party_7zr/7zr.exe
var embedded7zr []byte

var (
	shaOnce sync.Once
	shaHex  string
)

func embeddedSHA() string {
	shaOnce.Do(func() {
		sum := sha256.Sum256(embedded7zr)
		shaHex = hex.EncodeToString(sum[:])
	})
	return shaHex
}

// osTempDir7zr is a test seam returning os.TempDir() in production.
var osTempDir7zr = func() string { return os.TempDir() }

var (
	extractOnce *sync.Once = &sync.Once{}
	extractPath string
	extractErr  error
)

func resetExtractOnce() {
	extractOnce = &sync.Once{}
	extractPath = ""
	extractErr = nil
}

// extractBinaryOnce writes embedded7zr to <TEMP>/omnigate/7zr-<sha8>.exe (shared
// cross-backend cache; SHA-keyed so a binary upgrade auto-invalidates). Atomic
// write (tmpfile + rename); reuses an existing file of matching size.
func extractBinaryOnce(ctx context.Context) (string, error) {
	extractOnce.Do(func() {
		sha8 := embeddedSHA()[:8]
		dir := filepath.Join(osTempDir7zr(), "omnigate")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			extractErr = fmt.Errorf("mkdir %s: %w", dir, err)
			return
		}
		path := filepath.Join(dir, "7zr-"+sha8+".exe")
		if stat, err := os.Stat(path); err == nil && stat.Size() == int64(len(embedded7zr)) {
			extractPath = path
			return
		}
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, embedded7zr, 0o755); err != nil {
			extractErr = fmt.Errorf("write %s: %w", tmp, err)
			return
		}
		if err := os.Rename(tmp, path); err != nil {
			_ = os.Remove(tmp)
			extractErr = fmt.Errorf("rename %s → %s: %w", tmp, path, err)
			return
		}
		extractPath = path
	})
	if extractErr != nil {
		return "", extractErr
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return extractPath, nil
}

// new7zrCmd is a test seam for constructing the exec.Cmd.
var new7zrCmd = func(ctx context.Context, bin string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, bin, args...)
	hideConsoleWindow(cmd) // Windows: suppress the console window flash.
	return cmd
}

// Extract extracts every entry of the 7-Zip archive at archivePath into destDir,
// preserving directory structure (7-Zip `x` command, overwrite-all). Cancel
// propagates via ctx (exec.CommandContext kills the process on ctx.Done()).
func Extract(ctx context.Context, archivePath, destDir string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	bin, err := extractBinaryOnce(ctx)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("mkdir dest: %w", err)
	}
	// x = extract with full paths; -o<dir> output dir; -y assume-yes; -aoa
	// overwrite all (idempotent re-runs after a partial extract).
	cmd := new7zrCmd(ctx, bin, "x", archivePath, "-o"+destDir, "-y", "-aoa")
	out, err := cmd.CombinedOutput()
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return ctxErr
		}
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return fmt.Errorf("7zr exit %d: %w; output: %s", ee.ExitCode(), err, string(out))
		}
		return fmt.Errorf("7zr exec: %w; output: %s", err, string(out))
	}
	return nil
}

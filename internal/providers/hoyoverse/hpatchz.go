package hoyoverse

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	_ "embed"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

//go:embed third_party_hpatchz/hpatchz.exe
var embeddedHpatchz []byte

// embeddedHpatchzSHA returns the SHA-256 hex of the embedded binary.
// Computed once at package init via sync.Once-guarded var below.
var (
	hpatchzSHAOnce sync.Once
	hpatchzSHA     string
)

func embeddedHpatchzSHA() string {
	hpatchzSHAOnce.Do(func() {
		sum := sha256.Sum256(embeddedHpatchz)
		hpatchzSHA = hex.EncodeToString(sum[:])
	})
	return hpatchzSHA
}

// osTempDirHpatchz is a test seam returning os.TempDir() in production.
var osTempDirHpatchz = func() string { return os.TempDir() }

// extractOnce gates first-use binary extraction. resetHpatchzExtractOnce
// resets it for tests; production code never calls reset.
var (
	hpatchzExtractOnce *sync.Once = &sync.Once{}
	hpatchzExtractPath string
	hpatchzExtractErr  error
)

func resetHpatchzExtractOnce() {
	hpatchzExtractOnce = &sync.Once{}
	hpatchzExtractPath = ""
	hpatchzExtractErr = nil
}

// extractHpatchzOnce writes embeddedHpatchz to <TEMP>/omnigate/hpatchz-<sha8>.exe
// (cross-backend shared cache; spec §1 hpatchz.go row, §2 sidecar tree). The
// SHA-keyed filename means a binary upgrade auto-invalidates the cache.
//
// Idempotent: if the target file already exists with matching size, reuse;
// else write atomically (tmpfile + rename). Returns the absolute path.
func extractHpatchzOnce(ctx context.Context) (string, error) {
	hpatchzExtractOnce.Do(func() {
		sha8 := embeddedHpatchzSHA()[:8]
		dir := filepath.Join(osTempDirHpatchz(), "omnigate")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			hpatchzExtractErr = fmt.Errorf("mkdir %s: %w", dir, err)
			return
		}
		path := filepath.Join(dir, "hpatchz-"+sha8+".exe")
		// Reuse if already present + correct size.
		if stat, err := os.Stat(path); err == nil && stat.Size() == int64(len(embeddedHpatchz)) {
			hpatchzExtractPath = path
			return
		}
		// Atomic write.
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, embeddedHpatchz, 0o755); err != nil {
			hpatchzExtractErr = fmt.Errorf("write %s: %w", tmp, err)
			return
		}
		if err := os.Rename(tmp, path); err != nil {
			_ = os.Remove(tmp)
			hpatchzExtractErr = fmt.Errorf("rename %s → %s: %w", tmp, path, err)
			return
		}
		hpatchzExtractPath = path
	})
	if hpatchzExtractErr != nil {
		return "", hpatchzExtractErr
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	return hpatchzExtractPath, nil
}

// newHpatchzCmd is a test seam for constructing the exec.Cmd. Keeps tests
// from binding to specific CommandContext flags.
func newHpatchzCmd(ctx context.Context, hpatchzPath string, args ...string) *exec.Cmd {
	return exec.CommandContext(ctx, hpatchzPath, args...)
}

// Run invokes hpatchz to apply <diffFile> to <oldFile>, producing <newFile>.
// Cancel propagates via ctx (exec.CommandContext machinery kills the process
// on ctx.Done()).
//
// hpatchz argument order: hpatchz [options] <oldFile> <diffFile> <outNewFile>
// Per HDiffPatch v4 docs (and Collapse Launcher's invocation pattern).
//
// Returns nil on zero exit code; otherwise error wrapping exec.ExitError
// with stderr.
func Run(ctx context.Context, oldFile, diffFile, newFile string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	hpatchzPath, err := extractHpatchzOnce(ctx)
	if err != nil {
		return err
	}
	cmd := newHpatchzCmd(ctx, hpatchzPath, "-f", oldFile, diffFile, newFile)
	out, err := cmd.CombinedOutput()
	if err != nil {
		var ee *exec.ExitError
		if errors.As(err, &ee) {
			return fmt.Errorf("hpatchz exit %d: %w; output: %s", ee.ExitCode(), err, string(out))
		}
		return fmt.Errorf("hpatchz exec: %w; output: %s", err, string(out))
	}
	return nil
}

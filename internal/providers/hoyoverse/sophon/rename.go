package sophon

import (
	"fmt"
	"io"
	"os"
)

// SafeAtomicRename renames src→dst, falling back to copy+remove when src and
// dst live on different volumes (the OS returns a cross-device errno). The
// fallback writes dst via a temp file + atomic rename within dst's directory.
func SafeAtomicRename(src, dst string) error {
	if err := os.Rename(src, dst); err != nil {
		if !isCrossDevice(err) {
			return err
		}
		return copyThenRemove(src, dst)
	}
	return nil
}

func copyThenRemove(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	tmp := dst + ".xdev.tmp"
	out, err := os.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return fmt.Errorf("cross-device copy %s → %s: %w", src, dst, err)
	}
	if err := out.Sync(); err != nil {
		_ = out.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := out.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Rename(tmp, dst); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	if err := os.Remove(src); err != nil {
		return fmt.Errorf("cross-device remove src %s: %w", src, err)
	}
	return nil
}

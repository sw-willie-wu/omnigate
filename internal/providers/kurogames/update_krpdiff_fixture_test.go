package kurogames

import (
	"os"
	"path/filepath"
	"testing"
)

// krpdiffMagicPrefix is the byte-exact header hdiffz v5.1.3 writes for a
// directory diff produced with `-c-zstd -C-fadler64`. See
// testdata/krpdiff/README.md for how the fixtures were generated and why
// krpdiff is a directory diff (HDIFF19), not a single-file diff (HDIFF13).
const krpdiffMagicPrefix = "HDIFF19&zstd&fadler64"

// TestKrpdiffFixture_Magic guards against format drift in the checked-in
// krpdiff fixtures: it asserts the first bytes of each fixture are exactly
// the expected HDiffPatch directory-diff header with zstd compression and
// fadler64 checksum.
func TestKrpdiffFixture_Magic(t *testing.T) {
	for _, name := range []string{"a.krpdiff", "b.krpdiff"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join("testdata", "krpdiff", name)
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("ReadFile(%s): %v", path, err)
			}
			if len(data) < len(krpdiffMagicPrefix) {
				t.Fatalf("%s too short: got %d bytes, want at least %d", path, len(data), len(krpdiffMagicPrefix))
			}
			got := string(data[:len(krpdiffMagicPrefix)])
			if got != krpdiffMagicPrefix {
				t.Fatalf("%s magic prefix mismatch:\n got:  %q\n want: %q", path, got, krpdiffMagicPrefix)
			}
		})
	}
}

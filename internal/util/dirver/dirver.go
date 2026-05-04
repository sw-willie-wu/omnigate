// Package dirver scans a directory for child directories whose names look
// like version numbers (3- or 4-part dotted ints, e.g. "1.2.3" or "2.6.1.0")
// and returns the highest-versioned name. Used by Kuro and Hypergryph
// providers to read the installed version from the launcher's local FS.
package dirver

import (
	"os"
	"regexp"
	"strconv"
	"strings"
)

// versionRe matches names of 3 or 4 dotted positive integers — e.g.
//   1.2.3
//   2.6.1.0
// and rejects:
//   Cache
//   1.2
//   1.2.3-rc1
//   v1.2.3 (no leading 'v')
var versionRe = regexp.MustCompile(`^\d+\.\d+\.\d+(\.\d+)?$`)

// MaxIn scans dir for child directories whose names match the version regex
// and returns the highest-versioned one as a string. Returns "" with nil
// error when:
//   - dir doesn't exist
//   - dir exists but contains no version-named children
//   - dir exists but is not actually a directory (file at that path)
//
// Returns a non-nil error only on unexpected I/O failure (permission
// denied, etc.).
func MaxIn(dir string) (string, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		// Treat "not a directory" as "no version found"
		if _, statErr := os.Stat(dir); statErr == nil {
			return "", nil
		}
		return "", err
	}

	var bestName string
	var bestParts []int
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if !versionRe.MatchString(name) {
			continue
		}
		parts := parseInts(name)
		if bestName == "" || cmpVersionInts(parts, bestParts) > 0 {
			bestName = name
			bestParts = parts
		}
	}
	return bestName, nil
}

// parseInts splits "1.2.3.4" into []int{1,2,3,4}. Caller must guarantee the
// input matches versionRe — no error returned.
func parseInts(s string) []int {
	segs := strings.Split(s, ".")
	out := make([]int, len(segs))
	for i, seg := range segs {
		n, _ := strconv.Atoi(seg)
		out[i] = n
	}
	return out
}

// cmpVersionInts compares two int slices lexicographically, treating missing
// trailing segments as 0 (so [1,2,3] < [1,2,3,1]). Returns -1, 0, or 1.
func cmpVersionInts(a, b []int) int {
	n := len(a)
	if len(b) > n {
		n = len(b)
	}
	for i := 0; i < n; i++ {
		var ai, bi int
		if i < len(a) {
			ai = a[i]
		}
		if i < len(b) {
			bi = b[i]
		}
		if ai < bi {
			return -1
		}
		if ai > bi {
			return 1
		}
	}
	return 0
}

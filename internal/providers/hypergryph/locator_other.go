//go:build !windows

package hypergryph

// platformGryphRoot is a no-op off Windows: the GRYPHLINK installer records its
// uninstall entry (launcher root) in the Windows registry, which doesn't exist
// on other platforms.
func platformGryphRoot() (string, bool) { return "", false }

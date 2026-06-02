//go:build !windows

package hypergryph

func platformIsProcessRunning(exeName string) bool {
	return false // stub: tests run on non-Windows; production is Windows-only
}

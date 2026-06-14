//go:build !windows

package kurogames

func platformIsProcessRunning(exeName string) bool {
	return false // stub: tests run on non-Windows; production is Windows-only
}

func anyProcessRunning(names []string) bool {
	for _, n := range names {
		if platformIsProcessRunning(n) {
			return true
		}
	}
	return false
}

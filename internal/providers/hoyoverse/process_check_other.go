//go:build !windows

package hoyoverse

func platformIsProcessRunning(exeName string) bool {
	return false
}

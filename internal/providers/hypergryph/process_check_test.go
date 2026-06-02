package hypergryph

import "testing"

// TestPlatformIsProcessRunning_NotRunning asserts a clearly-absent exe returns
// false on every platform (Windows snapshot walk + non-Windows stub).
func TestPlatformIsProcessRunning_NotRunning(t *testing.T) {
	if platformIsProcessRunning("definitely-not-a-real-process-xyz.exe") {
		t.Errorf("expected false for a non-existent process")
	}
}

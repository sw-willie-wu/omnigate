//go:build windows

package hoyoverse

import (
	"os"
	"path/filepath"
	"testing"
)

func TestPlatformIsProcessRunning_NotRunning(t *testing.T) {
	if platformIsProcessRunning("definitely_not_a_real_exe_name_xyz123.exe") {
		t.Error("expected false for nonexistent process name")
	}
}

func TestPlatformIsProcessRunning_SelfPID(t *testing.T) {
	// The currently-running test binary should be detected.
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	if !platformIsProcessRunning(filepath.Base(exe)) {
		t.Errorf("expected true for self process name (%s); enumeration may have failed", filepath.Base(exe))
	}
}

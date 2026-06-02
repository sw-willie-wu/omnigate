package hypergryph

import (
	"errors"
	"math"
	"testing"

	"omnigate/internal/core"
)

func TestPreflightSameVolume(t *testing.T) {
	if err := preflightSameVolume(`C:\temp`, `C:\Games\X`); err != nil {
		t.Errorf("same volume should pass: %v", err)
	}
	err := preflightSameVolume(`C:\temp`, `D:\Games\X`)
	var ue *core.UpdateError
	if !errors.As(err, &ue) || ue.Code != "cross_volume_temp" {
		t.Errorf("cross volume should be cross_volume_temp, got %v", err)
	}
}

func TestCheckDiskSpace_Full(t *testing.T) {
	dir := t.TempDir()
	err := checkDiskSpace(dir, math.MaxInt64)
	var ue *core.UpdateError
	if !errors.As(err, &ue) || ue.Code != "disk_full" {
		t.Errorf("expected disk_full for impossible size, got %v", err)
	}
	if err := checkDiskSpace(dir, 1); err != nil {
		t.Errorf("1 byte should fit: %v", err)
	}
}

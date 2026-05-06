package hoyoverse

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"omnigate/internal/core"
)

func TestCheckDiskSpace_Enough(t *testing.T) {
	probe := &stubFreeSpace{bytes: 100 * 1024 * 1024 * 1024} // 100 GiB
	plan := core.UpdatePlan{
		Files: []core.FileTask{
			{Path: "blob1.zip", Size: 5 * 1024 * 1024 * 1024}, // 5 GiB
		},
	}
	err := CheckDiskSpace(plan, t.TempDir(), t.TempDir(), probe)
	if err != nil {
		t.Errorf("expected nil; got %v", err)
	}
}

func TestCheckDiskSpace_Insufficient(t *testing.T) {
	probe := &stubFreeSpace{bytes: 1 * 1024 * 1024 * 1024} // 1 GiB free
	plan := core.UpdatePlan{
		Files: []core.FileTask{
			{Path: "blob1.zip", Size: 50 * 1024 * 1024 * 1024}, // 50 GiB needed
		},
	}
	err := CheckDiskSpace(plan, t.TempDir(), t.TempDir(), probe)
	if err == nil {
		t.Fatal("expected error; got nil")
	}
	var ue *core.UpdateError
	if !errors.As(err, &ue) || ue.Code != "insufficient_space" {
		t.Errorf("expected insufficient_space; got %v", err)
	}
}

func TestCheckDiskSpace_CrossVolume(t *testing.T) {
	if !crossVolumeProbeReady() {
		t.Skip("cross-volume test requires Windows multi-drive")
	}
	probe := &stubFreeSpace{bytes: 100 * 1024 * 1024 * 1024}
	plan := core.UpdatePlan{Files: []core.FileTask{{Path: "x", Size: 1024}}}
	err := CheckDiskSpace(plan, `D:\genshin-temp`, `C:\Program Files\Genshin Impact\Genshin Impact game`, probe)
	if err == nil {
		t.Fatal("expected cross_volume_setup error")
	}
	var ue *core.UpdateError
	if !errors.As(err, &ue) || ue.Code != "cross_volume_setup" {
		t.Errorf("expected cross_volume_setup; got %v", err)
	}
}

func crossVolumeProbeReady() bool {
	if _, err := os.Stat(`D:\`); err != nil {
		return false
	}
	_ = filepath.Join // silence unused-import on builds where filepath isn't needed elsewhere
	return true
}

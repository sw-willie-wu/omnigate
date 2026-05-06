//go:build integration

package hoyoverse

import (
	"testing"
)

func TestEndToEnd_PlanPatch_HappyPath(t *testing.T) {
	t.Skip("real-zip + hpatchz integration; requires fixtures with valid hdiffmap+hdiff binaries; deferred to Task 22 smoke")
}

func TestEndToEnd_PlanFull_HappyPath(t *testing.T) {
	t.Skip("real-zip extraction; deferred to Task 22 smoke")
}

func TestEndToEnd_AudioOnly_HappyPath(t *testing.T) {
	t.Skip("requires zip fixture for audio_pkg; deferred to Task 22 smoke. Unit test TestBuildPlan_FlavorAudioOnly covers the planning logic.")
}

func TestEndToEnd_PredlHit(t *testing.T) {
	t.Skip("requires predl_ready.json + matching cached zip blob; deferred to Task 22 smoke")
}

func TestEndToEnd_CrashRecovery_StageC(t *testing.T) {
	t.Skip("requires range-aware blob server fixture; can be implemented in follow-up if needed")
}

func TestEndToEnd_CrashRecovery_StageE(t *testing.T) {
	t.Skip("requires hpatchz integration; deferred to Task 22 smoke")
}

func TestEndToEnd_CrashRecovery_StageF_PlanPatch(t *testing.T) {
	t.Skip("requires apply.wal mid-rename state; deferred to Task 22 smoke")
}

func TestEndToEnd_CrashRecovery_StageF_PlanFull(t *testing.T) {
	t.Skip("requires extract_progress.json mid-extract state; deferred to Task 22 smoke")
}

func TestEndToEnd_ConfigWritebackFail(t *testing.T) {
	t.Skip("requires Windows ACL setup; deferred to Task 22 smoke")
}

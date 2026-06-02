package hoyoverse

import (
	"testing"

	"omnigate/internal/core"
)

func TestProvider_IsGameRunning_Stub(t *testing.T) {
	p := &Provider{}
	got, err := p.IsGameRunning(core.GameID("hoyoverse/genshin"))
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	_ = got
}

func TestProvider_CheckForUpdate_FullPath(t *testing.T) {
	t.Skip("integration scenario lives in Task 20 integration_test.go")
}

func TestProvider_RunUpdate_ResumeDispatchTable(t *testing.T) {
	t.Skip("dispatch validation in Task 20 integration_test.go")
}

func TestProvider_SelfHeal_ConfigWritebackFailure(t *testing.T) {
	t.Skip("self-heal scenario in Task 20 integration_test.go")
}

package core

import (
	"context"
	"errors"
	"testing"
)

type predlCheckerStub struct{}

func (predlCheckerStub) SupportsPredownload(GameID) bool { return true }
func (predlCheckerStub) CheckForPredownload(context.Context, GameID, func(int, int)) (UpdatePlan, error) {
	return UpdatePlan{}, ErrPredownloadUnsupported
}

func TestPredownloadChecker_InterfaceAndSentinel(t *testing.T) {
	var pc PredownloadChecker = predlCheckerStub{}
	if !pc.SupportsPredownload("kurogames/wutheringwaves") {
		t.Fatal("stub should support predownload")
	}
	_, err := pc.CheckForPredownload(context.Background(), "kurogames/wutheringwaves", nil)
	if !errors.Is(err, ErrPredownloadUnsupported) {
		t.Fatalf("err = %v, want ErrPredownloadUnsupported", err)
	}
}

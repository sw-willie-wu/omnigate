package core

import (
	"errors"
	"fmt"
	"testing"
)

func TestSentinelErrors_Is(t *testing.T) {
	wrapped := fmt.Errorf("wrap: %w", ErrGameNotInstalled)
	if !errors.Is(wrapped, ErrGameNotInstalled) {
		t.Errorf("wrapped error not detected via errors.Is")
	}
}

func TestErrorCode(t *testing.T) {
	cases := []struct {
		err  error
		want string
	}{
		{ErrUnknownGame, "unknown_game"},
		{ErrGameNotInstalled, "not_installed"},
		{ErrBackendNotConfigured, "not_configured"},
		{ErrLauncherMissing, "launcher_missing"},
		{ErrAssetNotAvailable, "asset_unavailable"},
		{fmt.Errorf("wrap: %w", ErrGameNotInstalled), "not_installed"},
		{nil, "internal"},
		{errors.New("random"), "internal"},
	}
	for _, c := range cases {
		got := ErrorCode(c.err)
		if got != c.want {
			t.Errorf("ErrorCode(%v) = %q, want %q", c.err, got, c.want)
		}
	}
}

func TestErrorCode_AccountSwitcherSentinels(t *testing.T) {
	cases := map[error]string{
		ErrGameRunning:              "game_running",
		ErrAccountSwitchUnsupported: "account_switch_unsupported",
	}
	for sentinel, want := range cases {
		if got := ErrorCode(fmt.Errorf("wrap: %w", sentinel)); got != want {
			t.Errorf("ErrorCode(%v) = %q, want %q", sentinel, got, want)
		}
	}
}

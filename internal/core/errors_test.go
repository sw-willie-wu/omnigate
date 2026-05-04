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

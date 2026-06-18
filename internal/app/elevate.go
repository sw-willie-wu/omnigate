package app

import (
	"errors"
	"strings"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"
)

// errUACDeclined is returned by relaunchElevated when the user dismisses the UAC
// prompt (Windows ERROR_CANCELLED). The frontend maps its message to a toast.
var errUACDeclined = errors.New("uac_declined")

// parseElevateArg extracts the gid from `--elevate-update <gid>` or
// `--elevate-update=<gid>` in args; "" if absent.
func parseElevateArg(args []string) string {
	for i, x := range args {
		if x == "--elevate-update" && i+1 < len(args) {
			return args[i+1]
		}
		if strings.HasPrefix(x, "--elevate-update=") {
			return strings.TrimPrefix(x, "--elevate-update=")
		}
	}
	return ""
}

// IsElevated is a Wails RPC: true when the process runs with admin rights. The
// frontend gates the "restart as administrator" button on !IsElevated() to avoid
// an elevation loop.
func (a *App) IsElevated() bool { return isElevated() }

// PendingElevatedGame is a Wails RPC returning the --elevate-update gid once
// (then ""), so the elevated instance's frontend can auto-select + auto-start it.
func (a *App) PendingElevatedGame() string {
	g := a.pendingElevate
	a.pendingElevate = ""
	return g
}

// RelaunchElevated is a Wails RPC: relaunch omnigate elevated to update gameID,
// then quit this (non-elevated) instance. Returns errUACDeclined (→ toast) if the
// user declines UAC; "already_elevated" if called when already admin (defensive).
func (a *App) RelaunchElevated(gameID string) error {
	if isElevated() {
		return errors.New("already_elevated")
	}
	if err := relaunchElevated([]string{"--elevate-update", gameID}); err != nil {
		return err // errUACDeclined or a real error; both reach the frontend
	}
	if a.ctx != nil {
		wruntime.Quit(a.ctx)
	}
	return nil
}

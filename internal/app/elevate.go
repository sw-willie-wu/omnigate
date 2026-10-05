package app

import (
	"errors"
	"fmt"
	"strings"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"omnigate/internal/core"
)

// errUACDeclined is returned by relaunchElevated when the user dismisses the UAC
// prompt (Windows ERROR_CANCELLED). The frontend maps its message to a toast.
var errUACDeclined = errors.New("uac_declined")

// Test seams: tests stub these so they never call ShellExecute or depend on
// the real token elevation.
var (
	relaunchElevatedFn = relaunchElevated
	isElevatedFn       = isElevated
)

// ElevatedBundle is the bundle install an elevated relaunch should continue
// (spec §6.9). Zero value = nothing pending.
type ElevatedBundle struct {
	GameID string `json:"game_id"`
	Bundle string `json:"bundle"`
}

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

// parseElevateBundleArg extracts `--elevate-install-bundle <gid> <bundle>` from
// args. A missing value or a bundle outside SD/HD/UHD yields the zero value.
func parseElevateBundleArg(args []string) ElevatedBundle {
	for i, x := range args {
		if x != "--elevate-install-bundle" {
			continue
		}
		if i+2 >= len(args) {
			return ElevatedBundle{}
		}
		g, b := args[i+1], args[i+2]
		if g == "" || !knownBundles[b] {
			return ElevatedBundle{}
		}
		return ElevatedBundle{GameID: g, Bundle: b}
	}
	return ElevatedBundle{}
}

// IsElevated is a Wails RPC: true when the process runs with admin rights. The
// frontend gates the "restart as administrator" button on !IsElevated() to avoid
// an elevation loop.
func (a *App) IsElevated() bool { return isElevatedFn() }

// PendingElevatedGame is a Wails RPC returning the --elevate-update gid once
// (then ""), so the elevated instance's frontend can auto-select + auto-start it.
func (a *App) PendingElevatedGame() string {
	g := a.pendingElevate
	a.pendingElevate = ""
	return g
}

// PendingElevatedBundle is a Wails RPC returning the --elevate-install-bundle
// request once (then the zero value), so the elevated instance's frontend can
// auto-select the game and continue the bundle install.
func (a *App) PendingElevatedBundle() ElevatedBundle {
	b := a.pendingElevateBundle
	a.pendingElevateBundle = ElevatedBundle{}
	return b
}

// RelaunchElevated is a Wails RPC: relaunch omnigate elevated to update gameID,
// then quit this (non-elevated) instance. Returns errUACDeclined (→ toast) if the
// user declines UAC; "already_elevated" if called when already admin (defensive).
func (a *App) RelaunchElevated(gameID string) error {
	if isElevatedFn() {
		return errors.New("already_elevated")
	}
	if err := relaunchElevatedFn([]string{"--elevate-update", gameID}); err != nil {
		return err // errUACDeclined or a real error; both reach the frontend
	}
	if a.ctx != nil {
		wruntime.Quit(a.ctx)
	}
	return nil
}

// RelaunchElevatedForBundle is a Wails RPC: relaunch omnigate elevated with
// `--elevate-install-bundle <gameID> <bundle>` so the new instance continues
// the bundle install, then quit this instance (spec §6.9). It never emits
// --elevate-update (that would auto-start a full update). relaunchElevated
// joins args with spaces, so gameID is validated to block argument injection.
func (a *App) RelaunchElevatedForBundle(gameID, bundle string) error {
	if isElevatedFn() {
		return errors.New("already_elevated")
	}
	if !knownBundles[bundle] {
		return fmt.Errorf("unknown bundle %q", bundle)
	}
	if gameID == "" || strings.ContainsAny(gameID, " \t\"") || strings.HasPrefix(gameID, "-") {
		return fmt.Errorf("invalid game id %q", gameID)
	}
	if _, _, ok := a.bundleManager(core.GameID(gameID)); !ok {
		return fmt.Errorf("game %q does not support bundles", gameID)
	}
	if err := relaunchElevatedFn([]string{"--elevate-install-bundle", gameID, bundle}); err != nil {
		return err // errUACDeclined or a real error
	}
	if a.ctx != nil {
		wruntime.Quit(a.ctx)
	}
	return nil
}

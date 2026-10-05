package app

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"omnigate/internal/core"
)

// BundleState is the RPC view of a game's resource bundles (spec §6.2).
type BundleState struct {
	Supported    bool              `json:"supported"`
	Active       string            `json:"active"`
	Bundles      []BundleRow       `json:"bundles"`
	Options      []LaunchOptionRow `json:"options"`
	CatalogStale bool              `json:"catalog_stale"`
	Error        *core.UpdateError `json:"error,omitempty"`
}

type BundleRow struct {
	Name            string               `json:"name"`
	DisplayName     core.LocalizedString `json:"display_name"`
	Installed       bool                 `json:"installed"`
	Pending         bool                 `json:"pending"`
	Version         string               `json:"version,omitempty"`
	SizeBytes       int64                `json:"size_bytes"`
	LaunchSupported bool                 `json:"launch_supported"`
	Removable       bool                 `json:"removable"`
}

type LaunchOptionRow struct {
	Cmd     string               `json:"cmd"`
	Label   core.LocalizedString `json:"label"`
	Enabled bool                 `json:"enabled"`
	Default bool                 `json:"default"`
}

var knownBundles = map[string]bool{"SD": true, "HD": true, "UHD": true}

// knownInstalled counts installed bundles the launcher knows how to start
// (spec installedKnown()).
func knownInstalled(st core.BundleInstallState) int {
	n := 0
	for name := range st.Installed {
		if knownBundles[name] {
			n++
		}
	}
	return n
}

func (a *App) bundleManager(gid core.GameID) (core.BundleManager, core.Provider, bool) {
	p, err := a.provider(gid)
	if err != nil {
		return nil, nil, false
	}
	bm, ok := p.(core.BundleManager)
	return bm, p, ok
}

func (a *App) buildBundleState(gid core.GameID, bm core.BundleManager) (BundleState, error) {
	cat, st, found, err := bm.BundleState(context.Background(), gid)
	if err != nil {
		return BundleState{Supported: true}, err
	}
	out := BundleState{Supported: true, Active: st.Active, CatalogStale: !found || cat.FetchedAt.Before(a.startedAt)}
	pending := map[string]bool{}
	for _, n := range st.Pending {
		pending[n] = true
	}
	nKnown := knownInstalled(st)
	seen := map[string]bool{}
	addRow := func(name string, e *core.BundleCatalogEntry) {
		seen[name] = true
		v, inst := st.Installed[name]
		row := BundleRow{Name: name, Installed: inst, Pending: pending[name], Version: v, LaunchSupported: knownBundles[name]}
		if e != nil {
			row.DisplayName, row.SizeBytes = e.DisplayName, e.PackSize
		} else {
			row.DisplayName = core.LocalizedString{"en": name}
		}
		row.Removable = inst && knownBundles[name] && !pending[name] && name != st.Active && nKnown > 1
		out.Bundles = append(out.Bundles, row)
	}
	if found {
		for i := range cat.Bundles {
			addRow(cat.Bundles[i].Name, &cat.Bundles[i])
		}
	}
	// Catalog missing or lagging: still show what is installed/pending.
	for _, n := range []string{"UHD", "HD", "SD"} {
		_, inst := st.Installed[n]
		if (inst || pending[n]) && !seen[n] {
			addRow(n, nil)
		}
	}
	if found {
		opts, _ := bm.LaunchOptions(context.Background(), gid)
		for _, e := range cat.Bundles {
			if e.Name != st.Active {
				continue
			}
			for _, o := range e.Options {
				v, set := opts[o.Cmd]
				out.Options = append(out.Options, LaunchOptionRow{Cmd: o.Cmd, Label: o.Label, Default: o.Default, Enabled: (set && v) || (!set && o.Default)})
			}
		}
	}
	return out, nil
}

// GetBundleState returns the offline bundle view. Unsupported games return
// Supported=false and no error.
func (a *App) GetBundleState(gameID string) (BundleState, error) {
	gid := core.GameID(gameID)
	bm, _, ok := a.bundleManager(gid)
	if !ok {
		return BundleState{Supported: false}, nil
	}
	return a.buildBundleState(gid, bm)
}

// withState rebuilds the state after an operation. A *core.UpdateError from
// the operation goes into BundleState.Error (RPC error stays nil so the UI
// can render it); any other error is returned as the RPC error.
func (a *App) withState(gid core.GameID, bm core.BundleManager, e error) (BundleState, error) {
	st, err := a.buildBundleState(gid, bm)
	var ue *core.UpdateError
	if errors.As(e, &ue) {
		st.Error = ue
		return st, err
	}
	if e != nil {
		return st, e
	}
	return st, err
}

// busy: an op is in flight, or an interrupted update awaits resume/discard.
// Other LastErrors do not block (startUpdateFlowWith clears them).
func (a *App) busy(gid core.GameID) bool {
	s := a.updateRegistry.Get(gid).Snapshot()
	return s.InFlight != nil || (s.LastError != nil && s.LastError.Code == "interrupted_resume")
}

func gateErr(code, bundle string) *core.UpdateError {
	return &core.UpdateError{Code: code, Retryable: false, Params: map[string]string{"bundle": bundle}}
}

func (a *App) gameRunning(p core.Provider, gid core.GameID) bool {
	if pc, ok := p.(core.ProcessChecker); ok {
		r, _ := pc.IsGameRunning(gid)
		return r
	}
	return false
}

func processBlocked(gameID string) *core.UpdateError {
	return &core.UpdateError{Code: "process_blocked", Retryable: true, Params: map[string]string{"kind": "process_running", "game": gameID}}
}

func (a *App) SetActiveBundle(gameID, name string) (BundleState, error) {
	gid := core.GameID(gameID)
	bm, _, ok := a.bundleManager(gid)
	if !ok {
		return BundleState{Supported: false}, nil
	}
	return a.withState(gid, bm, bm.SetActiveBundle(context.Background(), gid, name))
}

func (a *App) SetLaunchOption(gameID, cmd string, enabled bool) (BundleState, error) {
	gid := core.GameID(gameID)
	bm, _, ok := a.bundleManager(gid)
	if !ok {
		return BundleState{Supported: false}, nil
	}
	return a.withState(gid, bm, bm.SetLaunchOption(context.Background(), gid, cmd, enabled))
}

// InstallBundle runs the sync gates (spec §6.4 step 1 order), the residual
// sidecar check (step 4), then starts the async install via
// startUpdateFlowWith. Gate failures land in BundleState.Error, never in
// LastError, and never leave an in-flight op.
func (a *App) InstallBundle(gameID, name string) (BundleState, error) {
	gid := core.GameID(gameID)
	bm, p, ok := a.bundleManager(gid)
	if !ok {
		return BundleState{Supported: false}, nil
	}
	fail := func(ue *core.UpdateError) (BundleState, error) { return a.withState(gid, bm, ue) }
	if !knownBundles[name] {
		return fail(gateErr("bundle_unknown", name))
	}
	if a.gameRunning(p, gid) {
		return fail(processBlocked(gameID))
	}
	if a.busy(gid) {
		return fail(&core.UpdateError{Code: "bundle_busy", Retryable: true})
	}
	_, st, _, err := bm.BundleState(context.Background(), gid)
	if err != nil {
		return a.withState(gid, bm, err)
	}
	if knownInstalled(st) == 0 {
		return fail(&core.UpdateError{Code: "install_record_missing"})
	}
	for _, n := range st.Pending {
		if n == name {
			return fail(gateErr("bundle_pending", name))
		}
	}
	if _, inst := st.Installed[name]; inst {
		return fail(gateErr("bundle_installed", name))
	}
	// Quick check only; the authoritative version check is inside
	// BuildBundleInstallPlan (spec §6.8). A network failure here lets it pass.
	if vi, verr := a.RefreshVersion(gameID); verr == nil && updateAvailable(vi) {
		return fail(gateErr("bundle_update_first", name))
	}
	// Common version = the lowest version among installed known bundles.
	common := ""
	for n, v := range st.Installed {
		if !knownBundles[n] || v == "" {
			continue
		}
		if common == "" || versionNewer(common, v) {
			common = v
		}
	}
	if common != "" {
		if rs := core.ScanRecovery(a.sidecarVersionDir(p, gid, common)); rs.Phase != core.RecoveryNone && rs.Bundle != name {
			return fail(&core.UpdateError{Code: "bundle_busy", Retryable: true, Params: map[string]string{"reason": "residual", "bundle": rs.Bundle}})
		}
	}
	fn := func(ctx context.Context, onProgress func(done, total int)) (core.UpdatePlan, error) {
		return bm.BuildBundleInstallPlan(ctx, gid, name, onProgress)
	}
	if err := a.startUpdateFlowWith(gid, core.PlanUpdate, fn, name); err != nil {
		return a.withState(gid, bm, err)
	}
	return a.buildBundleState(gid, bm)
}

// RemoveBundle gates (spec §6.7 order) then deletes the bundle via the
// provider. The provider does not re-check these gates.
func (a *App) RemoveBundle(gameID, name string) (BundleState, error) {
	gid := core.GameID(gameID)
	bm, p, ok := a.bundleManager(gid)
	if !ok {
		return BundleState{Supported: false}, nil
	}
	fail := func(ue *core.UpdateError) (BundleState, error) { return a.withState(gid, bm, ue) }
	if !knownBundles[name] {
		return fail(gateErr("bundle_unknown", name))
	}
	if a.gameRunning(p, gid) {
		return fail(processBlocked(gameID))
	}
	if a.busy(gid) {
		return fail(&core.UpdateError{Code: "bundle_busy", Retryable: true})
	}
	_, st, _, err := bm.BundleState(context.Background(), gid)
	if err != nil {
		return a.withState(gid, bm, err)
	}
	for _, n := range st.Pending {
		if n == name {
			return fail(gateErr("bundle_pending", name))
		}
	}
	if _, inst := st.Installed[name]; !inst {
		return fail(gateErr("bundle_not_installed", name))
	}
	if st.Active == name {
		return fail(gateErr("bundle_in_use", name))
	}
	if knownInstalled(st) <= 1 {
		return fail(gateErr("bundle_last", name))
	}
	return a.withState(gid, bm, bm.RemoveBundle(context.Background(), gid, name))
}

// onBundleInstalled runs on a successful bundle install, before InFlight
// clears: the newly installed bundle becomes active.
func (a *App) onBundleInstalled(gid core.GameID, name string) {
	bm, _, ok := a.bundleManager(gid)
	if !ok {
		return
	}
	if err := bm.SetActiveBundle(context.Background(), gid, name); err != nil {
		a.logger.Warn("onBundleInstalled: set active failed", "game", gid, "bundle", name, "err", err)
	}
}

// validVersionDirName reports whether v is safe to use as a single sidecar
// version-dir path segment (no separators, no "."/"..").
func validVersionDirName(v string) bool {
	return v != "" && v != "." && v != ".." && !strings.ContainsAny(v, `/\`) && filepath.Base(v) == v
}

// DiscardInterrupted drops an interrupted (or residual) update sidecar so the
// user can start over (spec §6.6). With an interrupted_resume LastError it
// removes that version dir; otherwise it looks for a residual sidecar at the
// installed bundles' common version. The path is only ever built through
// sidecarVersionDir from a validated single segment.
func (a *App) DiscardInterrupted(gameID string) error {
	gid := core.GameID(gameID)
	p, err := a.provider(gid)
	if err != nil {
		return err
	}
	state := a.updateRegistry.Get(gid)
	snap := state.Snapshot()
	if snap.InFlight != nil {
		return fmt.Errorf("operation in flight for %s", gid)
	}
	var dir string
	if snap.LastError != nil && snap.LastError.Code == "interrupted_resume" {
		v := snap.LastError.Params["version"]
		if !validVersionDirName(v) {
			return fmt.Errorf("invalid sidecar version %q", v)
		}
		dir = a.sidecarVersionDir(p, gid, v)
	} else {
		bm, ok := p.(core.BundleManager)
		if !ok {
			return fmt.Errorf("nothing to discard for %s", gid)
		}
		_, st, _, berr := bm.BundleState(context.Background(), gid)
		if berr != nil {
			return berr
		}
		common := ""
		for n, v := range st.Installed {
			if !knownBundles[n] || v == "" {
				continue
			}
			if common == "" || versionNewer(common, v) {
				common = v
			}
		}
		if !validVersionDirName(common) {
			return fmt.Errorf("nothing to discard for %s", gid)
		}
		dir = a.sidecarVersionDir(p, gid, common)
		if core.ScanRecovery(dir).Phase == core.RecoveryNone {
			return fmt.Errorf("nothing to discard for %s", gid)
		}
	}
	if err := removeAll(dir); err != nil {
		return err
	}
	state.mu.Lock()
	state.LastError = nil
	state.mu.Unlock()
	a.updateRegistry.EmitTerminal(gid)
	return nil
}

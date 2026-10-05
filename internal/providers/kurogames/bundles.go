package kurogames

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"omnigate/internal/core"
)

func bundleErr(code, name string) *core.UpdateError {
	return &core.UpdateError{Code: code, Retryable: false, Params: map[string]string{"bundle": name}}
}

func (p *Provider) readState(ctx context.Context, gid core.GameID) (string, installState, error) {
	dir, err := p.gameDir(ctx, gid)
	if err != nil {
		return "", installState{}, err
	}
	s, err := readInstallState(filepath.Join(dir, installStateFile))
	return dir, s, err
}

func (p *Provider) BundleState(ctx context.Context, gid core.GameID) (core.BundleCatalog, core.BundleInstallState, bool, error) {
	_, s, err := p.readState(ctx, gid)
	if err != nil {
		return core.BundleCatalog{}, core.BundleInstallState{}, false, err
	}
	st := core.BundleInstallState{Active: resolveActiveBundle(s, p.activeBundleConfig(gid)), Installed: map[string]string{}, Pending: s.pendingKnown()}
	for _, n := range s.installedKnown() {
		st.Installed[n] = s.Bundles[n].Version
	}
	cat, ok := p.loadCatalog(gid)
	return cat, st, ok, nil
}

func (p *Provider) SetActiveBundle(ctx context.Context, gid core.GameID, name string) error {
	if !knownBundle(name) {
		return bundleErr("bundle_unknown", name)
	}
	_, s, err := p.readState(ctx, gid)
	if err != nil {
		return err
	}
	for _, n := range s.pendingKnown() {
		if n == name {
			return bundleErr("bundle_pending", name)
		}
	}
	for _, n := range s.installedKnown() {
		if n == name {
			return p.setActiveBundleConfig(gid, name)
		}
	}
	return bundleErr("bundle_not_installed", name)
}

func (p *Provider) LaunchOptions(_ context.Context, gid core.GameID) (map[string]bool, error) {
	return p.loadLaunchOpts(gid), nil
}

func (p *Provider) SetLaunchOption(_ context.Context, gid core.GameID, cmd string, enabled bool) error {
	m := p.loadLaunchOpts(gid)
	m[cmd] = enabled
	return p.saveLaunchOpts(gid, m)
}

func (p *Provider) BuildBundleInstallPlan(ctx context.Context, gid core.GameID, name string, onProgress func(done, total int)) (core.UpdatePlan, error) {
	if !knownBundle(name) {
		return core.UpdatePlan{}, bundleErr("bundle_unknown", name)
	}
	dir, s, err := p.readState(ctx, gid)
	if err != nil {
		return core.UpdatePlan{}, err
	}
	if len(s.installedKnown()) == 0 {
		return core.UpdatePlan{}, &core.UpdateError{Code: "install_record_missing", Retryable: false}
	}
	idx, err := fetchGameIndexV3(ctx, p.httpClient)
	if err != nil {
		return core.UpdatePlan{}, err
	}
	p.saveCatalog(gid, catalogFromIndex(idx, time.Now()))
	if _, ok := idx.Bundles[name]; !ok {
		return core.UpdatePlan{}, bundleErr("bundle_unknown", name)
	}
	if s.packVersion("common") != idx.ResourcePacks["common"].Version { // authoritative (spec 6.8)
		return core.UpdatePlan{}, bundleErr("bundle_update_first", name)
	}
	plan, err := p.buildPlanV3(ctx, gid, dir, idx, []string{packOf(name)}, func(string) string { return "" }, onProgress)
	if err != nil {
		return core.UpdatePlan{}, err
	}
	plan.Bundle = name
	return plan, nil
}

func (p *Provider) RemoveBundle(ctx context.Context, gid core.GameID, name string) error {
	if !knownBundle(name) {
		return bundleErr("bundle_unknown", name)
	}
	dir, err := p.gameDir(ctx, gid)
	if err != nil {
		return err
	}
	if err := p.removeAll(filepath.Join(dir, "Client", "Content", name)); err != nil && !os.IsNotExist(err) {
		return &core.UpdateError{Code: "bundle_remove_failed", Retryable: true, Params: map[string]string{"bundle": name, "detail": err.Error()}}
	}
	if err := writeInstallState(filepath.Join(dir, installStateFile), func(s *installState) { delete(s.Bundles, name) }); err != nil {
		return err
	}
	if p.activeBundleConfig(gid) == name {
		_ = p.setActiveBundleConfig(gid, "")
	}
	return nil
}

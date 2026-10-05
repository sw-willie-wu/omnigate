package kurogames

import (
	"context"
	"os"
	"path/filepath"

	"omnigate/internal/core"
)

func resolveActiveBundle(s installState, configured string) string {
	inst := s.installedKnown()
	for _, n := range inst {
		if n == configured {
			return n
		}
	}
	if len(inst) > 0 {
		return inst[0]
	}
	return legacyBundle
}

func buildLaunchArgs(active string, cat core.BundleCatalog, haveCat bool, opts map[string]bool) []string {
	args := []string{bundleLaunchArgs[active]}
	if !haveCat {
		return args
	}
	for _, e := range cat.Bundles {
		if e.Name != active {
			continue
		}
		for _, o := range e.Options {
			v, set := opts[o.Cmd]
			if (set && v) || (!set && o.Default) {
				args = append(args, o.Cmd)
			}
		}
	}
	return args
}

func (p *Provider) launchArgsFor(_ context.Context, gid core.GameID, installPath string) []string {
	s, err := readInstallState(filepath.Join(installPath, installStateFile))
	if err != nil {
		p.logger.Warn("kurogames: install state unreadable; launching with legacy bundle", "game", gid, "err", err)
	}
	active := resolveActiveBundle(s, p.activeBundleConfig(gid))
	if len(s.installedKnown()) == 0 {
		p.logger.Warn("kurogames: no install record; launching with legacy bundle", "game", gid)
	}
	if _, err := os.Stat(filepath.Join(installPath, "Client", "Content", active)); err != nil {
		p.logger.Warn("kurogames: active bundle dir missing", "game", gid, "bundle", active)
	}
	cat, ok := p.loadCatalog(gid)
	args := buildLaunchArgs(active, cat, ok, p.loadLaunchOpts(gid))
	p.logger.Info("kurogames launch args", "game", gid, "bundle", active, "args", args)
	return args
}

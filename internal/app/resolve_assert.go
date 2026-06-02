package app

import (
	"omnigate/internal/core"
	"omnigate/internal/providers/hoyoverse"
	"omnigate/internal/providers/hypergryph"
	"omnigate/internal/providers/kurogames"
)

// Compile-time guarantees that every provider satisfies the resolution +
// injection contracts resolveAll depends on. resolveAll injects via runtime
// type assertions (p.(backendScanner) / p.(core.ResolvedPathSetter)); without
// these, a signature drift on DefaultScan/SetResolvedPaths would silently make
// those assertions fail — a provider would then receive no injected paths and
// report every game as not-installed, with no compile error or test failure.
var (
	_ backendScanner          = (*hoyoverse.Provider)(nil)
	_ backendScanner          = (*kurogames.Provider)(nil)
	_ backendScanner          = (*hypergryph.Provider)(nil)
	_ core.ResolvedPathSetter = (*hoyoverse.Provider)(nil)
	_ core.ResolvedPathSetter = (*kurogames.Provider)(nil)
	_ core.ResolvedPathSetter = (*hypergryph.Provider)(nil)
)

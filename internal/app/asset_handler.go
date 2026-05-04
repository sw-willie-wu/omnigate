package app

import (
	"net/http"
	"strings"

	"launcher-collection-tmp/internal/core"
	"launcher-collection-tmp/internal/providers/iconext"
)

// newAssetHandler returns the http.Handler mounted at /_asset/* on the
// existing Wails AssetServer. Dispatches:
//
//   GET /_asset/<backendID>/icon/<key>  → uniform: cachedDetect + iconext.Extract
//   GET /_asset/<backendID>/bg/<key>    → delegate to provider's core.AssetServer
//
// Returns 404 for unknown backends, unknown kinds, "." or ".." in the key
// path component, or providers that don't implement core.AssetServer for bg
// requests.
//
// Adds Cache-Control: max-age=3600 on successful responses; the WebView's
// in-memory cache absorbs repeated same-session requests.
func newAssetHandler(a *App) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Strip "/_asset/" prefix.
		const prefix = "/_asset/"
		if !strings.HasPrefix(r.URL.Path, prefix) {
			http.NotFound(w, r)
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, prefix)
		parts := strings.SplitN(rest, "/", 3)
		if len(parts) != 3 {
			http.NotFound(w, r)
			return
		}
		backendID, kind, key := parts[0], parts[1], parts[2]

		// Validate kind allowlist.
		if kind != "icon" && kind != "bg" {
			http.NotFound(w, r)
			return
		}
		// Reject path-escape attempts in key.
		if key == "" || key == "." || key == ".." ||
			strings.Contains(key, "/") || strings.Contains(key, "\\") ||
			strings.Contains(key, "..") {
			http.NotFound(w, r)
			return
		}

		p := a.byID(core.BackendID(backendID))
		if p == nil {
			http.NotFound(w, r)
			return
		}

		switch kind {
		case "icon":
			a.serveIcon(w, r, p, key)
		case "bg":
			as, ok := p.(core.AssetServer)
			if !ok {
				http.NotFound(w, r)
				return
			}
			data, mime, err := as.ServeAsset(r.Context(), "bg", key)
			if err != nil {
				a.logger.Debug("ServeAsset bg failed", "backend", p.ID(), "key", key, "err", err)
				http.NotFound(w, r)
				return
			}
			w.Header().Set("Content-Type", mime)
			w.Header().Set("Cache-Control", "max-age=3600")
			_, _ = w.Write(data)
		}
	})
}

// serveIcon handles kind=icon uniformly: look up the install, use ExeNamer to
// get the .exe filename, then iconext.Extract(<installPath>/<exeName>).
func (a *App) serveIcon(w http.ResponseWriter, r *http.Request, p core.Provider, key string) {
	installs, err := a.cachedDetect(r.Context(), p)
	if err != nil {
		a.logger.Debug("cachedDetect failed in icon serve", "backend", p.ID(), "err", err)
		http.NotFound(w, r)
		return
	}
	gid := core.GameID(string(p.ID()) + "/" + key)
	var inst *core.InstalledGame
	for i := range installs {
		if installs[i].GameID == gid {
			inst = &installs[i]
			break
		}
	}
	if inst == nil {
		http.NotFound(w, r)
		return
	}
	en, ok := p.(core.ExeNamer)
	if !ok {
		http.NotFound(w, r)
		return
	}
	exeName, ok := en.ExeName(gid)
	if !ok {
		http.NotFound(w, r)
		return
	}
	exePath := inst.InstallPath + "\\" + exeName // Windows-only path semantics
	data, err := iconext.Extract(exePath)
	if err != nil {
		a.logger.Debug("iconext.Extract failed", "exe", exePath, "err", err)
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "max-age=3600")
	_, _ = w.Write(data)
}

// AssetHandlerForApp returns the asset HTTP handler for use as Wails'
// assetserver.Options.Handler. Exported so main.go can mount it.
func AssetHandlerForApp(a *App) http.Handler {
	return newAssetHandler(a)
}

package core

import "context"

// AssetServer is implemented by Providers that serve binary assets (e.g.
// background art bytes from a local launcher cache) over the AssetServer
// middleware's /_asset/<backend>/<kind>/<key> route.
//
// The middleware ONLY ever calls ServeAsset with kind == "bg". The kind ==
// "icon" path is handled uniformly by the middleware itself via the shared
// iconext package + ExeNamer interface — see internal/app/asset_handler.go.
//
// Implementations that don't have local-cache backgrounds (e.g. hoyoverse,
// which returns CDN URLs) do not implement this interface; the middleware
// falls back to 404 on the type-assertion failure.
type AssetServer interface {
	ServeAsset(ctx context.Context, kind, key string) (data []byte, mime string, err error)
}

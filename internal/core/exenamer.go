package core

// ExeNamer is implemented by Providers whose games each have a single primary
// .exe filename relative to the install path. The AssetServer middleware uses
// this to resolve <installPath>/<exeName> for runtime PE icon extraction.
//
// Optional interface — providers without a single canonical exe per game (or
// without runtime icon extraction needs) simply do not implement this; the
// middleware returns 404 for icon URLs in that case.
type ExeNamer interface {
	ExeName(gid GameID) (string, bool) // returns the exe filename and true; false if gid unknown
}

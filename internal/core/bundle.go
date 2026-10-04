package core

import (
	"context"
	"time"
)

// BundleCatalog is the cached, provider-neutral view of a game's resource
// bundles (WuWa quality levels). Persisted by the provider; read offline.
type BundleCatalog struct {
	FetchedAt     time.Time            `json:"fetched_at"`
	CommonVersion string               `json:"common_version"`
	Bundles       []BundleCatalogEntry `json:"bundles"`
}

type BundleCatalogEntry struct {
	Name        string          `json:"name"`
	DisplayName LocalizedString `json:"display_name"`
	PackSize    int64           `json:"pack_size"`
	PackVersion string          `json:"pack_version"`
	Options     []LaunchOption  `json:"options"`
}

type LaunchOption struct {
	Cmd     string          `json:"cmd"`
	Label   LocalizedString `json:"label"`
	Default bool            `json:"default"`
}

// BundleInstallState is the local install view: which bundles are usable,
// which is active, which the official launcher is still processing.
type BundleInstallState struct {
	Active    string            `json:"active"`
	Installed map[string]string `json:"installed"` // name → version
	Pending   []string          `json:"pending"`
}

// BundleManager is implemented by providers whose games ship selectable
// resource bundles (kurogames/WuWa). All methods except
// BuildBundleInstallPlan are offline.
type BundleManager interface {
	BundleState(ctx context.Context, gid GameID) (BundleCatalog, BundleInstallState, bool, error)
	SetActiveBundle(ctx context.Context, gid GameID, name string) error
	LaunchOptions(ctx context.Context, gid GameID) (map[string]bool, error)
	SetLaunchOption(ctx context.Context, gid GameID, cmd string, enabled bool) error
	BuildBundleInstallPlan(ctx context.Context, gid GameID, name string, onProgress func(done, total int)) (UpdatePlan, error)
	RemoveBundle(ctx context.Context, gid GameID, name string) error
}

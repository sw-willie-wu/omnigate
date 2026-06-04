package store

import (
	"time"

	"omnigate/internal/core"
)

// GachaStore persists gacha pulls keyed by (game, uid) with dedup by id, plus a
// per-(game,uid) cache of the last-known history URL. Backend implementation is
// swappable (SQLite today).
type GachaStore interface {
	UpsertPulls(game, uid string, pulls []core.GachaPull) (added int, err error)
	AllPulls(game, uid string) ([]core.GachaPull, error)
	KnownUIDs(game string) ([]string, error)
	LatestUID(game string) (string, error)
	GetURLCache(game, uid string) (url string, fetchedAt time.Time, err error)
	PutURLCache(game, uid, url string) error
	Close() error
}

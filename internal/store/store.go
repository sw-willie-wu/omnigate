package store

import (
	"time"

	"omnigate/internal/core"
)

// GachaStore persists gacha pulls keyed by (game, uid) with dedup by id, plus a
// per-(game,uid) cache of the last-known history URL and a per-game cache of the
// gacha credential. Backend implementation is swappable (SQLite today).
type GachaStore interface {
	UpsertPulls(game, uid string, pulls []core.GachaPull) (added int, err error)
	AllPulls(game, uid string) ([]core.GachaPull, error)
	KnownUIDs(game string) ([]string, error)
	LatestUID(game string) (string, error)
	GetURLCache(game, uid string) (url string, fetchedAt time.Time, err error)
	PutURLCache(game, uid, url string) error
	GetGachaCred(game string) (cred string, updatedAt time.Time, err error)
	PutGachaCred(game, cred string) error
	ListGachaAccounts(game string) ([]GachaAccount, error)
	GetGachaAccount(id string) (GachaAccount, error)
	UpsertGachaAccount(a GachaAccount) error
	SetGachaAccountLabel(id, label string) error
	DeleteGachaAccount(id string) error
	SetActiveGachaAccount(game, id string) error
	Close() error
}

// GachaAccount is one per-account gacha credential (Endfield). Token is the
// durable passport token. UID is the roleId (record-partition key), "" until the
// first refresh writes it back. Active marks the default account (resolved when
// callers pass accountID="").
type GachaAccount struct {
	ID, Game, HgID, UID, Label, Email, Token string
	Active                                    bool
}

// GameOverride is a per-game settings row (install path + background image).
type GameOverride struct{ Path, BackgroundPath string }

// AccountUID is a WuWa account uid-cache row.
type AccountUID struct{ UID, Label string }

// StateStore persists app config + per-game/account state in the SAME DB file
// as gacha (omnigate.db). Implemented by *SQLiteStore. All methods are
// single-statement (or a single short transaction) under MaxOpenConns(1).
type StateStore interface {
	GetMeta(key string) (val string, ok bool, err error)
	SetMeta(key, val string) error
	GetConfig(key string) (val string, ok bool, err error)
	SetConfig(key, val string) error
	AllConfig() (map[string]string, error)
	AllGameSettings() (map[string]GameOverride, error)
	ReplaceGameSettings(m map[string]GameOverride) error
	AllPlaystate() (map[string]int64, error)
	SetPlaystate(game string, unix int64) error
	AllAccountUID() (map[string]AccountUID, error)
	SetAccountUID(cuid, uid, label string) error
}

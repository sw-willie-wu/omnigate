package core

import "time"

// ProgressEntry is one downloaded file's resume metadata after successful
// download + hash verify + atomic rename. mtime+size exact-equality is the
// resume trust check.
type ProgressEntry struct {
	Size  int64     `json:"size"`
	MTime time.Time `json:"mtime"`
	Hash  string    `json:"hash,omitempty"`
}

// ProgressFile is the on-disk shape of progress.json (and predl_ready.json
// after rename — same schema). Provider-agnostic; M3.A kurogames was the
// first consumer, M3.B HoYoverse / M3.C Hypergryph reuse this type.
type ProgressFile struct {
	GameID  string                   `json:"game_id"`
	Version string                   `json:"version"`
	ETag    string                   `json:"etag"`
	Entries map[string]ProgressEntry `json:"entries"`
}

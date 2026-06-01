package hoyoverse

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"omnigate/internal/core"
	pb "omnigate/internal/providers/hoyoverse/sophon/proto"

	"github.com/klauspost/compress/zstd"
	"google.golang.org/protobuf/proto"
)

// appliedManifestSet is the on-disk applied.json index (§A.6 / §4.1).
type appliedManifestSet struct {
	Latest   *appliedBuild `json:"latest"`
	Previous *appliedBuild `json:"previous"`
}

type appliedBuild struct {
	BuildID    string            `json:"build_id"`
	Version    string            `json:"version"`
	AppliedAt  string            `json:"applied_at"`  // RFC3339
	Categories map[string]string `json:"categories"`  // matchingField → build_id
}

// appliedSet is the in-memory wrapper around appliedManifestSet that
// lazy-loads (+ zstd-decompresses + unmarshals) blob files on demand
// (§4.2). The proto is NOT held across CheckForUpdate calls.
type appliedSet struct {
	tempRoot string
	gid      core.GameID
	set      appliedManifestSet
}

func manifestBlobName(buildID, category string) string {
	return buildID + "__" + category + ".manifest.pb.zst"
}

// SaveAppliedManifest writes the raw zstd-proto wire bytes for one
// (build_id, category) into <…/.sophon>/manifests/ (§4.1). Atomic
// tmp→write→rename. It does NOT touch applied.json — RotateAfterApply does.
func SaveAppliedManifest(tempRoot string, gid core.GameID, category, buildID, version string, rawZst []byte) error {
	dir := sophonManifestsDir(tempRoot, gid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	path := filepath.Join(dir, manifestBlobName(buildID, category))
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, rawZst, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// LoadAppliedManifests reads applied.json and returns an in-memory appliedSet.
// Per §E.2 P3 it returns *appliedSet (no error) and NEVER nil: an absent or
// corrupt index yields an empty set, so callers can safely chain
// LoadAppliedManifests(...).MatchByVersion(...) (decision tree / Task 18).
func LoadAppliedManifests(tempRoot string, gid core.GameID) *appliedSet {
	idx, err := loadJSONSidecar[appliedManifestSet](sophonAppliedJSONPath(tempRoot, gid))
	if err != nil || idx == nil {
		return &appliedSet{tempRoot: tempRoot, gid: gid}
	}
	return &appliedSet{tempRoot: tempRoot, gid: gid, set: *idx}
}

// MatchByVersion lazily loads the blob for (version, category) and returns the
// parsed manifest, or nil if no latest/previous slot matches both (§4.2).
func (a *appliedSet) MatchByVersion(version, category string) *pb.SophonManifestProto {
	buildID := a.buildIDFor(version, category)
	if buildID == "" {
		return nil
	}
	path := filepath.Join(sophonManifestsDir(a.tempRoot, a.gid), manifestBlobName(buildID, category))
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	m, err := decodeManifestZst(raw)
	if err != nil {
		return nil
	}
	return m
}

func (a *appliedSet) buildIDFor(version, category string) string {
	for _, b := range []*appliedBuild{a.set.Latest, a.set.Previous} {
		if b == nil || b.Version != version {
			continue
		}
		if bid, ok := b.Categories[category]; ok {
			return bid
		}
	}
	return ""
}

// decodeManifestZst zstd-decompresses then proto-unmarshals a manifest blob.
func decodeManifestZst(rawZst []byte) (*pb.SophonManifestProto, error) {
	dec, err := zstd.NewReader(nil)
	if err != nil {
		return nil, err
	}
	defer dec.Close()
	raw, err := dec.DecodeAll(rawZst, nil)
	if err != nil {
		return nil, fmt.Errorf("zstd decode manifest: %w", err)
	}
	var m pb.SophonManifestProto
	if err := proto.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("proto unmarshal manifest: %w", err)
	}
	return &m, nil
}

// RotateAfterApply promotes build B (version V, per-category build ids) to
// latest, demotes old latest to previous, evicts the old previous's blobs, and
// sweeps orphan blobs (§4.3). Write order: capture-evicted → atomic-write
// applied.json → delete evicted blobs → orphan sweep.
func RotateAfterApply(tempRoot string, gid core.GameID, buildID, version string, newCategories map[string]string) error {
	cur, err := loadJSONSidecar[appliedManifestSet](sophonAppliedJSONPath(tempRoot, gid))
	if err != nil {
		return err
	}
	if cur == nil {
		cur = &appliedManifestSet{}
	}

	// 1. Capture evicted (old previous).
	var evicted *appliedBuild
	if cur.Previous != nil {
		evicted = cur.Previous
	}

	// 2. Build new in-memory applied: latest = B, previous = old latest.
	next := appliedManifestSet{
		Latest: &appliedBuild{
			BuildID:    buildID,
			Version:    version,
			AppliedAt:  time.Now().UTC().Format(time.RFC3339),
			Categories: newCategories,
		},
		Previous: cur.Latest,
	}

	// 3. Atomic write new applied.json.
	if err := writeAppliedJSON(tempRoot, gid, &next); err != nil {
		return err
	}

	// 4. Delete evicted build's blobs.
	manDir := sophonManifestsDir(tempRoot, gid)
	if evicted != nil {
		for _, bid := range evicted.Categories {
			_ = removeBlobsForBuild(manDir, bid)
		}
	}

	// 5. Sweep any blob whose build_id is not referenced by new latest∪previous.
	keep := map[string]bool{}
	for _, b := range []*appliedBuild{next.Latest, next.Previous} {
		if b == nil {
			continue
		}
		for _, bid := range b.Categories {
			keep[bid] = true
		}
	}
	entries, _ := os.ReadDir(manDir)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".manifest.pb.zst") {
			continue
		}
		bid := strings.SplitN(e.Name(), "__", 2)[0]
		if !keep[bid] {
			_ = os.Remove(filepath.Join(manDir, e.Name()))
		}
	}
	return nil
}

func removeBlobsForBuild(manDir, buildID string) error {
	entries, err := os.ReadDir(manDir)
	if err != nil {
		return err
	}
	prefix := buildID + "__"
	for _, e := range entries {
		if !e.IsDir() && strings.HasPrefix(e.Name(), prefix) && strings.HasSuffix(e.Name(), ".manifest.pb.zst") {
			_ = os.Remove(filepath.Join(manDir, e.Name()))
		}
	}
	return nil
}

func writeAppliedJSON(tempRoot string, gid core.GameID, set *appliedManifestSet) error {
	dir := sophonSubdir(tempRoot, gid)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(set, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal applied.json: %w", err)
	}
	path := sophonAppliedJSONPath(tempRoot, gid)
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// cleanupStaleSophonSidecars sweeps version-scoped dirs under gameSidecarDir
// (§4 + §6.2):
//
//	(a) a version dir whose sophon_apply.wal is all-done AND TargetTag ==
//	    currentTag → remove the whole dir (rotate-vs-cleanup crash window).
//	(b) a version dir for a version NOT in allowedTargets AND older than 7
//	    days by mtime → remove (orphan staging from abandoned runs).
//
// The cross-version .sophon dir (dot-prefixed) is always skipped.
func cleanupStaleSophonSidecars(tempRoot string, gid core.GameID, currentTag string, allowedTargets []string) error {
	root := gameSidecarDir(tempRoot, gid)
	entries, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	allowed := map[string]bool{}
	for _, t := range allowedTargets {
		allowed[t] = true
	}
	cutoff := time.Now().Add(-7 * 24 * time.Hour)

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		name := e.Name()
		if strings.HasPrefix(name, ".") { // skip .sophon and any dotfile dir
			continue
		}
		vdir := filepath.Join(root, name)

		// (a) all-done WAL matching currentTag.
		if wal, _ := readSophonApplyWAL(vdir); wal != nil {
			if wal.TargetTag == currentTag && wal.firstPending() == -1 {
				_ = os.RemoveAll(vdir)
				continue
			}
		}

		// (b) orphan: not an allowed target AND aged >7 days.
		if allowed[name] {
			continue
		}
		info, statErr := e.Info()
		if statErr != nil {
			continue
		}
		if info.ModTime().Before(cutoff) {
			_ = os.RemoveAll(vdir)
		}
	}
	return nil
}

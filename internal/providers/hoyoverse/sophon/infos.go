package sophon

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strconv"
)

// Boolish accepts HoYoverse's varied boolean serializations:
// 0|1|"0"|"1"|true|false (spec §2.3, Collapse BoolConverter parity).
type Boolish bool

func (b *Boolish) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	switch string(data) {
	case "true", "1", `"1"`, `"true"`:
		*b = true
		return nil
	case "false", "0", `"0"`, `"false"`, "null", `""`:
		*b = false
		return nil
	}
	return fmt.Errorf("sophon: Boolish: cannot parse %q", string(data))
}

// Int64ish accepts a JSON number OR a quoted number (HoYoverse uses
// AllowReadingFromString for size fields; spec §2.3).
type Int64ish int64

func (n *Int64ish) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if string(data) == "null" {
		*n = 0
		return nil
	}
	s := string(data)
	if len(s) >= 2 && s[0] == '"' && s[len(s)-1] == '"' {
		s = s[1 : len(s)-1]
	}
	if s == "" {
		*n = 0
		return nil
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return fmt.Errorf("sophon: Int64ish: cannot parse %q: %w", string(data), err)
	}
	*n = Int64ish(v)
	return nil
}

// ManifestFileInfo is the `manifest` block of one category identity.
type ManifestFileInfo struct {
	ID               string   `json:"id"`
	Checksum         string   `json:"checksum"`
	CompressedSize   Int64ish `json:"compressed_size"`
	UncompressedSize Int64ish `json:"uncompressed_size"`
}

// ManifestDownloadInfo is a `manifest_download` / `chunk_download` /
// `diff_download` block.
type ManifestDownloadInfo struct {
	URLPrefix   string  `json:"url_prefix"`
	URLSuffix   string  `json:"url_suffix"`
	Password    string  `json:"password"`
	Encryption  Boolish `json:"encryption"`
	Compression Boolish `json:"compression"`
}

// ManifestIdentity is one category's manifest identity within a build.
type ManifestIdentity struct {
	CategoryID       string               `json:"category_id"`
	CategoryName     string               `json:"category_name"`
	MatchingField    string               `json:"matching_field"`
	Manifest         ManifestFileInfo     `json:"manifest"`
	ManifestDownload ManifestDownloadInfo `json:"manifest_download"`
	ChunkDownload    ManifestDownloadInfo `json:"chunk_download"`
	DiffDownload     ManifestDownloadInfo `json:"diff_download"`
}

// BuildResponse is the parsed getBuild / getPatchBuild Data envelope.
type BuildResponse struct {
	BuildID   string             `json:"build_id"`
	Tag       string             `json:"tag"`
	PatchID   string             `json:"patch_id"`
	Manifests []ManifestIdentity `json:"manifests"`
}

// ParseBuildResponse parses the apiEnvelope.Data of getBuild.
func ParseBuildResponse(data []byte) (*BuildResponse, error) {
	var b BuildResponse
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("sophon: parse getBuild: %w", err)
	}
	return &b, nil
}

// ParsePatchResponse parses the apiEnvelope.Data of getPatchBuild (same
// envelope shape; PatchID populated).
func ParsePatchResponse(data []byte) (*BuildResponse, error) {
	var b BuildResponse
	if err := json.Unmarshal(data, &b); err != nil {
		return nil, fmt.Errorf("sophon: parse getPatchBuild: %w", err)
	}
	return &b, nil
}

// ManifestFor returns the manifest identity for the given matching_field
// ("game" | "zh-cn" | ...).
func (b *BuildResponse) ManifestFor(matchingField string) (*ManifestIdentity, bool) {
	for i := range b.Manifests {
		if b.Manifests[i].MatchingField == matchingField {
			return &b.Manifests[i], true
		}
	}
	return nil, false
}

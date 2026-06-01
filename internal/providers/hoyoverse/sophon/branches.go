// Package sophon implements the HoYoverse Sophon chunk-level binary-delta
// download protocol (Genshin 6.0+). This file parses the extended
// getGameBranches response (spec §2.2).
package sophon

import (
	"encoding/json"
	"fmt"
)

// Category identifies one manifest category (game / audio language).
type Category struct {
	ID            string // "10016".."10020"
	MatchingField string // "game" | "zh-cn" | "en-us" | "ja-jp" | "ko-kr"
	Type          string // "CATEGORY_TYPE_RESOURCE" | "CATEGORY_TYPE_AUDIO"
}

// BranchSlot is one branch (main or pre_download) of a Sophon game.
type BranchSlot struct {
	PackageID  string
	Branch     string
	Password   string
	Tag        string
	DiffTags   []string
	Categories []Category
}

// IsEmpty reports whether the slot is absent (HoYoverse omits pre_download
// between releases).
func (s BranchSlot) IsEmpty() bool { return s.PackageID == "" }

// BranchInfo is the parsed getGameBranches result for one game.
type BranchInfo struct {
	Main        BranchSlot
	PreDownload BranchSlot
}

// wire shapes (snake_case getGameBranches Data).
type rawBranchCategory struct {
	CategoryID    string `json:"category_id"`
	MatchingField string `json:"matching_field"`
	Type          string `json:"type"`
}

type rawBranchSlot struct {
	PackageID  string              `json:"package_id"`
	Branch     string              `json:"branch"`
	Password   string              `json:"password"`
	Tag        string              `json:"tag"`
	DiffTags   []string            `json:"diff_tags"`
	Categories []rawBranchCategory `json:"categories"`
}

type rawBranchesData struct {
	GameBranches []struct {
		Game struct {
			ID  string `json:"id"`
			Biz string `json:"biz"`
		} `json:"game"`
		Main        rawBranchSlot `json:"main"`
		PreDownload rawBranchSlot `json:"pre_download"`
	} `json:"game_branches"`
}

func (r rawBranchSlot) toSlot() BranchSlot {
	cats := make([]Category, 0, len(r.Categories))
	for _, c := range r.Categories {
		cats = append(cats, Category{ID: c.CategoryID, MatchingField: c.MatchingField, Type: c.Type})
	}
	return BranchSlot{
		PackageID:  r.PackageID,
		Branch:     r.Branch,
		Password:   r.Password,
		Tag:        r.Tag,
		DiffTags:   r.DiffTags,
		Categories: cats,
	}
}

// ParseBranches parses the apiEnvelope.Data of getGameBranches for one game.
// Spec §2.2: an empty Categories list on a present Main slot is malformed; the
// same emptiness on PreDownload is tolerated (game-category-only predl).
func ParseBranches(data []byte, apiGameID string) (*BranchInfo, error) {
	var raw rawBranchesData
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("sophon: parse getGameBranches: %w", err)
	}
	for _, b := range raw.GameBranches {
		if b.Game.ID != apiGameID {
			continue
		}
		bi := &BranchInfo{Main: b.Main.toSlot(), PreDownload: b.PreDownload.toSlot()}
		if !bi.Main.IsEmpty() && len(bi.Main.Categories) == 0 {
			return nil, fmt.Errorf("sophon: getGameBranches main for %q has no categories (malformed)", apiGameID)
		}
		return bi, nil
	}
	return nil, fmt.Errorf("sophon: getGameBranches: game id %q not in response", apiGameID)
}

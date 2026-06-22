package gachaicon

import (
	"strings"

	"omnigate/internal/core"
)

// ayItem is the shared Project Amber/Yatta item shape {id, rank, name, icon}.
type ayItem struct {
	ID   any    `json:"id"`
	Rank int    `json:"rank"`
	Name string `json:"name"`
	Icon string `json:"icon"`
}
type ayEnvelope struct {
	Data struct {
		Items map[string]ayItem `json:"items"`
	} `json:"data"`
}

// iconURL builds the remote image URL for a resolved icon ref.
func iconURL(gid core.GameID, kind, iconRef string) string {
	switch gid {
	case "hoyoverse/genshin":
		return "https://gi.yatta.moe/assets/UI/" + iconRef + ".png"
	case "hoyoverse/starrail":
		sub := "avatar"
		if kind == "weapon" {
			sub = "equipment"
		}
		return "https://sr.yatta.moe/hsr/assets/UI/" + sub + "/" + iconRef + ".png"
	case "hoyoverse/zzz":
		return zzzIconURL(kind, iconRef)
	case "kurogames/wutheringwaves":
		return wuwaIconURL(kind, iconRef)
	}
	return ""
}

// nanoka.cc (ex-Hakush) image CDN. WuWa & ZZZ icons both live under /assets/<game>/
// and are served as .webp. kind is unused: for WuWa the char/weapon sub-path is
// embedded in the UE object path; for ZZZ the code is self-identifying.

// wuwaIconURL builds the image URL from a WuWa `icon` ref. The ref is an Unreal
// objectpath like "/Game/Aki/UI/UIResources/.../T_Icon..._UI.T_Icon..._UI". nanoka
// drops the "/Game/Aki/UI/" prefix and keeps everything before the first '.' (the
// "path.AssetName" duplicate suffix), then serves <base>/<path>.webp.
func wuwaIconURL(kind, iconRef string) string {
	if iconRef == "" {
		return ""
	}
	if strings.HasPrefix(iconRef, "http://") || strings.HasPrefix(iconRef, "https://") {
		return iconRef
	}
	s := strings.Replace(iconRef, "/Game/Aki/UI/", "", 1)
	if i := strings.IndexByte(s, '.'); i >= 0 {
		s = s[:i]
	}
	if s == "" {
		return ""
	}
	return "https://static.nanoka.cc/assets/ww/" + s + ".webp"
}

// zzzIconURL builds the image URL from a ZZZ `icon` code like "IconRole01" or
// "Weapon_S_1021" (any trailing image extension is stripped), served as .webp.
func zzzIconURL(kind, iconRef string) string {
	if iconRef == "" {
		return ""
	}
	if strings.HasPrefix(iconRef, "http://") || strings.HasPrefix(iconRef, "https://") {
		return iconRef
	}
	if i := strings.LastIndexByte(iconRef, '.'); i >= 0 {
		switch strings.ToLower(iconRef[i:]) {
		case ".png", ".webp", ".jpg", ".jpeg":
			iconRef = iconRef[:i]
		}
	}
	if iconRef == "" {
		return ""
	}
	return "https://static.nanoka.cc/assets/zzz/" + iconRef + ".webp"
}

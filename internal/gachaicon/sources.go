package gachaicon

import "omnigate/internal/core"

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

// STUBS — replaced in Task 3 once the live Hakush icon path is captured. Do not
// build ZZZ/WuWa real URLs here yet.
func zzzIconURL(kind, iconRef string) string  { return "" }
func wuwaIconURL(kind, iconRef string) string { return "" }

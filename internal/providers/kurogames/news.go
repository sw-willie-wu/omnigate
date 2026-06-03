package kurogames

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strings"

	"omnigate/internal/core"
)

// wuwaNewsBaseDefault is the global (oversea) CMS JSON base. CN locale uses a
// different host; we only serve en/zh-tw/zh-cn and route zh-cn to the ZH host.
const (
	wuwaNewsBaseDefault   = "https://hw-media-cdn-mingchao.kurogame.com/akiwebsite/website2.0/json/G152"
	wuwaNewsBaseZHDefault = "https://media-cdn-mingchao.kurogame.com/akiwebsite/website2.0/json/G152"
	wuwaNewsSiteBase      = "https://wutheringwaves.kurogames.com"
)

// wuwaNewsBase is overridable in tests (covers the en/oversea host).
var wuwaNewsBase = wuwaNewsBaseDefault

type wuwaArticle struct {
	ArticleID    int    `json:"articleId"`
	ArticleTitle string `json:"articleTitle"`
	ArticleType  int    `json:"articleType"`
	StartTime    string `json:"startTime"`
	SuggestCover string `json:"suggestCover"`
	Top          int    `json:"top"`
	SortingMark  int    `json:"sortingMark"`
}

func wuwaCategory(articleType int) core.NewsCategory {
	switch articleType {
	case 58:
		return core.NewsAnnounce // Notice
	case 59:
		return core.NewsActivity // Event
	default:
		return core.NewsInfo // 57 News + anything else
	}
}

// wuwaLang maps app lang → (locale segment, base host).
func wuwaLang(appLang string) (string, string) {
	switch appLang {
	case "zh-TW":
		return "zh-tw", wuwaNewsBase
	case "zh-CN":
		return "zh-cn", wuwaNewsBaseZHDefault
	default:
		return "en", wuwaNewsBase
	}
}

// GetNews implements core.NewsProvider via the WuWa official-site CMS feed
// (no auth). Failure degrades to an empty slice.
func (p *Provider) GetNews(ctx context.Context, gid core.GameID, lang string) ([]core.NewsItem, error) {
	if findByID(gid) == nil {
		return []core.NewsItem{}, nil
	}
	loc, base := wuwaLang(lang)
	hc := p.httpClient
	if hc == nil {
		hc = http.DefaultClient
	}
	u := fmt.Sprintf("%s/%s/ArticleMenu.json", base, loc)
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return []core.NewsItem{}, nil
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := hc.Do(req)
	if err != nil {
		return []core.NewsItem{}, nil
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return []core.NewsItem{}, nil
	}
	body, _ := io.ReadAll(resp.Body)
	var arr []wuwaArticle
	if err := json.Unmarshal(body, &arr); err != nil {
		return []core.NewsItem{}, nil
	}
	// official sort: top desc, then sortingMark asc (newest editorial order).
	sort.SliceStable(arr, func(i, j int) bool {
		if arr[i].Top != arr[j].Top {
			return arr[i].Top > arr[j].Top
		}
		return arr[i].SortingMark < arr[j].SortingMark
	})
	out := make([]core.NewsItem, 0, len(arr))
	for _, a := range arr {
		date := a.StartTime
		if i := strings.IndexByte(date, ' '); i > 0 {
			date = date[:i] // "YYYY-MM-DD HH:MM:SS" → "YYYY-MM-DD"
		}
		out = append(out, core.NewsItem{
			Title:     a.ArticleTitle,
			Category:  wuwaCategory(a.ArticleType),
			Date:      date,
			URL:       fmt.Sprintf("%s/%s/main/news/detail/%d", wuwaNewsSiteBase, loc, a.ArticleID),
			Thumbnail: a.SuggestCover,
		})
	}
	return out, nil
}

var _ core.NewsProvider = (*Provider)(nil)

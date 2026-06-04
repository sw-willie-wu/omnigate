package kurogames

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strings"
	"sync"

	"omnigate/internal/core"
)

// WuWa news comes from the official site's static CMS JSON (no auth):
//   - <base>/<lang>/ArticleMenu.json  → the article list (id/title/date/order)
//   - <base>/<lang>/article/<id>.json → per-article detail (category + images)
// The list feed carries NO cover (suggestCover is empty for every article) and
// no category name (only a locale-specific articleType int), so the category
// label and the thumbnail both come from the per-article detail JSON.
const (
	wuwaNewsBaseDefault   = "https://hw-media-cdn-mingchao.kurogame.com/akiwebsite/website2.0/json/G152"
	wuwaNewsBaseZHDefault = "https://media-cdn-mingchao.kurogame.com/akiwebsite/website2.0/json/G152"
	wuwaNewsSiteBase      = "https://wutheringwaves.kurogames.com"
	wuwaNewsMaxItems      = 10 // cap per-article detail fetches
)

// wuwaNewsBase is overridable in tests (covers the en/oversea host).
var wuwaNewsBase = wuwaNewsBaseDefault

type wuwaArticle struct {
	ArticleID    int    `json:"articleId"`
	ArticleTitle string `json:"articleTitle"`
	StartTime    string `json:"startTime"`
	Top          int    `json:"top"`
	SortingMark  int    `json:"sortingMark"`
}

type wuwaArticleDetail struct {
	ArticleTypeName string `json:"articleTypeName"`
	ArticleContent  string `json:"articleContent"`
}

var wuwaImgRe = regexp.MustCompile(`<img[^>]+src=["']([^"']+)["']`)

// wuwaCategory maps the localized articleTypeName (公告/活動/新聞, Notice/Event/
// News, …) to our category. The articleType INT is locale-specific (en 58/57/59
// vs zh-tw 90/89/91), so we key on the name string instead.
func wuwaCategory(typeName string) core.NewsCategory {
	switch typeName {
	case "公告", "Notice":
		return core.NewsAnnounce
	case "活動", "活动", "Event":
		return core.NewsActivity
	default: // 新聞/新闻/News/资讯/… → info
		return core.NewsInfo
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

func wuwaGetJSON(ctx context.Context, hc *http.Client, url string, v any) bool {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return false
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := hc.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return false
	}
	body, _ := io.ReadAll(resp.Body)
	return json.Unmarshal(body, v) == nil
}

// GetNews implements core.NewsProvider via the WuWa official-site CMS feed
// (no auth). It fetches the article list, then per-article detail (concurrently,
// capped) for the category name + first inline image. Failure degrades to an
// empty slice; a per-article detail failure keeps the item (category=info, no
// thumbnail).
func (p *Provider) GetNews(ctx context.Context, gid core.GameID, lang string) ([]core.NewsItem, error) {
	if findByID(gid) == nil {
		return []core.NewsItem{}, nil
	}
	loc, base := wuwaLang(lang)
	hc := p.httpClient
	if hc == nil {
		hc = http.DefaultClient
	}

	var arr []wuwaArticle
	if !wuwaGetJSON(ctx, hc, fmt.Sprintf("%s/%s/ArticleMenu.json", base, loc), &arr) {
		return []core.NewsItem{}, nil
	}
	// official sort: top desc, then sortingMark asc (editorial order).
	sort.SliceStable(arr, func(i, j int) bool {
		if arr[i].Top != arr[j].Top {
			return arr[i].Top > arr[j].Top
		}
		return arr[i].SortingMark < arr[j].SortingMark
	})
	if len(arr) > wuwaNewsMaxItems {
		arr = arr[:wuwaNewsMaxItems]
	}

	out := make([]core.NewsItem, len(arr))
	var wg sync.WaitGroup
	for i := range arr {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			a := arr[i]
			date := a.StartTime
			if k := strings.IndexByte(date, ' '); k > 0 {
				date = date[:k] // "YYYY-MM-DD HH:MM:SS" → "YYYY-MM-DD"
			}
			item := core.NewsItem{
				Title:    a.ArticleTitle,
				Category: core.NewsInfo, // default until detail resolves it
				Date:     date,
				URL:      fmt.Sprintf("%s/%s/main/news/detail/%d", wuwaNewsSiteBase, loc, a.ArticleID),
			}
			var d wuwaArticleDetail
			if wuwaGetJSON(ctx, hc, fmt.Sprintf("%s/%s/article/%d.json", base, loc, a.ArticleID), &d) {
				item.Category = wuwaCategory(d.ArticleTypeName)
				if m := wuwaImgRe.FindStringSubmatch(d.ArticleContent); m != nil {
					item.Thumbnail = m[1]
				}
			}
			out[i] = item
		}(i)
	}
	wg.Wait()
	return out, nil
}

var _ core.NewsProvider = (*Provider)(nil)

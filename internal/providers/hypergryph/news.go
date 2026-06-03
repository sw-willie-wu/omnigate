package hypergryph

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"time"

	"omnigate/internal/core"
)

const (
	endfieldNewsBaseDefault = "https://web-news.gryphline.com"
	endfieldAppCode         = "arknights_endfield_official"
	endfieldSiteBase        = "https://endfield.gryphline.com"
)

// endfieldNewsBase is overridable in tests.
var endfieldNewsBase = endfieldNewsBaseDefault

type endfieldBulletinResp struct {
	Code int    `json:"code"`
	Msg  string `json:"msg"`
	Data struct {
		List []struct {
			Cid         string `json:"cid"`
			Tab         string `json:"tab"`
			Title       string `json:"title"`
			DisplayTime int64  `json:"displayTime"`
			Cover       string `json:"cover"`
		} `json:"list"`
	} `json:"data"`
}

func endfieldTabCategory(tab string) core.NewsCategory {
	switch tab {
	case "notices":
		return core.NewsAnnounce
	case "events":
		return core.NewsActivity
	default:
		return core.NewsInfo // "news" + anything else
	}
}

func endfieldLocale(appLang string) string {
	switch appLang {
	case "zh-TW":
		return "zh-tw"
	case "zh-CN":
		return "zh-cn"
	default:
		return "en-us"
	}
}

// GetNews implements core.NewsProvider via the public Endfield bulletin API
// (no auth). Failure degrades to an empty slice.
func (p *Provider) GetNews(ctx context.Context, gid core.GameID, lang string) ([]core.NewsItem, error) {
	if findByID(gid) == nil {
		return []core.NewsItem{}, nil
	}
	loc := endfieldLocale(lang)
	hc := p.client
	if hc == nil {
		hc = http.DefaultClient
	}
	q := url.Values{}
	q.Set("lang", loc)
	q.Set("code", endfieldAppCode)
	q.Set("page", "1")
	q.Set("pageSize", "12")
	req, err := http.NewRequestWithContext(ctx, "GET", endfieldNewsBase+"/api/bulletin?"+q.Encode(), nil)
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
	var r endfieldBulletinResp
	if err := json.Unmarshal(body, &r); err != nil || r.Code != 0 {
		return []core.NewsItem{}, nil
	}
	out := make([]core.NewsItem, 0, len(r.Data.List))
	for _, e := range r.Data.List {
		out = append(out, core.NewsItem{
			Title:     e.Title,
			Category:  endfieldTabCategory(e.Tab),
			Date:      time.Unix(e.DisplayTime, 0).UTC().Format("2006-01-02"),
			URL:       fmt.Sprintf("%s/%s/news/%s", endfieldSiteBase, loc, e.Cid),
			Thumbnail: e.Cover,
		})
	}
	return out, nil
}

var _ core.NewsProvider = (*Provider)(nil)

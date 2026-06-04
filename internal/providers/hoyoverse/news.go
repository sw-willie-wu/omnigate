package hoyoverse

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

const hoyolabNewsBase = "https://bbs-api-os.hoyolab.com"

// newsAPIBase is the HoYoLab news host; overridable in tests.
var newsAPIBase = hoyolabNewsBase

// gidToGids maps our GameID to the HoYoLab gids param.
var gidToGids = map[core.GameID]string{
	"hoyoverse/genshin":  "2",
	"hoyoverse/starrail": "6",
	"hoyoverse/zzz":      "8",
}

// newsTypeToCategory maps the HoYoLab type param to our category.
var newsTypeToCategory = map[string]core.NewsCategory{
	"1": core.NewsAnnounce,
	"2": core.NewsActivity,
	"3": core.NewsInfo,
}

type hoyolabNewsResp struct {
	Retcode int    `json:"retcode"`
	Message string `json:"message"`
	Data    struct {
		List []struct {
			Post struct {
				PostID    string `json:"post_id"`
				Subject   string `json:"subject"`
				CreatedAt int64  `json:"created_at"`
				Cover     string `json:"cover"`
			} `json:"post"`
			ImageList []struct {
				URL string `json:"url"`
			} `json:"image_list"`
		} `json:"list"`
	} `json:"data"`
}

func hoyolabLang(appLang string) string {
	switch appLang {
	case "zh-TW":
		return "zh-tw"
	case "zh-CN":
		return "zh-cn"
	default:
		return "en-us"
	}
}

// GetNews implements core.NewsProvider via the public HoYoLab getNewsList API
// (no auth). It calls type 1/2/3 and merges. Any per-call failure is skipped;
// the method returns whatever it could gather (best-effort, never fatal).
func (p *Provider) GetNews(ctx context.Context, gid core.GameID, lang string) ([]core.NewsItem, error) {
	gids, ok := gidToGids[gid]
	if !ok {
		return []core.NewsItem{}, nil
	}
	hc := p.httpClient
	if hc == nil {
		hc = http.DefaultClient
	}
	xlang := hoyolabLang(lang)
	out := []core.NewsItem{}
	for _, typ := range []string{"1", "2", "3"} {
		q := url.Values{}
		q.Set("gids", gids)
		q.Set("type", typ)
		q.Set("page_size", "8")
		req, err := http.NewRequestWithContext(ctx, "GET", newsAPIBase+"/community/post/wapi/getNewsList?"+q.Encode(), nil)
		if err != nil {
			continue
		}
		req.Header.Set("x-rpc-language", xlang)
		req.Header.Set("User-Agent", UserAgent)
		resp, err := hc.Do(req)
		if err != nil {
			continue
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		if resp.StatusCode != 200 {
			continue
		}
		var r hoyolabNewsResp
		if err := json.Unmarshal(body, &r); err != nil || r.Retcode != 0 {
			continue
		}
		cat := newsTypeToCategory[typ]
		for _, e := range r.Data.List {
			thumb := e.Post.Cover
			if len(e.ImageList) > 0 && e.ImageList[0].URL != "" {
				thumb = e.ImageList[0].URL
			}
			out = append(out, core.NewsItem{
				Title:     e.Post.Subject,
				Category:  cat,
				Date:      time.Unix(e.Post.CreatedAt, 0).UTC().Format("2006-01-02"),
				URL:       fmt.Sprintf("https://www.hoyolab.com/article/%s", e.Post.PostID),
				Thumbnail: thumb,
			})
		}
	}
	return out, nil
}

var _ core.NewsProvider = (*Provider)(nil)

package gachaicon

import (
	"crypto/hmac"
	"crypto/md5"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"time"
)

// skportBaseURL is the Gryphline wiki API host; overridable in tests via SetSkportBaseForTest.
var skportBaseURL = "https://zonai.skport.com"

const skportUA = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/125.0.6422.112 Safari/537.36"

// skportSign reproduces the wiki's request signature (RE-verified, golden-tested):
//
//	sign = MD5( hex( HmacSHA256( path + query + ts + headerJSON, token ) ) )
//
// headerJSON MUST be a literal string with key order platform,timestamp,dId,vName —
// do NOT json.Marshal a map (Go sorts keys → breaks the sign). query excludes the leading '?'.
func skportSign(path, query, token, ts string) string {
	hdr := `{"platform":"3","timestamp":"` + ts + `","dId":"","vName":"1.0.0"}`
	s := path + query + ts + hdr
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write([]byte(s))
	inner := hex.EncodeToString(mac.Sum(nil))
	sum := md5.Sum([]byte(inner))
	return hex.EncodeToString(sum[:])
}

// skportGuestToken fetches the anonymous guest token (the HMAC key). No login/sign needed.
func (m *Manager) skportGuestToken() (string, error) {
	req, err := http.NewRequest("GET", skportBaseURL+"/web/v1/auth/refresh", nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", skportUA)
	resp, err := m.hc.Do(req)
	if err != nil {
		return "", fmt.Errorf("skport auth request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("skport auth status %d", resp.StatusCode)
	}
	var r struct {
		Code int `json:"code"`
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return "", fmt.Errorf("skport auth decode")
	}
	if r.Code != 0 || r.Data.Token == "" {
		return "", fmt.Errorf("skport auth code %d", r.Code)
	}
	return r.Data.Token, nil
}

// skportGet does a signed GET. NEVER puts sign/timestamp/token in errors. query excludes the leading '?'.
func (m *Manager) skportGet(path, query, token, lang string) ([]byte, error) {
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sign := skportSign(path, query, token, ts)
	u := skportBaseURL + path
	if query != "" {
		u += "?" + query
	}
	req, err := http.NewRequest("GET", u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("sign", sign)
	req.Header.Set("timestamp", ts)
	req.Header.Set("platform", "3")
	req.Header.Set("vname", "1.0.0")
	req.Header.Set("sk-language", lang)
	req.Header.Set("content-type", "application/json")
	req.Header.Set("origin", "https://wiki.skport.com")
	req.Header.Set("referer", "https://wiki.skport.com/")
	req.Header.Set("User-Agent", skportUA)
	resp, err := m.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("skport request failed")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("skport status %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

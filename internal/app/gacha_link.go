package app

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	wruntime "github.com/wailsapp/wails/v2/pkg/runtime"

	"omnigate/internal/core"
)

const gachaLinkTTL = 15 * time.Minute

type gachaLinkSession struct {
	srv   *http.Server
	timer *time.Timer
}

// GachaLinkInfo is returned to the frontend to render the link panel.
type GachaLinkInfo struct {
	Bookmarklet string `json:"bookmarklet"`
	LoginURL    string `json:"loginUrl"`
	Port        int    `json:"port"`
}

func newNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// startGachaLinkListener starts the one-shot 127.0.0.1 listener (no browser/ctx);
// returns the port + nonce. onLinked(game) fires after a valid capture.
func (a *App) startGachaLinkListener(game string, onLinked func(string)) (int, string, error) {
	a.stopGachaLink() // cancel any prior session
	nonce, err := newNonce()
	if err != nil {
		return 0, "", err
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, "", err
	}
	port := ln.Addr().(*net.TCPAddr).Port

	mux := http.NewServeMux()
	mux.HandleFunc("/cb", func(w http.ResponseWriter, r *http.Request) {
		// PNA + CORS preflight.
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Private-Network", "true")
		w.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "*")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}
		if r.URL.Query().Get("n") != nonce {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		body, _ := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		token := strings.TrimSpace(string(body))
		if token == "" {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		if err := a.gachaStore.PutGachaCred(game, token); err != nil {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write([]byte("<h3>已連結，可關閉此分頁並回到 omnigate</h3>"))
		if onLinked != nil {
			onLinked(game)
		}
		go a.stopGachaLink() // single-use
	})

	srv := &http.Server{Handler: mux}
	sess := &gachaLinkSession{srv: srv}
	sess.timer = time.AfterFunc(gachaLinkTTL, func() { a.stopGachaLink() })

	a.gachaLinkMu.Lock()
	a.gachaLink = sess
	a.gachaLinkMu.Unlock()

	go srv.Serve(ln) //nolint:errcheck // Serve returns on Shutdown/Close
	return port, nonce, nil
}

// stopGachaLink shuts down + clears the active session (idempotent).
func (a *App) stopGachaLink() {
	a.gachaLinkMu.Lock()
	sess := a.gachaLink
	a.gachaLink = nil
	a.gachaLinkMu.Unlock()
	if sess == nil {
		return
	}
	if sess.timer != nil {
		sess.timer.Stop()
	}
	_ = sess.srv.Close()
}

// StartGachaLink (RPC): start the listener, open the login page, return the
// bookmarklet (port+nonce baked in) for the link panel.
func (a *App) StartGachaLink(gameID string) (GachaLinkInfo, error) {
	gid := core.GameID(gameID)
	p, err := a.provider(gid)
	if err != nil {
		return GachaLinkInfo{}, err
	}
	if _, ok := p.(core.GachaCredentialProvider); !ok {
		return GachaLinkInfo{}, core.ErrGachaURLUnavailable // not a credential game
	}
	if a.gachaStore == nil {
		return GachaLinkInfo{}, fmt.Errorf("gacha store unavailable")
	}
	port, nonce, err := a.startGachaLinkListener(gameID, func(g string) { a.emit("gacha:linked", g) })
	if err != nil {
		return GachaLinkInfo{}, err
	}
	loginURL := "https://user.gryphline.com/"
	if a.ctx != nil {
		wruntime.BrowserOpenURL(a.ctx, loginURL)
	}
	bm := buildGachaBookmarklet(port, nonce)
	return GachaLinkInfo{Bookmarklet: bm, LoginURL: loginURL, Port: port}, nil
}

// SetGachaCredential (RPC): manual-paste fallback. Stores the token + signals linked.
func (a *App) SetGachaCredential(gameID, credential string) error {
	gid := core.GameID(gameID)
	p, err := a.provider(gid)
	if err != nil {
		return err
	}
	if _, ok := p.(core.GachaCredentialProvider); !ok {
		return core.ErrGachaURLUnavailable
	}
	if a.gachaStore == nil {
		return fmt.Errorf("gacha store unavailable")
	}
	credential = strings.TrimSpace(credential)
	if credential == "" {
		return core.ErrGachaCredentialRequired
	}
	if err := a.gachaStore.PutGachaCred(gameID, credential); err != nil {
		return err
	}
	a.emit("gacha:linked", gameID)
	return nil
}

// buildGachaBookmarklet returns a javascript: URL that, run on a gryphline.com
// page, reads cookie_store/account_token (first-party) and POSTs it to the
// listener (loopback POST; mixed-content-exempt; PNA preflight answered by /cb).
func buildGachaBookmarklet(port int, nonce string) string {
	js := fmt.Sprintf(`javascript:(function(){`+
		`fetch('https://web-api.gryphline.com/cookie_store/account_token',{credentials:'include'})`+
		`.then(function(r){return r.json()})`+
		`.then(function(d){var t=(d&&d.data&&d.data.content)||'';`+
		`if(!t){alert('未取得 token：請先在此分頁登入 Gryphline');return;}`+
		`fetch('http://127.0.0.1:%d/cb?n=%s',{method:'POST',mode:'no-cors',body:t})`+
		`.then(function(){alert('已回傳 omnigate，可關閉分頁');});})`+
		`.catch(function(e){alert('擷取失敗：'+e);});})();`, port, nonce)
	return js
}

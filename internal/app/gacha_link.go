package app

import (
	"encoding/json"
	"fmt"
	"strings"

	"omnigate/internal/core"
	"omnigate/internal/store"
)

// SetGachaCredential (RPC): manual-paste fallback — create a per-account row from a
// pasted account_token, then refresh to populate its roleId uid. Shares the
// write-back path with AddGachaAccountByLogin (Task 6).
// Returns the fully-populated account (with UID written back after the refresh).
func (a *App) SetGachaCredential(gameID, credential string) (store.GachaAccount, error) {
	gid := core.GameID(gameID)
	p, err := a.provider(gid)
	if err != nil {
		return store.GachaAccount{}, err
	}
	if _, ok := p.(core.GachaCredentialProvider); !ok {
		return store.GachaAccount{}, core.ErrGachaURLUnavailable // not a credential game
	}
	if a.gachaStore == nil {
		return store.GachaAccount{}, fmt.Errorf("gacha store unavailable")
	}
	token := extractAccountToken(credential)
	if token == "" {
		return store.GachaAccount{}, core.ErrGachaCredentialRequired
	}
	ctx, cancel := a.gachaCtx()
	defer cancel()
	hgID, email, label := a.resolveCredentialIdentity(ctx, gid, token, "", "")
	acc, err := a.upsertDedupedCredentialAccount(gameID, hgID, email, label, token)
	if err != nil {
		return store.GachaAccount{}, err
	}
	if err := a.refreshAndWriteBackUID(ctx, gid, &acc); err != nil {
		return acc, err
	}
	return acc, nil
}

// extractAccountToken normalises a captured/pasted credential: it accepts either
// the bare account_token or the whole cookie_store/account_token JSON response
// ({"data":{"content":"<token>"}}, or a top-level {"content":...}) and returns
// the token. Non-JSON input is returned trimmed as-is, so a bare token still works.
func extractAccountToken(s string) string {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "{") {
		return s
	}
	var p struct {
		Content string `json:"content"`
		Data    struct {
			Content string `json:"content"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(s), &p); err != nil {
		return s // not parseable JSON → treat as a raw token
	}
	if p.Data.Content != "" {
		return p.Data.Content
	}
	if p.Content != "" {
		return p.Content
	}
	return s // JSON but no content field → leave untouched
}

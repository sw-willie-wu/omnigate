package kurogames

import (
	"encoding/json"
	"fmt"

	"omnigate/internal/core"
)

// krsdkCache is the on-disk KRSDKUserCache.json shape. Only the fields we need
// are mapped; token is intentionally NOT mapped so it never enters memory.
type krsdkCache struct {
	AccountList []struct {
		Cuid     json.Number `json:"cuid"`
		Email    string      `json:"email"`
		Username string      `json:"username"`
	} `json:"account_list"`
	LastLoginCuid string `json:"last_login_cuid"` // a quoted string in the file
}

// parseKRSDKAccounts maps the KRSDK cache JSON to []core.GameAccount. The active
// account (cuid == last_login_cuid) is flagged. UID is left "" (enriched later).
func parseKRSDKAccounts(data []byte) ([]core.GameAccount, error) {
	var c krsdkCache
	if err := json.Unmarshal(data, &c); err != nil {
		return nil, fmt.Errorf("parse KRSDK cache: %w", err)
	}
	out := make([]core.GameAccount, 0, len(c.AccountList))
	for _, a := range c.AccountList {
		id := a.Cuid.String()
		out = append(out, core.GameAccount{
			ID:       id,
			Email:    a.Email,
			Username: a.Username,
			Active:   id == c.LastLoginCuid,
		})
	}
	return out, nil
}

package hypergryph

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"omnigate/internal/core"
)

func TestLoginByEmailPassword(t *testing.T) {
	var gotBody map[string]string
	var gotHeaders http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/user/auth/v1/token_by_email_password") {
			w.WriteHeader(404)
			return
		}
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		gotHeaders = r.Header.Clone()
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"token":"TOK","hgId":"HG","email":"a@b.com"},"status":0,"msg":"OK"}`))
	}))
	defer srv.Close()

	p := New(Settings{}, nil)
	p.oauthBase = srv.URL // login lives under oauthBase (as.gryphline.com)

	res, err := p.LoginByEmailPassword(context.Background(), "a@b.com", "pw")
	if err != nil {
		t.Fatalf("login err: %v", err)
	}
	if res.Token != "TOK" || res.HgID != "HG" || res.Email != "a@b.com" {
		t.Fatalf("res = %+v", res)
	}
	if gotBody["email"] != "a@b.com" || gotBody["password"] != "pw" {
		t.Fatalf("body = %+v (must be plaintext email+password)", gotBody)
	}
	if gotHeaders.Get("X-AppCode") == "" || gotHeaders.Get("X-DeviceId") == "" {
		t.Fatalf("missing required headers: %v", gotHeaders)
	}
}

func TestLoginByEmailPassword_BadCreds(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":null,"status":1,"msg":"invalid"}`))
	}))
	defer srv.Close()
	p := New(Settings{}, nil)
	p.oauthBase = srv.URL
	_, err := p.LoginByEmailPassword(context.Background(), "a", "b")
	if !errors.Is(err, core.ErrGachaLoginFailed) {
		t.Fatalf("err = %v, want ErrGachaLoginFailed", err)
	}
}

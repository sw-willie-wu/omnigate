package hypergryph

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
)

func TestReadLocalVersion_FromConfigIni(t *testing.T) {
	dir := t.TempDir()
	writeEncryptedConfig(t, dir, "1.2.3")
	got, err := readLocalVersion(dir)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != "1.2.3" {
		t.Fatalf("got %q want 1.2.3", got)
	}
}

func TestReadLocalVersion_MissingReturnsEmpty(t *testing.T) {
	got, err := readLocalVersion(t.TempDir())
	if err != nil {
		t.Fatalf("missing should not error: %v", err)
	}
	if got != "" {
		t.Fatalf("got %q want empty", got)
	}
}

func TestFetchVersion_PopulatesLatestFromServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"action": 1, "version": "1.2.9", "pkg": map[string]any{"packs": []any{}}})
	}))
	defer srv.Close()
	SetAPIBaseURL(srv.URL)
	defer SetAPIBaseURL("")

	dir := t.TempDir()
	writeEncryptedConfig(t, dir, "1.2.3")
	vi, err := fetchVersion(context.Background(), srv.Client(), dir, "endfield/global")
	if err != nil {
		t.Fatalf("fetchVersion: %v", err)
	}
	if vi.Current != "1.2.3" || vi.Latest != "1.2.9" {
		t.Fatalf("got %+v", vi)
	}
}

func TestFetchVersion_NoLocalSource_Degrades(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"action": 1, "version": "1.2.9", "pkg": map[string]any{"packs": []any{}}})
	}))
	defer srv.Close()
	SetAPIBaseURL(srv.URL)
	defer SetAPIBaseURL("")

	vi, err := fetchVersion(context.Background(), srv.Client(), t.TempDir(), "endfield/global")
	if err != nil {
		t.Fatalf("degrade should not error: %v", err)
	}
	if vi.Current != "" {
		t.Fatalf("degrade: Current must be empty (spec §6), got %q", vi.Current)
	}
	if vi.Latest != "1.2.9" {
		t.Fatalf("degrade: Latest should be populated, got %q", vi.Latest)
	}
}

// writeEncryptedConfig writes a real AES-256-CBC-encrypted config.ini (same
// key/IV as crypto.go) so readLocalVersion exercises the actual decrypt path.
func writeEncryptedConfig(t *testing.T, dir, version string) {
	t.Helper()
	plain := []byte("[Game]\nversion=" + version + "\nentry=Endfield.exe\n")
	block, err := aes.NewCipher(endfieldAESKey)
	if err != nil {
		t.Fatal(err)
	}
	bs := block.BlockSize()
	pad := bs - len(plain)%bs
	padded := append(plain, bytes.Repeat([]byte{byte(pad)}, pad)...)
	ct := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, endfieldAESIV).CryptBlocks(ct, padded)
	if err := os.WriteFile(filepath.Join(dir, "config.ini"), ct, 0o644); err != nil {
		t.Fatal(err)
	}
}

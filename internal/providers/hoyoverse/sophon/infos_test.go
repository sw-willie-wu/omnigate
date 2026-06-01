package sophon

import (
	"encoding/json"
	"testing"
)

func TestBoolishForms(t *testing.T) {
	cases := map[string]bool{
		`0`: false, `1`: true, `"0"`: false, `"1"`: true,
		`true`: true, `false`: false,
	}
	for in, want := range cases {
		var b Boolish
		if err := json.Unmarshal([]byte(in), &b); err != nil {
			t.Fatalf("Boolish(%s): %v", in, err)
		}
		if bool(b) != want {
			t.Fatalf("Boolish(%s) = %v, want %v", in, bool(b), want)
		}
	}
	var b Boolish
	if err := json.Unmarshal([]byte(`"yes"`), &b); err == nil {
		t.Fatal("Boolish should reject \"yes\"")
	}
}

func TestInt64ishForms(t *testing.T) {
	var n Int64ish
	if err := json.Unmarshal([]byte(`123`), &n); err != nil || n != 123 {
		t.Fatalf("Int64ish(123) = %d err=%v", n, err)
	}
	if err := json.Unmarshal([]byte(`"123"`), &n); err != nil || n != 123 {
		t.Fatalf(`Int64ish("123") = %d err=%v`, n, err)
	}
	if err := json.Unmarshal([]byte(`"x"`), &n); err == nil {
		t.Fatal(`Int64ish("x") should error`)
	}
}

const buildSmall = `{
  "build_id": "B66", "tag": "6.6.0", "patch_id": "",
  "manifests": [
    {
      "category_id": "10016", "category_name": "game", "matching_field": "game",
      "manifest": {"id": "M1", "checksum": "ck", "compressed_size": "12345", "uncompressed_size": 67890},
      "manifest_download": {"url_prefix": "https://m/", "url_suffix": "", "password": "", "encryption": 0, "compression": 1},
      "chunk_download": {"url_prefix": "https://c/", "url_suffix": "", "password": "", "encryption": "0", "compression": "1"},
      "diff_download": {"url_prefix": "https://d/", "compression": false}
    }
  ]
}`

func TestParseBuildResponse(t *testing.T) {
	b, err := ParseBuildResponse([]byte(buildSmall))
	if err != nil {
		t.Fatalf("ParseBuildResponse: %v", err)
	}
	if b.BuildID != "B66" || b.Tag != "6.6.0" {
		t.Fatalf("build header: %+v", b)
	}
	id, ok := b.ManifestFor("game")
	if !ok {
		t.Fatal("ManifestFor(game) miss")
	}
	if id.Manifest.ID != "M1" || id.Manifest.CompressedSize != 12345 || id.Manifest.UncompressedSize != 67890 {
		t.Fatalf("manifest file info: %+v", id.Manifest)
	}
	if !bool(id.ManifestDownload.Compression) || bool(id.ManifestDownload.Encryption) {
		t.Fatalf("manifest_download flags: %+v", id.ManifestDownload)
	}
	if !bool(id.ChunkDownload.Compression) {
		t.Fatalf("chunk_download compression (quoted) not parsed: %+v", id.ChunkDownload)
	}
	if bool(id.DiffDownload.Compression) {
		t.Fatalf("diff_download compression should be false: %+v", id.DiffDownload)
	}
	if _, ok := b.ManifestFor("ko-kr"); ok {
		t.Fatal("ManifestFor(ko-kr) should miss")
	}
}

func TestParsePatchResponsePatchID(t *testing.T) {
	const j = `{"build_id":"B67","tag":"6.7.0","patch_id":"P1","manifests":[]}`
	b, err := ParsePatchResponse([]byte(j))
	if err != nil {
		t.Fatalf("ParsePatchResponse: %v", err)
	}
	if b.PatchID != "P1" {
		t.Fatalf("patch_id: %+v", b)
	}
}

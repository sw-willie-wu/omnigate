package hypergryph

import "testing"

func TestAESRoundTrip(t *testing.T) {
	plain := []byte("[Game]\nversion=1.2.5\nentry=Endfield.exe\n")
	ct, err := encryptAESCBC(plain)
	if err != nil {
		t.Fatalf("encrypt: %v", err)
	}
	if len(ct) == 0 || len(ct)%16 != 0 {
		t.Fatalf("ciphertext length %d not a block multiple", len(ct))
	}
	got, err := decryptAESCBC(ct)
	if err != nil {
		t.Fatalf("decrypt: %v", err)
	}
	if string(got) != string(plain) {
		t.Errorf("round-trip mismatch: %q", got)
	}
}

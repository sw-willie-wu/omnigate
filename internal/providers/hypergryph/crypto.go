package hypergryph

import (
	"crypto/aes"
	"crypto/cipher"
	"errors"
	"fmt"
	"os"
)

// Endfield stores config.ini and the game_files manifest AES-256-CBC encrypted
// (PKCS7). The key + IV are a reverse-engineered constant ALREADY PUBLICLY
// PUBLISHED in the Collapse Launcher plugin
// "misaka10843/Hi3Helper.Plugin.Hypergryph"
// (Hi3Helper.Hypergryph.Core/Utils/HgCrypto.cs). Hardcoded here, with
// attribution, ONLY to interoperate with the official GRYPHLINK launcher's
// local config format — mirroring how kurogames hardcodes its reverse-engineered
// AppCred. No secret is newly disclosed. If omnigate is published and this draws
// concern, switch to build-time/runtime injection.
var (
	endfieldAESKey = []byte{
		0xC0, 0xF3, 0x0E, 0x1C, 0xE7, 0x63, 0xBB, 0xC2, 0x1C, 0xC3, 0x55, 0xA3, 0x43, 0x03, 0xAC, 0x50,
		0x39, 0x94, 0x44, 0xBF, 0xF6, 0x8C, 0x4A, 0x22, 0xAF, 0x39, 0x8C, 0x0A, 0x16, 0x6E, 0xE1, 0x43,
	}
	endfieldAESIV = []byte{
		0x33, 0x46, 0x78, 0x61, 0x19, 0x27, 0x50, 0x64, 0x95, 0x01, 0x93, 0x72, 0x64, 0x60, 0x84, 0x00,
	}
)

// decryptAESCBC decrypts AES-256-CBC + PKCS7 ciphertext with the Endfield key/IV.
func decryptAESCBC(ciphertext []byte) ([]byte, error) {
	block, err := aes.NewCipher(endfieldAESKey)
	if err != nil {
		return nil, err
	}
	bs := block.BlockSize()
	if len(ciphertext) == 0 || len(ciphertext)%bs != 0 {
		return nil, fmt.Errorf("invalid ciphertext length %d", len(ciphertext))
	}
	out := make([]byte, len(ciphertext))
	cipher.NewCBCDecrypter(block, endfieldAESIV).CryptBlocks(out, ciphertext)
	return pkcs7Unpad(out, bs)
}

// decryptConfigFile reads + AES-decrypts a file to a UTF-8 string. Missing file
// → ("", nil). Decrypt failure → error.
func decryptConfigFile(path string) (string, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	plain, err := decryptAESCBC(data)
	if err != nil {
		return "", fmt.Errorf("hypergryph: decrypt %s: %w", path, err)
	}
	return string(plain), nil
}

func pkcs7Unpad(b []byte, blockSize int) ([]byte, error) {
	if len(b) == 0 {
		return nil, errors.New("empty plaintext")
	}
	pad := int(b[len(b)-1])
	if pad == 0 || pad > blockSize || pad > len(b) {
		return nil, fmt.Errorf("invalid pkcs7 padding %d", pad)
	}
	for _, c := range b[len(b)-pad:] {
		if int(c) != pad {
			return nil, errors.New("invalid pkcs7 padding bytes")
		}
	}
	return b[:len(b)-pad], nil
}

// encryptAESCBC encrypts plaintext with the Endfield key/IV (AES-256-CBC + PKCS7).
// Inverse of decryptAESCBC — used for config.ini version writeback (spec §5/§6).
func encryptAESCBC(plaintext []byte) ([]byte, error) {
	block, err := aes.NewCipher(endfieldAESKey)
	if err != nil {
		return nil, err
	}
	bs := block.BlockSize()
	padded := pkcs7Pad(plaintext, bs)
	out := make([]byte, len(padded))
	cipher.NewCBCEncrypter(block, endfieldAESIV).CryptBlocks(out, padded)
	return out, nil
}

// pkcs7Pad appends PKCS7 padding to a multiple of blockSize.
func pkcs7Pad(b []byte, blockSize int) []byte {
	pad := blockSize - (len(b) % blockSize)
	out := make([]byte, len(b)+pad)
	copy(out, b)
	for i := len(b); i < len(out); i++ {
		out[i] = byte(pad)
	}
	return out
}

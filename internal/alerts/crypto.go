package alerts

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

var ErrEncryptionNotConfigured = errors.New("MYPROBE_ENCRYPTION_KEY must contain at least 32 characters")

type cryptoBox struct {
	aead cipher.AEAD
}

func newCryptoBox(secret string) (*cryptoBox, error) {
	if len(secret) < 32 {
		return nil, ErrEncryptionNotConfigured
	}
	key := sha256.Sum256([]byte(secret))
	block, err := aes.NewCipher(key[:])
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &cryptoBox{aead: aead}, nil
}

func (b *cryptoBox) seal(plaintext []byte) (string, error) {
	nonce := make([]byte, b.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := b.aead.Seal(nonce, nonce, plaintext, []byte("myprobe-notification-v1"))
	return "v1:" + base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (b *cryptoBox) open(value string) ([]byte, error) {
	if !strings.HasPrefix(value, "v1:") {
		return nil, errors.New("unsupported encrypted configuration version")
	}
	raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, "v1:"))
	if err != nil || len(raw) < b.aead.NonceSize() {
		return nil, errors.New("invalid encrypted configuration")
	}
	nonce, ciphertext := raw[:b.aead.NonceSize()], raw[b.aead.NonceSize():]
	plaintext, err := b.aead.Open(nil, nonce, ciphertext, []byte("myprobe-notification-v1"))
	if err != nil {
		return nil, fmt.Errorf("decrypt notification configuration: %w", err)
	}
	return plaintext, nil
}

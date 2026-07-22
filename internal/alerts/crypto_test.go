package alerts

import (
	"strings"
	"testing"
)

func TestCryptoBoxRoundTripAndTamperDetection(t *testing.T) {
	box, err := newCryptoBox(strings.Repeat("k", 32))
	if err != nil {
		t.Fatal(err)
	}
	plaintext := []byte(`{"url":"https://example.com/secret"}`)
	sealed, err := box.seal(plaintext)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sealed, "example.com") {
		t.Fatal("ciphertext contains plaintext")
	}
	opened, err := box.open(sealed)
	if err != nil || string(opened) != string(plaintext) {
		t.Fatalf("opened = %q, error = %v", opened, err)
	}
	tampered := sealed[:len(sealed)-1] + "A"
	if _, err := box.open(tampered); err == nil {
		t.Fatal("tampered ciphertext was accepted")
	}
}

func TestCryptoBoxRequiresStrongSecret(t *testing.T) {
	if _, err := newCryptoBox("short"); err != ErrEncryptionNotConfigured {
		t.Fatalf("error = %v", err)
	}
}

package backup

import (
	"bytes"
	"strings"
	"testing"
)

func TestEncryptDecryptRoundTrip(t *testing.T) {
	source := bytes.Repeat([]byte("myprobe-backup\x00"), 140000)
	var encrypted bytes.Buffer
	if err := Encrypt(&encrypted, bytes.NewReader(source), "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(encrypted.Bytes(), []byte("myprobe-backup")) {
		t.Fatal("encrypted backup contains plaintext")
	}
	var decrypted bytes.Buffer
	if err := Decrypt(&decrypted, bytes.NewReader(encrypted.Bytes()), "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(source, decrypted.Bytes()) {
		t.Fatal("decrypted backup differs from source")
	}
}

func TestDecryptRejectsWrongPassphraseTamperingAndTruncation(t *testing.T) {
	var encrypted bytes.Buffer
	if err := Encrypt(&encrypted, strings.NewReader("sensitive database"), "correct horse battery staple"); err != nil {
		t.Fatal(err)
	}
	if err := Decrypt(&bytes.Buffer{}, bytes.NewReader(encrypted.Bytes()), "incorrect passphrase"); err == nil {
		t.Fatal("wrong passphrase was accepted")
	}
	tampered := append([]byte(nil), encrypted.Bytes()...)
	tampered[len(tampered)-8] ^= 0x40
	if err := Decrypt(&bytes.Buffer{}, bytes.NewReader(tampered), "correct horse battery staple"); err == nil {
		t.Fatal("tampered backup was accepted")
	}
	truncated := encrypted.Bytes()[:encrypted.Len()-1]
	if err := Decrypt(&bytes.Buffer{}, bytes.NewReader(truncated), "correct horse battery staple"); err == nil {
		t.Fatal("truncated backup was accepted")
	}
}

func TestEncryptRequiresStrongPassphrase(t *testing.T) {
	if err := Encrypt(&bytes.Buffer{}, strings.NewReader("data"), "too-short"); err == nil {
		t.Fatal("short passphrase was accepted")
	}
}

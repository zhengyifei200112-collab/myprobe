package diagnostics

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestExportPreviewFreezesBytesAndBindsSession(t *testing.T) {
	var cache ExportPreviews
	now := time.Now()
	body := []byte("{\n  \"counter\": 3\n}\n")
	original := append([]byte(nil), body...)
	metadata, err := cache.Prepare("synthetic-session-one", body, now)
	if err != nil {
		t.Fatal(err)
	}
	body[0] = 'x'
	if _, _, err = cache.Read("synthetic-session-two", metadata.ID, now); !errors.Is(err, ErrExportPreviewMissing) {
		t.Fatal("another session read the preview")
	}
	got, download, err := cache.Read("synthetic-session-one", metadata.ID, now)
	if err != nil || string(download) != string(original) || got != metadata {
		t.Fatal("frozen preview changed")
	}
	hash := sha256.Sum256(download)
	if metadata.Bytes != len(download) || metadata.SHA256 != hex.EncodeToString(hash[:]) {
		t.Fatal("download does not match advertised bytes/hash")
	}
	download[0] = 'x'
	_, again, err := cache.Read("synthetic-session-one", metadata.ID, now)
	if err != nil || string(again) != string(original) {
		t.Fatal("caller mutation changed cached bytes")
	}
	encoded, _ := json.Marshal(metadata)
	if strings.Contains(string(encoded), "synthetic-session") {
		t.Fatal("metadata disclosed the bound session")
	}
	if _, _, err = cache.Read("synthetic-session-one", metadata.ID, now.Add(ExportPreviewTTL)); !errors.Is(err, ErrExportPreviewMissing) {
		t.Fatal("expired preview was readable")
	}
}

func TestExportPreviewConcurrentCapacityAndExpiry(t *testing.T) {
	var cache ExportPreviews
	now := time.Now()
	var accepted atomic.Int64
	var group sync.WaitGroup
	for i := 0; i < MaximumExportPreviews*2; i++ {
		group.Add(1)
		go func() {
			defer group.Done()
			_, err := cache.Prepare("synthetic-session", []byte(`{"counter":0}`), now)
			if err == nil {
				accepted.Add(1)
			} else if !errors.Is(err, ErrExportPreviewCapacity) {
				t.Error(err)
			}
		}()
	}
	group.Wait()
	if accepted.Load() != MaximumExportPreviews {
		t.Fatalf("capacity: %d", accepted.Load())
	}
	if _, err := cache.Prepare("synthetic-session", []byte(`{}`), now.Add(ExportPreviewTTL)); err != nil {
		t.Fatal("expired previews did not release capacity")
	}
	if _, err := cache.Prepare("synthetic-session", make([]byte, MaximumExportBytes+1), now); !errors.Is(err, ErrExportDocument) {
		t.Fatal("oversized document accepted")
	}
	if _, err := cache.Prepare("synthetic-session", []byte(`{"bad"`), now); !errors.Is(err, ErrExportDocument) {
		t.Fatal("invalid JSON accepted")
	}
}

package diagnostics

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"sync"
	"time"
)

const MaximumExportBytes = 128 << 10
const MaximumExportPreviews = 32
const ExportPreviewTTL = 5 * time.Minute

var ErrExportPreviewMissing = errors.New("diagnostic_preview_missing")
var ErrExportPreviewCapacity = errors.New("diagnostic_preview_capacity")
var ErrExportDocument = errors.New("diagnostic_document_invalid")

type ExportMetadata struct {
	ID        string    `json:"preview_id"`
	FileName  string    `json:"file_name"`
	Bytes     int       `json:"bytes"`
	SHA256    string    `json:"sha256"`
	ExpiresAt time.Time `json:"expires_at"`
}
type exportPreview struct {
	owner    string
	deadline time.Time // Keeps Go's monotonic component while this process is alive.
	metadata ExportMetadata
	body     []byte
}

// ExportPreviews freezes exactly the bytes reviewed by one administrator
// session. It is bounded, expires on restart and never stores credentials.
// Callers supply a whitelist-built document, never a raw runtime/config dump.
type ExportPreviews struct {
	mu       sync.Mutex
	previews map[string]exportPreview
}

func (p *ExportPreviews) expire(now time.Time) {
	for id, item := range p.previews {
		if !now.Before(item.deadline) {
			delete(p.previews, id)
		}
	}
}
func (p *ExportPreviews) Prepare(owner string, body []byte, now time.Time) (ExportMetadata, error) {
	if owner == "" || now.IsZero() || len(body) == 0 || len(body) > MaximumExportBytes || !json.Valid(body) {
		return ExportMetadata{}, ErrExportDocument
	}
	var nonce [16]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		return ExportMetadata{}, ErrExportDocument
	}
	hash := sha256.Sum256(body)
	metadata := ExportMetadata{ID: hex.EncodeToString(nonce[:]), Bytes: len(body), SHA256: hex.EncodeToString(hash[:]), ExpiresAt: now.Add(ExportPreviewTTL).UTC()}
	metadata.FileName = "myprobe-diagnostics-" + now.UTC().Format("20060102T150405Z") + "-" + metadata.ID + ".json"
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.previews == nil {
		p.previews = map[string]exportPreview{}
	}
	p.expire(now)
	if len(p.previews) >= MaximumExportPreviews {
		return ExportMetadata{}, ErrExportPreviewCapacity
	}
	p.previews[metadata.ID] = exportPreview{owner: owner, deadline: now.Add(ExportPreviewTTL), metadata: metadata, body: append([]byte(nil), body...)}
	return metadata, nil
}
func (p *ExportPreviews) Read(owner, id string, now time.Time) (ExportMetadata, []byte, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.expire(now)
	item, ok := p.previews[id]
	if !ok || owner == "" || owner != item.owner {
		return ExportMetadata{}, nil, ErrExportPreviewMissing
	}
	return item.metadata, append([]byte(nil), item.body...), nil
}

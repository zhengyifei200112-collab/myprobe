package store

import (
	"context"
	"errors"
	"os"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/diagnostics"
)

type FileSizeObservation struct {
	Status string `json:"status"`
	Bytes  *int64 `json:"bytes,omitempty"`
}

type DatabaseDiagnostics struct {
	Status         string              `json:"status"`
	ErrorCode      string              `json:"error_code,omitempty"`
	ObservedAt     time.Time           `json:"observed_at"`
	JournalMode    string              `json:"journal_mode,omitempty"`
	SchemaVersion  string              `json:"schema_version,omitempty"`
	AllocatedBytes *int64              `json:"allocated_bytes,omitempty"`
	ReusableBytes  *int64              `json:"reusable_bytes,omitempty"`
	DatabaseFile   FileSizeObservation `json:"database_file"`
	WALFile        FileSizeObservation `json:"wal_file"`
}

func (s *Store) RetentionDiagnostics() diagnostics.JobSnapshot {
	return s.retentionJob.Snapshot()
}

// DatabaseDiagnostics inspects cheap metadata only. It must never checkpoint,
// VACUUM, run an integrity scan, or count samples as a side effect of polling.
func (s *Store) DatabaseDiagnostics(ctx context.Context) DatabaseDiagnostics {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	result := DatabaseDiagnostics{Status: "ok", ObservedAt: time.Now().UTC()}
	if s.path == ":memory:" {
		result.DatabaseFile.Status = "not_applicable"
		result.WALFile.Status = "not_applicable"
	} else {
		result.DatabaseFile = observeFileSize(s.path)
		result.WALFile = observeFileSize(s.path + "-wal")
	}
	var pages, pageSize, freePages int64
	err := s.db.QueryRowContext(ctx, `SELECT
		(SELECT page_count FROM pragma_page_count),
		(SELECT page_size FROM pragma_page_size),
		(SELECT freelist_count FROM pragma_freelist_count),
		(SELECT journal_mode FROM pragma_journal_mode),
		(SELECT COALESCE(MAX(version),'') FROM schema_migrations)`).Scan(
		&pages, &pageSize, &freePages, &result.JournalMode, &result.SchemaVersion)
	if err != nil {
		result.Status, result.ErrorCode = "unavailable", "database_query_failed"
		result.JournalMode, result.SchemaVersion = "", ""
		return result
	}
	allocated, reusable := pages*pageSize, freePages*pageSize
	result.AllocatedBytes, result.ReusableBytes = &allocated, &reusable
	return result
}

func observeFileSize(path string) FileSizeObservation {
	info, err := os.Stat(path)
	return fileSizeObservation(info, err)
}

func fileSizeObservation(info os.FileInfo, err error) FileSizeObservation {
	if errors.Is(err, os.ErrNotExist) {
		zero := int64(0)
		return FileSizeObservation{Status: "absent", Bytes: &zero}
	}
	if err != nil || info == nil || !info.Mode().IsRegular() {
		return FileSizeObservation{Status: "unavailable"}
	}
	size := info.Size()
	return FileSizeObservation{Status: "ok", Bytes: &size}
}

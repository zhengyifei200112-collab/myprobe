package store

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestDatabaseDiagnosticsDistinguishesMemoryAndFiles(t *testing.T) {
	for _, inMemory := range []bool{true, false} {
		name := "file"
		if inMemory {
			name = "memory"
		}
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "private-database-path.db")
			if inMemory {
				path = ":memory:"
			}
			database, err := Open(context.Background(), path)
			if err != nil {
				t.Fatal(err)
			}
			defer database.Close()
			result := database.DatabaseDiagnostics(context.Background())
			if result.Status != "ok" || result.AllocatedBytes == nil || *result.AllocatedBytes <= 0 || result.ReusableBytes == nil || result.SchemaVersion == "" {
				t.Fatalf("database evidence: %+v", result)
			}
			if inMemory {
				if result.DatabaseFile.Status != "not_applicable" || result.DatabaseFile.Bytes != nil || result.WALFile.Bytes != nil || result.JournalMode != "memory" {
					t.Fatalf("invented memory file size: %+v", result)
				}
			} else {
				if result.DatabaseFile.Status != "ok" || result.WALFile.Status != "ok" || result.JournalMode != "wal" {
					t.Fatalf("file evidence: %+v", result)
				}
			}
			encoded, _ := json.Marshal(result)
			if strings.Contains(string(encoded), "private-database-path") {
				t.Fatalf("diagnostics disclosed filesystem path: %s", encoded)
			}
			cancelled, cancel := context.WithCancel(context.Background())
			cancel()
			failed := database.DatabaseDiagnostics(cancelled)
			if failed.Status != "unavailable" || failed.AllocatedBytes != nil || failed.ErrorCode != "database_query_failed" {
				t.Fatalf("cancelled read invented zero usage: %+v", failed)
			}
		})
	}
}

func TestFileDiagnosticsDoNotTurnPermissionFailureIntoZero(t *testing.T) {
	missing := fileSizeObservation(nil, os.ErrNotExist)
	if missing.Status != "absent" || missing.Bytes == nil || *missing.Bytes != 0 {
		t.Fatalf("missing file: %+v", missing)
	}
	denied := fileSizeObservation(nil, &os.PathError{Op: "stat", Path: "private-path", Err: os.ErrPermission})
	if denied.Status != "unavailable" || denied.Bytes != nil {
		t.Fatalf("permission failure: %+v", denied)
	}
}

func TestRetentionDiagnosticsObserveActualOutcome(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if database.RetentionDiagnostics().State != "never_run" {
		t.Fatal("new store claimed a retention run")
	}
	if err := database.ApplyRetention(context.Background(), time.Now().UTC(), DefaultRetentionPolicy()); err != nil {
		t.Fatal(err)
	}
	success := database.RetentionDiagnostics()
	if success.State != "success" || success.CompletedRuns != 1 || success.LastSuccessAt == nil {
		t.Fatalf("successful retention: %+v", success)
	}
	if err := database.ApplyRetention(context.Background(), time.Now().UTC(), RetentionPolicy{}); err == nil {
		t.Fatal("invalid policy succeeded")
	}
	failed := database.RetentionDiagnostics()
	if failed.State != "failed" || failed.FailedRuns != 1 || failed.LastSuccessAt == nil || !failed.LastSuccessAt.Equal(*success.LastSuccessAt) {
		t.Fatalf("failed retention lost prior success: %+v", failed)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if err := database.ApplyRetention(cancelled, time.Now().UTC(), DefaultRetentionPolicy()); err == nil {
		t.Fatal("cancelled retention succeeded")
	}
	if result := database.RetentionDiagnostics(); result.State != "cancelled" || result.CancelledRuns != 1 {
		t.Fatalf("cancelled retention: %+v", result)
	}
}

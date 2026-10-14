package store

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"
)

func TestServiceAlertCommittedReplayDoesNotWaitForManagementWriter(t *testing.T) {
	s, bundle, mapping, _, _ := serviceTransferFixture(t)
	ctx := context.Background()
	preview, err := s.PreviewServiceAlertImport(ctx, bundle, mapping)
	if err != nil {
		t.Fatal(err)
	}
	first, err := s.ApplyServiceAlertImport(ctx, "synthetic-read-replay", bundle, mapping, preview.Digest)
	if err != nil {
		t.Fatal(err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `UPDATE alert_policy_writer SET version=version WHERE id=1`); err != nil {
		t.Fatal(err)
	}
	replayCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
	defer cancel()
	result, err := s.ApplyServiceAlertImport(replayCtx, "synthetic-read-replay", bundle, mapping, preview.Digest)
	if err != nil || !result.Replayed || !reflect.DeepEqual(first.ServiceIDs, result.ServiceIDs) || first.Created != result.Created || first.Updated != result.Updated {
		t.Fatalf("committed replay waited for unrelated writer: %v", err)
	}
	conflictCtx, stop := context.WithTimeout(ctx, 100*time.Millisecond)
	defer stop()
	changed := bundle
	changed.Alerts = append([]PortableServiceAlert(nil), bundle.Alerts...)
	changed.Alerts[0].Settings.CooldownSeconds++
	if _, err := s.ApplyServiceAlertImport(conflictCtx, "synthetic-read-replay", changed, mapping, preview.Digest); !errors.Is(err, ErrServiceAlertImportRequestConflict) {
		t.Fatalf("existing key did not reject changed content while writer busy: %v", err)
	}
}

func TestServiceAlertReplayDoesNotWriteOrRefreshOriginalReceipt(t *testing.T) {
	s, bundle, mapping, _, _ := serviceTransferFixture(t)
	ctx := context.Background()
	preview, err := s.PreviewServiceAlertImport(ctx, bundle, mapping)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ApplyServiceAlertImport(ctx, "immutable-read", bundle, mapping, preview.Digest); err != nil {
		t.Fatal(err)
	}
	var beforeDigest, beforeJSON, beforeAt string
	if err := s.db.QueryRow(`SELECT request_digest,result_json,created_at FROM service_alert_imports WHERE request_id='immutable-read'`).Scan(&beforeDigest, &beforeJSON, &beforeAt); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER synthetic_no_management_write BEFORE UPDATE ON alert_policy_writer BEGIN SELECT RAISE(ABORT,'unexpected management write'); END`); err != nil {
		t.Fatal(err)
	}
	result, err := s.ApplyServiceAlertImport(ctx, "immutable-read", bundle, mapping, preview.Digest)
	if err != nil || !result.Replayed {
		t.Fatal("committed replay attempted a management write")
	}
	var afterDigest, afterJSON, afterAt string
	if err := s.db.QueryRow(`SELECT request_digest,result_json,created_at FROM service_alert_imports WHERE request_id='immutable-read'`).Scan(&afterDigest, &afterJSON, &afterAt); err != nil {
		t.Fatal(err)
	}
	if beforeDigest != afterDigest || beforeJSON != afterJSON || beforeAt != afterAt {
		t.Fatal("replay refreshed or rewrote original evidence")
	}
}

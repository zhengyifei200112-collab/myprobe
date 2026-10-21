package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/alertpolicy"
)

// Receipt copies fill history, not simulated new API requests or active work.
// The boundary operations and replay/restoration use the production Store APIs.
func fillImportReceiptHistory(t *testing.T, s *Store, table, original string, count int) {
	t.Helper()
	result := "policy_ids_json"
	if table == "service_alert_imports" {
		result = "result_json"
	} else if table != "alert_policy_imports" {
		t.Fatal("unsupported test receipt table")
	}
	query := fmt.Sprintf(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x<?)
		INSERT INTO %s(request_id,request_digest,%s,created_at)
		SELECT 'capacity-history-'||x,request_digest,%s,'2000-01-01T00:00:00Z'
		FROM n CROSS JOIN %s WHERE request_id=?`, table, result, result, table)
	if _, err := s.db.ExecContext(context.Background(), query, count, original); err != nil {
		t.Fatal(err)
	}
}

func TestPolicyImportFullLedgerBoundaryAndStagedRestore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	live := filepath.Join(dir, "live.db")
	s, err := Open(ctx, live)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	channel, err := s.CreateNotificationChannel(ctx, "capacity fixture", ChannelKindWebhook, "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	policy := AlertPolicy{Policy: alertpolicy.Policy{Key: "cpu", Scope: alertpolicy.Scope{Kind: "all"}}, Name: "capacity fixture", ChannelID: channel.ID, Kind: "cpu", Config: json.RawMessage(`{"threshold_percent":90}`), CooldownSeconds: 900}
	digest := strings.Repeat("a", 64)
	original, err := s.ApplyPolicyImport(ctx, "original", digest, []AlertPolicy{policy})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteAlertPolicy(ctx, original.PolicyIDs[0], 1); err != nil {
		t.Fatal(err)
	}
	fillImportReceiptHistory(t, s, "alert_policy_imports", "original", 9998)
	if n := rowCount(t, s, "alert_policy_imports"); n != 9999 {
		t.Fatalf("pre-boundary receipts: %d", n)
	}
	last, err := s.ApplyPolicyImport(ctx, "last-slot", digest, []AlertPolicy{policy})
	if err != nil || last.Replayed || len(last.PolicyIDs) != 1 {
		t.Fatalf("10000th receipt: %+v %v", last, err)
	}
	check := func(db *Store) {
		t.Helper()
		if n := rowCount(t, db, "alert_policy_imports"); n != 10000 {
			t.Fatalf("full ledger receipts: %d", n)
		}
		if _, err := db.ApplyPolicyImport(ctx, "over-limit", digest, []AlertPolicy{policy}); !errors.Is(err, ErrPolicyImportLimit) {
			t.Fatalf("10001st receipt accepted or wrong error: %v", err)
		}
		if _, err := db.ApplyPolicyImport(ctx, "original", strings.Repeat("b", 64), []AlertPolicy{policy}); !errors.Is(err, ErrPolicyImportConflict) {
			t.Fatalf("full-ledger request conflict: %v", err)
		}
		for id, want := range map[string]PolicyImportResult{"original": original, "last-slot": last} {
			got, err := db.ApplyPolicyImport(ctx, id, digest, []AlertPolicy{policy})
			if err != nil || !got.Replayed || !reflect.DeepEqual(got.PolicyIDs, want.PolicyIDs) {
				t.Fatalf("full-ledger replay %s: %+v %v", id, got, err)
			}
		}
		policies, err := db.ListAlertPolicies(ctx)
		if err != nil || len(policies) != 1 || policies[0].ID != last.PolicyIDs[0] || rowCount(t, db, "alert_policy_imports") != 10000 {
			t.Fatalf("rejected/replayed work changed definitions or receipts: %v", err)
		}
	}
	check(s)
	snapshot := filepath.Join(dir, "snapshot.db")
	if err := s.ConsistentBackup(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	// Demonstrate restoration of actual state, not opening an unchanged live DB.
	if err := s.DeleteAlertPolicy(ctx, last.PolicyIDs[0], 1); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := StageDatabaseRestore(ctx, live, snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyPendingRestore(ctx, live, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(ctx, live)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	check(restored)
}

func TestServiceImportFullLedgerBoundaryAndSnapshotRestore(t *testing.T) {
	ctx := context.Background()
	s, bundle, mapping, services, channel := serviceTransferFixture(t)
	preview, err := s.PreviewServiceAlertImport(ctx, bundle, mapping)
	if err != nil {
		t.Fatal(err)
	}
	original, err := s.ApplyServiceAlertImport(ctx, "original", bundle, mapping, preview.Digest)
	if err != nil {
		t.Fatal(err)
	}
	fillImportReceiptHistory(t, s, "service_alert_imports", "original", 9998)
	if n := rowCount(t, s, "service_alert_imports"); n != 9999 {
		t.Fatalf("pre-boundary service receipts: %d", n)
	}
	fresh, err := s.PreviewServiceAlertImport(ctx, bundle, mapping)
	if err != nil {
		t.Fatal(err)
	}
	last, err := s.ApplyServiceAlertImport(ctx, "last-slot", bundle, mapping, fresh.Digest)
	if err != nil || last.Replayed || len(last.ServiceIDs) != len(services) {
		t.Fatalf("10000th service receipt: %+v %v", last, err)
	}
	for _, service := range services {
		if err := s.DeleteHTTPService(ctx, service.ID, service.Revision); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.DeleteNotificationChannel(ctx, channel.ID); err != nil {
		t.Fatal(err)
	}
	check := func(db *Store) {
		t.Helper()
		if n := rowCount(t, db, "service_alert_imports"); n != 10000 {
			t.Fatalf("full service receipts: %d", n)
		}
		if _, err := db.ApplyServiceAlertImport(ctx, "over-limit", bundle, mapping, fresh.Digest); !errors.Is(err, ErrServiceAlertImportLimit) {
			t.Fatalf("10001st service receipt: %v", err)
		}
		changed := bundle
		changed.Alerts = append([]PortableServiceAlert(nil), bundle.Alerts...)
		changed.Alerts[0].Settings.DurationSeconds++
		if _, err := db.ApplyServiceAlertImport(ctx, "original", changed, mapping, preview.Digest); !errors.Is(err, ErrServiceAlertImportRequestConflict) {
			t.Fatalf("full service request conflict: %v", err)
		}
		for _, request := range []struct {
			id, digest string
			want       ServiceAlertImportResult
		}{{"original", preview.Digest, original}, {"last-slot", fresh.Digest, last}} {
			got, err := db.ApplyServiceAlertImport(ctx, request.id, bundle, mapping, request.digest)
			request.want.Replayed = true
			if err != nil || !reflect.DeepEqual(got, request.want) {
				t.Fatalf("full service replay %s: %+v %v", request.id, got, err)
			}
		}
		if rowCount(t, db, "http_services") != 0 || rowCount(t, db, "http_service_alerts") != 0 || rowCount(t, db, "service_alert_imports") != 10000 {
			t.Fatal("full service replay recreated deleted targets or changed history")
		}
	}
	check(s)
	snapshot := filepath.Join(t.TempDir(), "full-ledger-snapshot.db")
	if err := s.ConsistentBackup(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(ctx, snapshot)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	check(restored)
}

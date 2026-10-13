package store

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"
)

func TestIncidentLinkCapturedAcrossRetryRestoreAndLifecycle(t *testing.T) {
	s, rule, node := incidentFixture(t, `{"threshold_percent":90,"repeat_seconds":300}`)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Millisecond)
	i, err := s.ObserveAlert(ctx, rule, node, AlertObservation{At: at, Known: true, Active: true, NotificationOrigin: "https://old.example.test/"})
	if err != nil {
		t.Fatal(err)
	}
	job, err := s.ClaimDelivery(ctx, at, time.Minute)
	if err != nil || job == nil {
		t.Fatalf("claim: %v %v", job, err)
	}
	var payload DeliveryNotification
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.IncidentPath != "https://old.example.test/admin?incident="+i.ID {
		t.Fatal("missing captured path")
	}
	original := string(job.Payload)
	if err := s.CompleteDelivery(ctx, job.ID, job.LeaseToken, at, DeliveryOutcome{Ambiguous: true, ErrorClass: "transport_error", RetryAt: at.Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ObserveAlert(ctx, rule, node, AlertObservation{At: at.Add(time.Millisecond), Known: true, Active: true, NotificationOrigin: "https://new.example.test"}); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(t.TempDir(), "snapshot.db")
	if err := s.ConsistentBackup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	path := s.DatabasePath()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := StageDatabaseRestore(ctx, path, backup); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyPendingRestore(ctx, path, at); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	retry, err := restored.ClaimDelivery(ctx, at.Add(time.Second), time.Minute)
	if err != nil || retry == nil || string(retry.Payload) != original || retry.IdempotencyKey != job.IdempotencyKey {
		t.Fatal("restore/retry changed payload identity")
	}
	if err := restored.CompleteDelivery(ctx, retry.ID, retry.LeaseToken, at.Add(time.Second), DeliveryOutcome{Delivered: true}); err != nil {
		t.Fatal(err)
	}
	for index, active := range []bool{true, false} {
		when := at.Add(time.Duration(index+1) * time.Hour)
		current, err := restored.ObserveAlert(ctx, rule, node, AlertObservation{At: when, Known: true, Active: active, NotificationOrigin: "https://new.example.test"})
		if err != nil || current.ID != i.ID {
			t.Fatal("lost original incident")
		}
		next, err := restored.ClaimDelivery(ctx, when, time.Minute)
		if err != nil || next == nil {
			t.Fatalf("lifecycle claim: %v %v", next, err)
		}
		if err := json.Unmarshal(next.Payload, &payload); err != nil {
			t.Fatal(err)
		}
		if payload.IncidentPath != "https://new.example.test/admin?incident="+i.ID {
			t.Fatal("new job did not capture current origin")
		}
		if active && payload.State != "firing" || !active && payload.State != "resolved" {
			t.Fatal("wrong lifecycle notification")
		}
		if err := restored.CompleteDelivery(ctx, next.ID, next.LeaseToken, when, DeliveryOutcome{Delivered: true}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestIncidentLinkInputAndLegacyPayload(t *testing.T) {
	s, rule, node := incidentFixture(t, `{"threshold_percent":90}`)
	ctx := context.Background()
	at := time.Now().UTC()
	if _, err := s.ObserveAlert(ctx, rule, node, AlertObservation{At: at, Known: true, Active: true, NotificationOrigin: "https://user:private@example.test"}); err == nil {
		t.Fatal("invalid origin accepted")
	}
	if rowCount(t, s, "alert_incidents") != 0 || rowCount(t, s, "notification_deliveries") != 0 {
		t.Fatal("invalid origin partially committed")
	}
	i := observe(t, s, rule, node, at, true, true)
	jobs, err := s.ListIncidentDeliveries(ctx, i.ID, 0, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatal(err)
	}
	var p DeliveryNotification
	if err := json.Unmarshal(jobs[0].Payload, &p); err != nil {
		t.Fatal(err)
	}
	if p.IncidentPath != "/admin?incident="+i.ID {
		t.Fatal("relative fallback missing")
	}
	p.IncidentPath = ""
	old, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE notification_deliveries SET payload_json=? WHERE id=?`, string(old), jobs[0].ID); err != nil {
		t.Fatal(err)
	}
	claimed, err := s.ClaimDelivery(ctx, at, time.Minute)
	if err != nil || claimed == nil || string(claimed.Payload) != string(old) {
		t.Fatal("legacy job was rewritten")
	}
}

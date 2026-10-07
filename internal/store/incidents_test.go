package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func incidentFixture(t *testing.T, config string) (*Store, AlertRule, Node) {
	t.Helper()
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "incidents.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "incident fixture"})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := s.CreateNotificationChannel(ctx, "test channel", ChannelKindWebhook, "encrypted-placeholder")
	if err != nil {
		t.Fatal(err)
	}
	rule, err := s.CreateAlertRule(ctx, node.ID, channel.ID, "cpu", json.RawMessage(config), 300)
	if err != nil {
		t.Fatal(err)
	}
	return s, rule, node
}
func observe(t *testing.T, s *Store, rule AlertRule, node Node, at time.Time, known, active bool) *Incident {
	t.Helper()
	i, err := s.ObserveAlert(context.Background(), rule, node, AlertObservation{At: at, Known: known, Active: active, Message: "synthetic condition"})
	if err != nil {
		t.Fatal(err)
	}
	return i
}
func rowCount(t *testing.T, s *Store, table string) int {
	t.Helper()
	var count int
	if err := s.db.QueryRow("SELECT count(*) FROM " + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	return count
}
func TestIncidentWindowsUnknownAndRecovery(t *testing.T) {
	s, rule, node := incidentFixture(t, `{"threshold_percent":90,"duration_seconds":30,"recovery_seconds":20}`)
	start := time.Now().UTC().Truncate(time.Second)
	i := observe(t, s, rule, node, start, true, true)
	if i.State != "pending" || i.FiredAt != nil {
		t.Fatal("fault did not start pending")
	}
	observe(t, s, rule, node, start.Add(29*time.Second), false, false)
	i = observe(t, s, rule, node, start.Add(31*time.Second), true, true)
	if i.State != "pending" {
		t.Fatal("unknown time counted toward trigger")
	}
	i = observe(t, s, rule, node, start.Add(61*time.Second), true, true)
	if i.State != "firing" || i.FiredAt == nil || !i.FiredAt.Equal(start.Add(61*time.Second)) || !i.StartedAt.Equal(start) {
		t.Fatal("incorrect trigger timestamps")
	}
	if rowCount(t, s, "notification_deliveries") != 1 {
		t.Fatal("trigger did not queue one job")
	}
	ctx := context.Background()
	job, err := s.ClaimDelivery(ctx, start.Add(62*time.Second), time.Minute)
	if err != nil || job == nil {
		t.Fatalf("claim=%v %v", job, err)
	}
	if err := s.CompleteDelivery(ctx, job.ID, job.LeaseToken, start.Add(63*time.Second), DeliveryOutcome{Delivered: true}); err != nil {
		t.Fatal(err)
	}
	observe(t, s, rule, node, start.Add(64*time.Second), true, false)
	i = observe(t, s, rule, node, start.Add(83*time.Second), false, false)
	if i.State != "firing" || !i.ObservationStale || i.RecoverySince != nil {
		t.Fatal("unknown data resolved a fault or kept recovery window")
	}
	observe(t, s, rule, node, start.Add(84*time.Second), true, false)
	i = observe(t, s, rule, node, start.Add(104*time.Second), true, false)
	if i.State != "resolved" || i.ResolutionReason != "recovered" || i.ObservationStale {
		t.Fatalf("not recovered: %+v", i)
	}
	if rowCount(t, s, "notification_deliveries") != 2 {
		t.Fatal("recovery did not queue after actual attempt")
	}
	// A delayed old observation cannot reopen a resolved incident.
	old := observe(t, s, rule, node, start.Add(90*time.Second), true, true)
	if old == nil || old.ID != i.ID || old.State != "resolved" {
		t.Fatal("old observation reopened fault")
	}
}
func TestConcurrentIncidentObservationAndAtomicQueue(t *testing.T) {
	s, rule, node := incidentFixture(t, `{"threshold_percent":90}`)
	at := time.Now().UTC()
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := s.ObserveAlert(context.Background(), rule, node, AlertObservation{At: at, Known: true, Active: true, Message: "high CPU"}); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if rowCount(t, s, "alert_incidents") != 1 || rowCount(t, s, "notification_deliveries") != 1 {
		t.Fatal("duplicate event/notification")
	}
	observe(t, s, rule, node, at.Add(time.Hour), true, true)
	if rowCount(t, s, "notification_deliveries") != 1 {
		t.Fatal("queued a reminder while delivery still pending")
	}
}
func TestIncidentQueueFailureRollsBackObservation(t *testing.T) {
	s, rule, node := incidentFixture(t, `{"threshold_percent":90}`)
	if _, err := s.db.Exec(`CREATE TRIGGER fail_delivery BEFORE INSERT ON notification_deliveries BEGIN SELECT RAISE(FAIL,'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	_, err := s.ObserveAlert(context.Background(), rule, node, AlertObservation{At: time.Now().UTC(), Known: true, Active: true})
	if err == nil {
		t.Fatal("expected queue failure")
	}
	if rowCount(t, s, "alert_incidents") != 0 || rowCount(t, s, "notification_deliveries") != 0 {
		t.Fatal("state committed without its outbox")
	}
}
func TestIncidentChannelAndRuleLifecycle(t *testing.T) {
	s, rule, node := incidentFixture(t, `{"threshold_percent":90}`)
	ctx := context.Background()
	at := time.Now().UTC()
	if _, err := s.db.Exec("UPDATE notification_channels SET enabled=0 WHERE id=?", rule.ChannelID); err != nil {
		t.Fatal(err)
	}
	i := observe(t, s, rule, node, at, true, true)
	if i.State != "firing" || rowCount(t, s, "notification_deliveries") != 0 {
		t.Fatal("disabled channel suppressed fault tracking")
	}
	if _, err := s.db.Exec("UPDATE notification_channels SET enabled=1 WHERE id=?", rule.ChannelID); err != nil {
		t.Fatal(err)
	}
	observe(t, s, rule, node, at.Add(time.Second), true, true)
	if rowCount(t, s, "notification_deliveries") != 1 {
		t.Fatal("reenabled channel did not enqueue current fault")
	}
	// Recovery before any attempt cancels queued firing and sends no standalone recovery.
	i = observe(t, s, rule, node, at.Add(2*time.Second), true, false)
	if i.State != "resolved" || rowCount(t, s, "notification_deliveries") != 1 {
		t.Fatal("unattempted firing produced recovery notification")
	}
	var status string
	if err := s.db.QueryRow("SELECT status FROM notification_deliveries").Scan(&status); err != nil || status != "canceled" {
		t.Fatal("old firing not canceled")
	}
	i = observe(t, s, rule, node, at.Add(3*time.Second), true, true)
	if _, err := s.db.Exec("UPDATE alert_rules SET enabled=0 WHERE id=?", rule.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileIncidents(ctx, at.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	saved, err := s.Incident(ctx, i.ID)
	if err != nil || saved.State != "resolved" || saved.ResolutionReason != "rule_disabled" {
		t.Fatal("disabled rule did not close with management reason")
	}
	if _, err := s.ObserveAlert(ctx, rule, node, AlertObservation{At: at.Add(5 * time.Second), Known: true, Active: true}); !errors.Is(err, ErrObservationObsolete) {
		t.Fatal("stale enabled rule accepted")
	}
}
func TestIncidentChangedAndDeletedRuleEndsWithoutRecovery(t *testing.T) {
	for _, change := range []string{"change", "delete"} {
		t.Run(change, func(t *testing.T) {
			s, rule, node := incidentFixture(t, `{"threshold_percent":90}`)
			ctx := context.Background()
			at := time.Now().UTC()
			i := observe(t, s, rule, node, at, true, true)
			reason := "rule_changed"
			if change == "delete" {
				if err := s.DeleteAlertRule(ctx, rule.ID); err != nil {
					t.Fatal(err)
				}
				reason = "rule_deleted"
			} else {
				if _, err := s.db.Exec("UPDATE alert_rules SET config_json=? WHERE id=?", `{"threshold_percent":95}`, rule.ID); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.ReconcileIncidents(ctx, at.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			saved, err := s.Incident(ctx, i.ID)
			if err != nil || saved.ResolutionReason != reason || saved.State != "resolved" {
				t.Fatalf("%+v %v", saved, err)
			}
			if rowCount(t, s, "notification_deliveries") != 1 {
				t.Fatal("management closure queued recovery")
			}
		})
	}
}

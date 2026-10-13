package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
)

func serviceAlertFixture(t *testing.T) (*Store, HTTPService, NotificationChannel, Node, ServiceAlertSettings) {
	t.Helper()
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "service-alert.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "observer"})
	if err != nil {
		t.Fatal(err)
	}
	service, err := s.SaveHTTPService(ctx, HTTPService{Name: "private service alias", Enabled: true, IntervalSeconds: 30, NodeIDs: []string{node.ID}, Spec: httpcheck.Spec{URL: "http://example.test/private?opaque=not-a-secret", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 1000, MaxBodyBytes: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := s.CreateNotificationChannel(ctx, "channel", "webhook", "synthetic config")
	if err != nil {
		t.Fatal(err)
	}
	settings, err := s.SaveServiceAlertSettings(ctx, ServiceAlertSettings{ServiceID: service.ID, ServiceRevision: service.Revision, ChannelID: channel.ID, Enabled: true, CooldownSeconds: 300})
	if err != nil {
		t.Fatal(err)
	}
	return s, service, channel, node, settings
}

func TestServiceAlertSettingsAndOutboxSurviveFullRestore(t *testing.T) {
	s, service, channel, node, settings := serviceAlertFixture(t)
	ctx := context.Background()
	at := time.Now().UTC().Add(time.Second)
	if _, err := s.PrepareAlertPolicyRules(ctx, at); err != nil {
		t.Fatal(err)
	}
	rules, err := s.ListAlertRules(ctx)
	if err != nil || len(rules) != 1 {
		t.Fatal(err)
	}
	i, err := s.ObserveAlert(ctx, rules[0], node, AlertObservation{At: at, Known: true, Active: true, Message: "safe fault"})
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.ClaimDelivery(ctx, at, time.Minute)
	if err != nil || d == nil {
		t.Fatal(err)
	}
	if err := s.CheckDeliveryLease(ctx, d.ID, d.LeaseToken, at); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteDelivery(ctx, d.ID, d.LeaseToken, at.Add(time.Millisecond), DeliveryOutcome{Ambiguous: true, ErrorClass: "transport_error", RetryAt: at.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	pauseFixtureChannel(t, s, channel.ID, at, at.Add(time.Hour))
	backup := filepath.Join(t.TempDir(), "full.db")
	if err := s.ConsistentBackup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	settings.Enabled = false
	if _, err := s.SaveServiceAlertSettings(ctx, settings); err != nil {
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
	current, err := restored.ServiceAlertSettings(ctx, service.ID)
	if err != nil || current.Revision != settings.Revision || !current.Enabled || current.ChannelID != channel.ID {
		t.Fatal("service settings restore lost")
	}
	if _, err := restored.PrepareAlertPolicyRules(ctx, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	rules, err = restored.ListAlertRules(ctx)
	if err != nil || len(rules) != 1 || rules[0].ID != i.RuleID || rowCount(t, restored, "alert_policies") != 0 {
		t.Fatal("service rule restore changed identity")
	}
	jobs, err := restored.ListIncidentDeliveries(ctx, i.ID, 0, 25)
	if err != nil || len(jobs) != 1 || jobs[0].ID != d.ID || jobs[0].Status != "pending" || jobs[0].AttemptCount != 1 || string(jobs[0].Payload) != string(d.Payload) || jobs[0].IdempotencyKey != d.IdempotencyKey {
		t.Fatal("service outbox restore lost")
	}
	attempts, err := restored.ListDeliveryAttempts(ctx, d.ID)
	if err != nil || len(attempts) != 1 || attempts[0].Outcome != "unknown" {
		t.Fatal("service receipt evidence lost")
	}
	c, err := restored.NotificationChannel(ctx, channel.ID)
	if err != nil || c.DeliveryPausedUntil == nil || !c.DeliveryPausedUntil.Equal(ceilMilliseconds(at.Add(time.Hour))) {
		t.Fatal("shared wait lost")
	}
}

func TestServiceAlertOwnershipRevisionAndAtomicPreparation(t *testing.T) {
	s, service, channel, node, settings := serviceAlertFixture(t)
	ctx := context.Background()
	at := time.Now().UTC()
	if _, err := s.SaveServiceAlertSettings(ctx, ServiceAlertSettings{ServiceID: service.ID, ServiceRevision: service.Revision, Revision: 0, ChannelID: channel.ID, Enabled: true, CooldownSeconds: 300}); !errors.Is(err, ErrServiceConflict) {
		t.Fatal("stale creation accepted")
	}
	bad := settings
	bad.ServiceRevision++
	if _, err := s.SaveServiceAlertSettings(ctx, bad); !errors.Is(err, ErrServiceConflict) {
		t.Fatal("stale service revision accepted")
	}
	bad = settings
	bad.RepeatSeconds = 1
	if _, err := s.SaveServiceAlertSettings(ctx, bad); !errors.Is(err, ErrInvalidHTTPService) {
		t.Fatal("invalid timing accepted")
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_service_binding BEFORE INSERT ON http_service_rule_bindings BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareAlertPolicyRules(ctx, at); err == nil {
		t.Fatal("forced materialization succeeded")
	}
	if rowCount(t, s, "alert_rules") != 0 || rowCount(t, s, "http_service_rule_bindings") != 0 {
		t.Fatal("partial service rules committed")
	}
	if _, err := s.db.Exec(`DROP TRIGGER fail_service_binding`); err != nil {
		t.Fatal(err)
	}
	if count, err := s.PrepareAlertPolicyRules(ctx, at); err != nil || count != 0 {
		t.Fatal("service rules became host policies")
	}
	rules, err := s.ListAlertRules(ctx)
	if err != nil || len(rules) != 1 || rules[0].Kind != "service_http" || rules[0].NodeID != node.ID {
		t.Fatal("service observer binding missing")
	}
	before := rules[0]
	if _, err := s.PrepareAlertPolicyRules(ctx, at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	rules, err = s.ListAlertRules(ctx)
	if err != nil || len(rules) != 1 || rules[0].ID != before.ID || !rules[0].UpdatedAt.Equal(before.UpdatedAt) || rowCount(t, s, "alert_policies") != 0 {
		t.Fatal("unchanged service synchronization rewrote rules")
	}
	if err := s.DeleteAlertRule(ctx, before.ID); !errors.Is(err, ErrServiceAlertRuleManaged) {
		t.Fatal("service rule directly deleted")
	}
	if _, err := s.UpdateAlertRule(ctx, before.ID, node.ID, channel.ID, "cpu", []byte(`{"threshold_percent":90}`), true, 300); !errors.Is(err, ErrServiceAlertRuleManaged) {
		t.Fatal("service rule directly edited")
	}
	if err := s.DeleteNotificationChannel(ctx, channel.ID); !errors.Is(err, ErrServiceNotificationChannelInUse) {
		t.Fatal("service channel deleted")
	}
	incident, err := s.ObserveAlert(ctx, before, node, AlertObservation{At: at.Add(2 * time.Second), Known: true, Active: true})
	if err != nil || incident.ServiceID != service.ID || incident.ServiceName != service.Name {
		t.Fatal("service subject not retained")
	}
	settings.Enabled = false
	settings, err = s.SaveServiceAlertSettings(ctx, settings)
	if err != nil {
		t.Fatal(err)
	}
	// Definition fencing applies before service materialization has run again.
	if _, err := s.ObserveAlert(ctx, before, node, AlertObservation{At: at.Add(3 * time.Second), Known: true, Active: true}); !errors.Is(err, ErrObservationObsolete) {
		t.Fatal("obsolete service observation accepted")
	}
	if _, err := s.PrepareAlertPolicyRules(ctx, at.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.ReconcileIncidents(ctx, at.Add(4*time.Second)); err != nil {
		t.Fatal(err)
	}
	current, err := s.Incident(ctx, incident.ID)
	if err != nil || current.State != "resolved" || current.ResolutionReason != "rule_disabled" || current.ServiceID != service.ID {
		t.Fatal("service disable lost history")
	}
	if err := s.DeleteHTTPService(ctx, service.ID, service.Revision); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareAlertPolicyRules(ctx, at.Add(5*time.Second)); err != nil || rowCount(t, s, "alert_policies") != 0 {
		t.Fatal("deleted service became host policy")
	}
}

func TestLatestServiceObservationIsPerObserverAndCurrentRevision(t *testing.T) {
	s, service, _, node, _ := serviceAlertFixture(t)
	ctx := context.Background()
	at := time.Now().UTC().Add(time.Second)
	task, err := s.CreateHTTPTask(ctx, service.ID, node.ID, at, at)
	if err != nil {
		t.Fatal(err)
	}
	result := httpcheck.Result{TaskID: task.ID, ServiceID: service.ID, Revision: task.Revision, ScheduledAt: task.ScheduledAt, CompletedAt: at.Add(time.Millisecond), Outcome: "failure", ErrorClass: "status_mismatch", StatusCode: 503, DurationMS: 1}
	if err := s.SaveHTTPResult(ctx, node.ID, result, result.CompletedAt); err != nil {
		t.Fatal(err)
	}
	pending, err := s.CreateHTTPTask(ctx, service.ID, node.ID, at.Add(time.Second), at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	current, _, err := s.LatestServiceAlertObservation(ctx, service.ID, node.ID, service.Revision, at.Add(2*time.Second))
	if err != nil || current == nil || current.TaskID != pending.ID || current.Result != nil {
		t.Fatal("pending newer slot hidden behind old fault")
	}
	before, _, err := s.LatestServiceAlertObservation(ctx, service.ID, node.ID, service.Revision, at.Add(time.Millisecond))
	if err != nil || before == nil || before.TaskID != task.ID || before.Result == nil {
		t.Fatal("future task replaced current observation")
	}
	empty, _, err := s.LatestServiceAlertObservation(ctx, service.ID, "unassigned", service.Revision, at.Add(2*time.Second))
	if err != nil || empty != nil {
		t.Fatal("other observer acquired result")
	}
	service.Name = "changed"
	service, err = s.SaveHTTPService(ctx, service)
	if err != nil {
		t.Fatal(err)
	}
	empty, _, err = s.LatestServiceAlertObservation(ctx, service.ID, node.ID, service.Revision, at.Add(2*time.Second))
	if err != nil || empty != nil {
		t.Fatal("old revision observation reused")
	}
}

func TestServiceAlertChangesFenceAlreadyStartedReceipt(t *testing.T) {
	for _, change := range []string{"service", "settings", "assignment", "delete"} {
		t.Run(change, func(t *testing.T) {
			s, service, channel, node, settings := serviceAlertFixture(t)
			originalName := service.Name
			ctx := context.Background()
			at := time.Now().UTC()
			if _, err := s.PrepareAlertPolicyRules(ctx, at); err != nil {
				t.Fatal(err)
			}
			rules, err := s.ListAlertRules(ctx)
			if err != nil || len(rules) != 1 {
				t.Fatal(err)
			}
			i := observe(t, s, rules[0], node, at, true, true)
			d, err := s.ClaimDelivery(ctx, at, time.Minute)
			if err != nil || d == nil {
				t.Fatal(err)
			}
			if err := s.CheckDeliveryLease(ctx, d.ID, d.LeaseToken, at, channel.DeliveryRevision); err != nil {
				t.Fatal(err)
			}
			switch change {
			case "service":
				service.Name = "new alias"
				_, err = s.SaveHTTPService(ctx, service)
			case "assignment":
				replacement, _, createErr := s.CreateNode(ctx, CreateNodeParams{Name: "replacement"})
				if createErr != nil {
					t.Fatal(createErr)
				}
				service.NodeIDs = []string{replacement.ID}
				_, err = s.SaveHTTPService(ctx, service)
			case "settings":
				settings.CooldownSeconds = 600
				_, err = s.SaveServiceAlertSettings(ctx, settings)
			case "delete":
				err = s.DeleteHTTPService(ctx, service.ID, service.Revision)
			}
			if err != nil {
				t.Fatal(err)
			}
			if err := s.CompleteDelivery(ctx, d.ID, d.LeaseToken, at.Add(time.Second), DeliveryOutcome{Delivered: true, ChannelRevision: channel.DeliveryRevision}); err != nil {
				t.Fatal(err)
			}
			job, err := s.Delivery(ctx, d.ID)
			if err != nil || job.Status != "canceled" || job.ErrorClass != "configuration_changed" {
				t.Fatal("obsolete service receipt retained authority", err)
			}
			attempts, err := s.ListDeliveryAttempts(ctx, d.ID)
			if err != nil || len(attempts) != 1 || attempts[0].Outcome != "delivered" {
				t.Fatal("actual receipt lost", err)
			}
			if _, err := s.PrepareAlertPolicyRules(ctx, at.Add(2*time.Second)); err != nil {
				t.Fatal(err)
			}
			if err := s.ReconcileIncidents(ctx, at.Add(2*time.Second)); err != nil {
				t.Fatal(err)
			}
			ended, err := s.Incident(ctx, i.ID)
			if err != nil || ended.State != "resolved" || ended.ResolutionReason == "recovered" || ended.ServiceName != originalName || rowCount(t, s, "notification_deliveries") != 1 || rowCount(t, s, "alert_policies") != 0 {
				t.Fatal("management change manufactured recovery or lost history", err)
			}
		})
	}
}

func TestServiceAlertMaintenanceAndSilenceReuseSuppression(t *testing.T) {
	s, _, _, node, _ := serviceAlertFixture(t)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Second)
	if _, err := s.PrepareAlertPolicyRules(ctx, at); err != nil {
		t.Fatal(err)
	}
	rules, err := s.ListAlertRules(ctx)
	if err != nil || len(rules) != 1 {
		t.Fatal(err)
	}
	actor, err := s.CreateUser(ctx, "fixture", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	i := observe(t, s, rules[0], node, at, true, true)
	if _, err := s.CreateMaintenanceWindow(ctx, MaintenanceWindow{Reason: "work", Timezone: "UTC", CreatedBy: actor.ID, NodeIDs: []string{node.ID}, StartsAt: at.Add(time.Second), EndsAt: at.Add(90 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateIncidentSilence(ctx, i.ID, "service-silence", actor.ID, CreateIncidentSilenceParams{DurationSeconds: 60}, at.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	jobs, err := s.ListIncidentDeliveries(ctx, i.ID, 0, 25)
	if err != nil || len(jobs) != 1 || jobs[0].Status != "canceled" || jobs[0].ErrorClass != "maintenance_and_silence" {
		t.Fatal("service suppression not reused", err)
	}
	observe(t, s, rules[0], node, at.Add(62*time.Second), true, true)
	observe(t, s, rules[0], node, at.Add(90*time.Second), false, false)
	if d, err := s.ClaimDelivery(ctx, at.Add(90*time.Second), time.Minute); err != nil || d != nil {
		t.Fatal("expiry replayed stale service fault", err)
	}
	observe(t, s, rules[0], node, at.Add(91*time.Second), true, true)
	d, err := s.ClaimDelivery(ctx, at.Add(91*time.Second), time.Minute)
	if err != nil || d == nil || d.ID == jobs[0].ID {
		t.Fatal("fresh service summary missing", err)
	}
	if err := s.CompleteDelivery(ctx, d.ID, d.LeaseToken, at.Add(92*time.Second), DeliveryOutcome{Delivered: true}); err != nil {
		t.Fatal(err)
	}
	observe(t, s, rules[0], node, at.Add(93*time.Second), true, true)
	if rowCount(t, s, "notification_deliveries") != 2 {
		t.Fatal("service summary duplicated")
	}
}

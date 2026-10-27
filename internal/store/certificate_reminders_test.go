package store

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func certificateFixture(t *testing.T) (*Store, HTTPService, NotificationChannel, []Node, ServiceAlertSettings) {
	t.Helper()
	s, service, channel, node, settings := serviceAlertFixture(t)
	other, _, err := s.CreateNode(context.Background(), CreateNodeParams{Name: "second observer"})
	if err != nil {
		t.Fatal(err)
	}
	service.Spec.URL = "https://example.test/private?opaque=private-marker"
	service.NodeIDs = []string{node.ID, other.ID}
	service, err = s.SaveHTTPService(context.Background(), service)
	if err != nil {
		t.Fatal(err)
	}
	settings.ServiceRevision = service.Revision
	settings.Enabled = false
	settings.CertificateRemindersEnabled = true
	settings, err = s.SaveServiceAlertSettings(context.Background(), settings)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range service.NodeIDs {
		if err := s.SaveAgentMetadata(context.Background(), id, protocol.Hello{AgentVersion: "fixture", Hostname: "fixture", OS: "fixture", Capabilities: []string{"http_probe.v1"}}); err != nil {
			t.Fatal(err)
		}
	}
	return s, service, channel, []Node{node, other}, settings
}

func certificateReport(t *testing.T, s *Store, service HTTPService, node Node, at time.Time, certificates []httpcheck.Certificate, outcome, class string) {
	t.Helper()
	ctx := context.Background()
	if _, err := s.db.Exec(`UPDATE nodes SET last_seen_at=? WHERE id=?`, formatTime(at), node.ID); err != nil {
		t.Fatal(err)
	}
	task, err := s.CreateHTTPTask(ctx, service.ID, node.ID, at, at)
	if err != nil {
		t.Fatal(err)
	}
	r := httpcheck.Result{TaskID: task.ID, ServiceID: service.ID, Revision: task.Revision, ScheduledAt: at, CompletedAt: at.Add(time.Millisecond), Outcome: outcome, ErrorClass: class, DurationMS: 1, Certificates: certificates}
	if outcome == "success" {
		r.StatusCode = 200
	}
	if err := s.SaveHTTPResult(ctx, node.ID, r, r.CompletedAt); err != nil {
		t.Fatal(err)
	}
}

func prepareCertificatePool(t *testing.T, s *Store, service HTTPService, settings ServiceAlertSettings, nodes []Node, at time.Time) *CertificatePool {
	t.Helper()
	available := map[string]bool{}
	for _, node := range nodes {
		available[node.ID] = true
	}
	pool, err := s.PrepareCertificateReminders(context.Background(), CertificateAlertTarget{service.ID, service.Revision, settings.Revision}, available, at)
	if err != nil {
		t.Fatal(err)
	}
	return pool
}

func recordCertificateEvents(t *testing.T, s *Store, nodes []Node, at time.Time) {
	t.Helper()
	ctx := context.Background()
	if err := s.ReconcileIncidents(ctx, at); err != nil {
		t.Fatal(err)
	}
	rules, err := s.ListAlertRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for _, rule := range rules {
		if rule.Kind != "service_certificate" || !rule.Enabled {
			continue
		}
		for _, node := range nodes {
			if node.ID == rule.NodeID {
				if _, err := s.ObserveAlert(ctx, rule, node, AlertObservation{At: at, Known: true, Active: true, Message: "safe certificate expiry reminder", NotificationOrigin: "https://probe.example.test"}); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

func TestCertificateReminderLevelsDeduplicateAcrossObserversAndFullRestore(t *testing.T) {
	s, service, _, nodes, settings := certificateFixture(t)
	ctx := context.Background()
	at := time.Now().UTC().Add(time.Second)
	c := httpcheck.Certificate{SHA256: strings.Repeat("ab", 32), NotBefore: at.Add(-time.Hour), NotAfter: at.Add(14 * 24 * time.Hour), Verified: true}
	for _, node := range nodes {
		certificateReport(t, s, service, node, at, []httpcheck.Certificate{c}, "success", "")
	}
	prepareCertificatePool(t, s, service, settings, nodes, at.Add(time.Millisecond))
	recordCertificateEvents(t, s, nodes, at.Add(2*time.Millisecond))
	if rowCount(t, s, "http_certificate_reminders") != 1 || rowCount(t, s, "notification_deliveries") != 1 || rowCount(t, s, "alert_policies") != 0 {
		t.Fatal("multi-observer reminder duplicated or became host policy")
	}
	first, err := s.ClaimDelivery(ctx, at.Add(2*time.Millisecond), time.Minute)
	if err != nil || first == nil {
		t.Fatal(err)
	}
	if err := s.CheckDeliveryLease(ctx, first.ID, first.LeaseToken, at.Add(3*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteDelivery(ctx, first.ID, first.LeaseToken, at.Add(4*time.Millisecond), DeliveryOutcome{Ambiguous: true, ErrorClass: "transport_error", RetryAt: at.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	backup := filepath.Join(httpFixtureDir(t), "certificate-full.db")
	if err := s.ConsistentBackup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	path := s.DatabasePath()
	s.Close()
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
	s = restored
	loaded, err := s.ServiceAlertSettings(ctx, service.ID)
	if err != nil || !loaded.CertificateRemindersEnabled || loaded.Enabled {
		t.Fatal("certificate-only configuration not restored")
	}
	// Retry-After is rounded up to millisecond storage precision.
	retryAt := at.Add(time.Minute + time.Millisecond)
	retry, err := s.ClaimDelivery(ctx, retryAt, time.Minute)
	if err != nil || retry == nil || retry.ID != first.ID || retry.IdempotencyKey != first.IdempotencyKey || string(retry.Payload) != string(first.Payload) {
		t.Fatal("restored retry changed identity")
	}
	if err := s.CheckDeliveryLease(ctx, retry.ID, retry.LeaseToken, retryAt); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteDelivery(ctx, retry.ID, retry.LeaseToken, retryAt.Add(time.Millisecond), DeliveryOutcome{Delivered: true}); err != nil {
		t.Fatal(err)
	}
	for _, v := range []struct {
		after time.Duration
		level int
	}{{7 * 24 * time.Hour, 7}, {13 * 24 * time.Hour, 1}} {
		point := at.Add(v.after)
		for _, node := range nodes {
			certificateReport(t, s, service, node, point, []httpcheck.Certificate{c}, "success", "")
		}
		prepareCertificatePool(t, s, service, settings, nodes, point.Add(time.Millisecond))
		recordCertificateEvents(t, s, nodes, point.Add(2*time.Millisecond))
		d, err := s.ClaimDelivery(ctx, point.Add(2*time.Millisecond), time.Minute)
		if err != nil || d == nil {
			t.Fatal("next threshold missing", v.level, err)
		}
		if err := s.CheckDeliveryLease(ctx, d.ID, d.LeaseToken, point.Add(3*time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		if err := s.CompleteDelivery(ctx, d.ID, d.LeaseToken, point.Add(4*time.Millisecond), DeliveryOutcome{Delivered: true}); err != nil {
			t.Fatal(err)
		}
		recordCertificateEvents(t, s, nodes, point.Add(30*time.Second))
	}
	if rowCount(t, s, "http_certificate_reminders") != 3 || rowCount(t, s, "notification_deliveries") != 3 {
		t.Fatal("thresholds replayed")
	}
	jobs, err := s.ListIncidentDeliveries(ctx, first.IncidentID, 0, 25)
	if err != nil || len(jobs) != 1 || jobs[0].ID != first.ID || jobs[0].Status != "delivered" {
		t.Fatal("original receipt lost")
	}
	attempts, err := s.ListDeliveryAttempts(ctx, first.ID)
	if err != nil || len(attempts) != 2 || attempts[0].Outcome != "unknown" || attempts[1].Outcome != "delivered" {
		t.Fatal("restore rewrote unknown receipt")
	}
	records, err := s.ListCertificateReminders(ctx, service.ID, 0, 25)
	if err != nil || len(records) != 3 {
		t.Fatal(err)
	}
	for _, record := range records {
		if !record.QueuedOnce || !record.AuthorizedOnce || record.DeliveryStatus != "delivered" {
			t.Fatal("persistent certificate receipt missing")
		}
	}
	events, err := s.ListIncidents(ctx, IncidentFilter{ServiceID: service.ID, Limit: 25})
	if err != nil || len(events) != 3 {
		t.Fatal("certificate service event identity missing", err)
	}
	for _, event := range events {
		if event.Severity != "warning" || event.ServiceID != service.ID || event.ResolutionReason == "recovered" {
			t.Fatal("certificate reminder treated as technical recovery")
		}
	}
}

func TestCertificateFirstObservationSkipsOlderLevelsAndReplacementNeedsCompleteEvidence(t *testing.T) {
	s, service, _, nodes, settings := certificateFixture(t)
	ctx := context.Background()
	at := time.Now().UTC().Add(time.Second)
	old := httpcheck.Certificate{SHA256: strings.Repeat("cd", 32), NotBefore: at.Add(-time.Hour), NotAfter: at.Add(6 * 24 * time.Hour), Verified: true}
	for _, node := range nodes {
		certificateReport(t, s, service, node, at, []httpcheck.Certificate{old}, "success", "")
	}
	prepareCertificatePool(t, s, service, settings, nodes, at.Add(time.Millisecond))
	recordCertificateEvents(t, s, nodes, at.Add(2*time.Millisecond))
	if rowCount(t, s, "http_certificate_reminders") != 2 || rowCount(t, s, "notification_deliveries") != 1 {
		t.Fatal("older levels replayed")
	}
	rules, err := s.ListAlertRules(ctx)
	if err != nil || len(rules) != 1 {
		t.Fatal(err)
	}
	if err := s.DeleteAlertRule(ctx, rules[0].ID); !errors.Is(err, ErrServiceAlertRuleManaged) {
		t.Fatal("certificate rule deleted through host API")
	}
	next := old
	next.SHA256 = strings.Repeat("ef", 32)
	next.NotAfter = at.Add(5 * 24 * time.Hour)
	certificateReport(t, s, service, nodes[0], at.Add(time.Second), []httpcheck.Certificate{next}, "success", "")
	certificateReport(t, s, service, nodes[1], at.Add(time.Second), nil, "unobserved", "busy")
	pool := prepareCertificatePool(t, s, service, settings, nodes, at.Add(time.Second+time.Millisecond))
	if pool.Complete {
		t.Fatal("incomplete checks certified replacement")
	}
	original, err := s.Incident(ctx, func() string {
		items, err := s.ListIncidents(ctx, IncidentFilter{ServiceID: service.ID, Limit: 25})
		if err != nil || len(items) != 1 {
			t.Fatal(err)
		}
		return items[0].ID
	}())
	if err != nil || original.State != "firing" {
		t.Fatal("old certificate ended on missing evidence")
	}
	certificateReport(t, s, service, nodes[0], at.Add(2*time.Second), []httpcheck.Certificate{next}, "success", "")
	certificateReport(t, s, service, nodes[1], at.Add(2*time.Second), []httpcheck.Certificate{next}, "success", "")
	prepareCertificatePool(t, s, service, settings, nodes, at.Add(2*time.Second+time.Millisecond))
	ended, err := s.Incident(ctx, original.ID)
	if err != nil || ended.State != "resolved" || ended.ResolutionReason != "certificate_no_longer_observed" {
		t.Fatal("replacement did not end old warning")
	}
	recordCertificateEvents(t, s, nodes, at.Add(2*time.Second+2*time.Millisecond))
	if rowCount(t, s, "notification_deliveries") != 2 {
		t.Fatal("new certificate did not receive its own warning or generated recovery")
	}
}

func TestCertificateCanceledUnsentCanRearmButAuthorizedCannot(t *testing.T) {
	for _, authorized := range []bool{false, true} {
		t.Run(map[bool]string{false: "never-authorized", true: "authorized"}[authorized], func(t *testing.T) {
			s, service, _, nodes, settings := certificateFixture(t)
			ctx := context.Background()
			at := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
			c := httpcheck.Certificate{SHA256: strings.Repeat("ab", 32), NotBefore: at.Add(-time.Hour), NotAfter: at.Add(6 * 24 * time.Hour), Verified: true}
			for _, n := range nodes {
				certificateReport(t, s, service, n, at, []httpcheck.Certificate{c}, "success", "")
			}
			prepareCertificatePool(t, s, service, settings, nodes, at.Add(time.Millisecond))
			recordCertificateEvents(t, s, nodes, at.Add(2*time.Millisecond))
			items, err := s.ListIncidents(ctx, IncidentFilter{ServiceID: service.ID, Limit: 25})
			if err != nil || len(items) != 1 {
				t.Fatal("warning event missing")
			}
			var job *NotificationDelivery
			if authorized {
				job, err = s.ClaimDelivery(ctx, at.Add(3*time.Millisecond), time.Minute)
				if err != nil || job == nil {
					t.Fatal("claim missing")
				}
				if err := s.CheckDeliveryLease(ctx, job.ID, job.LeaseToken, at.Add(4*time.Millisecond)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.CreateMaintenanceWindow(ctx, MaintenanceWindow{Reason: "fixture maintenance", Timezone: "UTC", CreatedBy: "admin", NodeIDs: []string{items[0].NodeID}, StartsAt: at.Add(2 * time.Second), EndsAt: at.Add(4 * time.Second)}); err != nil {
				t.Fatal(err)
			}
			recordCertificateEvents(t, s, nodes, at.Add(3*time.Second))
			jobs, err := s.ListIncidentDeliveries(ctx, items[0].ID, 0, 25)
			if err != nil || len(jobs) != 1 || jobs[0].Status != "canceled" {
				t.Fatal("maintenance did not cancel old intent")
			}
			if authorized {
				if err := s.CompleteDelivery(ctx, job.ID, job.LeaseToken, at.Add(3*time.Second+time.Millisecond), DeliveryOutcome{Delivered: true}); err != nil {
					t.Fatal(err)
				}
				attempts, err := s.ListDeliveryAttempts(ctx, job.ID)
				if err != nil || len(attempts) != 1 || attempts[0].Outcome != "delivered" {
					t.Fatal("late real receipt lost")
				}
			}
			recordCertificateEvents(t, s, nodes, at.Add(5*time.Second))
			expected := 2
			if authorized {
				expected = 1
			}
			if rowCount(t, s, "notification_deliveries") != expected {
				t.Fatal("cancellation replayed an authorized reminder or lost an unsent reminder")
			}
		})
	}
}

func TestCertificateQueueAndLedgerCommitAtomically(t *testing.T) {
	s, service, _, nodes, settings := certificateFixture(t)
	at := time.Now().UTC().Add(time.Second)
	c := httpcheck.Certificate{SHA256: strings.Repeat("ab", 32), NotBefore: at.Add(-time.Hour), NotAfter: at.Add(6 * 24 * time.Hour), Verified: true}
	for _, n := range nodes {
		certificateReport(t, s, service, n, at, []httpcheck.Certificate{c}, "success", "")
	}
	prepareCertificatePool(t, s, service, settings, nodes, at.Add(time.Millisecond))
	if _, err := s.db.Exec(`CREATE TRIGGER reject_certificate_queue BEFORE INSERT ON notification_deliveries BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	rules, err := s.ListAlertRules(context.Background())
	if err != nil || len(rules) != 1 {
		t.Fatal("rule missing")
	}
	var anchor Node
	for _, n := range nodes {
		if n.ID == rules[0].NodeID {
			anchor = n
		}
	}
	if _, err := s.ObserveAlert(context.Background(), rules[0], anchor, AlertObservation{At: at.Add(2 * time.Millisecond), Known: true, Active: true}); err == nil {
		t.Fatal("forced queue failure succeeded")
	}
	var queued bool
	var incident, delivery *string
	if err := s.db.QueryRow(`SELECT queued_once,incident_id,delivery_id FROM http_certificate_reminders WHERE skipped=0`).Scan(&queued, &incident, &delivery); err != nil || queued || incident != nil || delivery != nil || rowCount(t, s, "alert_incidents") != 0 || rowCount(t, s, "notification_deliveries") != 0 {
		t.Fatal("failed queue consumed persistent identity")
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_certificate_queue`); err != nil {
		t.Fatal(err)
	}
	recordCertificateEvents(t, s, nodes, at.Add(3*time.Millisecond))
	if rowCount(t, s, "notification_deliveries") != 1 {
		t.Fatal("rolled back identity could not retry")
	}
}

func TestCertificateFinalSendChecksExactTierAndCurrentSettings(t *testing.T) {
	for _, change := range []string{"next-tier", "expired", "settings", "service-epoch"} {
		t.Run(change, func(t *testing.T) {
			s, service, _, nodes, settings := certificateFixture(t)
			ctx := context.Background()
			at := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
			expiry := at.Add(7*24*time.Hour + time.Second)
			if change == "expired" {
				expiry = at.Add(time.Second)
			}
			c := httpcheck.Certificate{SHA256: strings.Repeat("ab", 32), NotBefore: at.Add(-time.Hour), NotAfter: expiry, Verified: true}
			for _, n := range nodes {
				certificateReport(t, s, service, n, at, []httpcheck.Certificate{c}, "success", "")
			}
			prepareCertificatePool(t, s, service, settings, nodes, at.Add(time.Millisecond))
			recordCertificateEvents(t, s, nodes, at.Add(2*time.Millisecond))
			job, err := s.ClaimDelivery(ctx, at.Add(3*time.Millisecond), time.Minute)
			if err != nil || job == nil {
				t.Fatal("claim missing")
			}
			if change == "settings" {
				settings.CertificateRemindersEnabled = false
				if _, err := s.SaveServiceAlertSettings(ctx, settings); err != nil {
					t.Fatal(err)
				}
			}
			if change == "service-epoch" {
				if _, err := s.db.Exec(`UPDATE http_services SET updated_at=? WHERE id=?`, formatTime(at.Add(time.Second)), service.ID); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.CheckDeliveryLease(ctx, job.ID, job.LeaseToken, at.Add(2*time.Second)); !errors.Is(err, ErrDeliveryCanceled) {
				t.Fatal("obsolete certificate acquired send authority", err)
			}
			var authorized bool
			if err := s.db.QueryRow(`SELECT authorized_once FROM http_certificate_reminders WHERE rule_id=(SELECT rule_id FROM alert_incidents WHERE id=?)`, job.IncidentID).Scan(&authorized); err != nil || authorized {
				t.Fatal("rejected send consumed authorization")
			}
		})
	}
}

func TestAggregateApplicabilityUsesRequestedCertificateTime(t *testing.T) {
	s, service, _, nodes, settings := certificateFixture(t)
	ctx := context.Background()
	// Keep the fixture clock well ahead of the host clock, so the two clocks
	// fall on opposite sides of the seven-day reminder boundary.
	at := time.Now().UTC().Add(5 * time.Minute).Truncate(time.Millisecond)
	certificate := httpcheck.Certificate{SHA256: strings.Repeat("ab", 32), NotBefore: at.Add(-time.Hour), NotAfter: at.Add(7*24*time.Hour + time.Second), Verified: true}
	for _, node := range nodes {
		certificateReport(t, s, service, node, at, []httpcheck.Certificate{certificate}, "success", "")
	}
	prepareCertificatePool(t, s, service, settings, nodes, at.Add(time.Millisecond))
	recordCertificateEvents(t, s, nodes, at.Add(2*time.Millisecond))
	job, err := s.ClaimDelivery(ctx, at.Add(3*time.Millisecond), time.Minute)
	if err != nil || job == nil {
		t.Fatalf("expected certificate delivery: %v", err)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	valid, err := aggregateApplicable(ctx, tx, *job, at.Add(2*time.Second))
	if err != nil || valid {
		t.Fatalf("outdated tier must not be applicable: valid=%v err=%v", valid, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	var status string
	if err := s.db.QueryRowContext(ctx, `SELECT status FROM notification_deliveries WHERE id=?`, job.ID).Scan(&status); err != nil || status != "canceled" {
		t.Fatalf("outdated certificate delivery not canceled: %q %v", status, err)
	}
}

func TestCertificatePoolRejectsConflictingIdentityAndUnknownEvidence(t *testing.T) {
	s, service, _, nodes, settings := certificateFixture(t)
	at := time.Now().UTC().Add(time.Second)
	c := httpcheck.Certificate{SHA256: strings.Repeat("AB", 32), NotBefore: at.Add(-time.Hour), NotAfter: at.Add(6 * 24 * time.Hour), Verified: true}
	certificateReport(t, s, service, nodes[0], at, []httpcheck.Certificate{c}, "success", "")
	conflict := c
	conflict.NotAfter = at.Add(5 * 24 * time.Hour)
	certificateReport(t, s, service, nodes[1], at, []httpcheck.Certificate{conflict}, "success", "")
	pool := prepareCertificatePool(t, s, service, settings, nodes, at.Add(time.Millisecond))
	if pool.Complete || len(pool.Certificates) != 0 || rowCount(t, s, "http_certificate_reminders") != 0 {
		t.Fatal("conflicting dates accepted as verified identity")
	}
	certificateReport(t, s, service, nodes[1], at.Add(time.Second), nil, "unobserved", "busy")
	pool = prepareCertificatePool(t, s, service, settings, nodes, at.Add(time.Second+time.Millisecond))
	if pool.Complete || len(pool.Certificates) != 1 {
		t.Fatal("partial verified evidence lost or unknown certified completeness")
	}
	if _, ok := pool.Certificates[strings.ToLower(c.SHA256)]; !ok {
		t.Fatal("fingerprint was not canonicalized")
	}
}

func TestCertificateUnsentCanceledChannelCanRearmWithoutRepeatDelay(t *testing.T) {
	s, service, channel, nodes, settings := certificateFixture(t)
	ctx := context.Background()
	at := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
	c := httpcheck.Certificate{SHA256: strings.Repeat("ab", 32), NotBefore: at.Add(-time.Hour), NotAfter: at.Add(6 * 24 * time.Hour), Verified: true}
	for _, n := range nodes {
		certificateReport(t, s, service, n, at, []httpcheck.Certificate{c}, "success", "")
	}
	prepareCertificatePool(t, s, service, settings, nodes, at.Add(time.Millisecond))
	recordCertificateEvents(t, s, nodes, at.Add(2*time.Millisecond))
	if _, err := s.UpdateNotificationChannel(ctx, channel.ID, channel.Name, channel.Kind, nil, false); err != nil {
		t.Fatal(err)
	}
	if job, err := s.ClaimDelivery(ctx, at.Add(time.Second), time.Minute); err != nil || job != nil {
		t.Fatal("disabled channel acquired send authority")
	}
	if _, err := s.UpdateNotificationChannel(ctx, channel.ID, channel.Name, channel.Kind, nil, true); err != nil {
		t.Fatal(err)
	}
	recordCertificateEvents(t, s, nodes, at.Add(2*time.Second))
	if rowCount(t, s, "notification_deliveries") != 2 {
		t.Fatal("unattempted reminder waited for repeat delay")
	}
	recordCertificateEvents(t, s, nodes, at.Add(3*time.Second))
	if rowCount(t, s, "notification_deliveries") != 2 {
		t.Fatal("fresh report duplicated pending reminder")
	}
}

func TestCertificateAuthorizationAndSendMarkerRollBackTogether(t *testing.T) {
	s, service, _, nodes, settings := certificateFixture(t)
	ctx := context.Background()
	at := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
	c := httpcheck.Certificate{SHA256: strings.Repeat("ab", 32), NotBefore: at.Add(-time.Hour), NotAfter: at.Add(6 * 24 * time.Hour), Verified: true}
	for _, n := range nodes {
		certificateReport(t, s, service, n, at, []httpcheck.Certificate{c}, "success", "")
	}
	prepareCertificatePool(t, s, service, settings, nodes, at.Add(time.Millisecond))
	recordCertificateEvents(t, s, nodes, at.Add(2*time.Millisecond))
	job, err := s.ClaimDelivery(ctx, at.Add(3*time.Millisecond), time.Minute)
	if err != nil || job == nil {
		t.Fatal("claim missing")
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_certificate_authorization BEFORE UPDATE OF authorized_once ON http_certificate_reminders WHEN NEW.authorized_once=1 BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckDeliveryLease(ctx, job.ID, job.LeaseToken, at.Add(4*time.Millisecond)); err == nil {
		t.Fatal("forced authorization succeeded")
	}
	var sent, authorized bool
	if err := s.db.QueryRow(`SELECT d.send_authorized,l.authorized_once FROM notification_deliveries d JOIN http_certificate_reminders l ON l.delivery_id=d.id WHERE d.id=?`, job.ID).Scan(&sent, &authorized); err != nil || sent || authorized {
		t.Fatal("failed authorization partially committed")
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_certificate_authorization`); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckDeliveryLease(ctx, job.ID, job.LeaseToken, at.Add(5*time.Millisecond)); err != nil {
		t.Fatal("rolled back authorization could not retry", err)
	}
}

func TestCertificateMissingDeliveryHistoryDoesNotCreateNewIntent(t *testing.T) {
	s, service, _, nodes, settings := certificateFixture(t)
	ctx := context.Background()
	at := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
	c := httpcheck.Certificate{SHA256: strings.Repeat("ab", 32), NotBefore: at.Add(-time.Hour), NotAfter: at.Add(6 * 24 * time.Hour), Verified: true}
	for _, n := range nodes {
		certificateReport(t, s, service, n, at, []httpcheck.Certificate{c}, "success", "")
	}
	prepareCertificatePool(t, s, service, settings, nodes, at.Add(time.Millisecond))
	recordCertificateEvents(t, s, nodes, at.Add(2*time.Millisecond))
	if _, err := s.db.Exec(`DELETE FROM notification_deliveries`); err != nil {
		t.Fatal(err)
	}
	recordCertificateEvents(t, s, nodes, at.Add(time.Second))
	if rowCount(t, s, "notification_deliveries") != 0 {
		t.Fatal("missing history regenerated an uncertain old intent")
	}
	records, err := s.ListCertificateReminders(ctx, service.ID, 0, 25)
	if err != nil || len(records) != 2 || records[0].DeliveryStatus != "history_unavailable" || !records[0].QueuedOnce {
		t.Fatal("missing history falsely labeled success")
	}
}

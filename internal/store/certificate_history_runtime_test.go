package store

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
)

func TestCertificateHistoryRuntimeIndexesRollbackAndKeepHistory(t *testing.T) {
	s := preNodeBatchIntegrationStore(t, false)
	ctx := context.Background()
	for _, name := range []string{"046_node_batches.sql", "047_node_timeline_indexes.sql"} {
		body, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(string(body)); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`, name, nowText()); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec(`INSERT INTO http_certificate_reminders(service_id,certificate_sha256,days_before,not_before,not_after,rule_id,queued_once,authorized_once,skipped,incident_id,delivery_id,created_at,updated_at) VALUES('deleted-service',?,14,'2026-01-01T00:00:00Z','2026-02-01T00:00:00Z','historical-rule',1,1,1,'historical-incident','historical-delivery','2026-01-18T00:00:00Z','2026-02-01T00:00:00Z')`, strings.Repeat("ab", 32)); err != nil {
		t.Fatal(err)
	}
	history := func() string {
		t.Helper()
		var raw string
		if err := s.db.QueryRow(`SELECT json_object('seq',seq,'service',service_id,'sha',certificate_sha256,'days',days_before,'before',not_before,'after',not_after,'rule',rule_id,'queued',queued_once,'authorized',authorized_once,'skipped',skipped,'incident',incident_id,'delivery',delivery_id,'created',created_at,'updated',updated_at) FROM http_certificate_reminders`).Scan(&raw); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	before := history()
	if _, err := s.db.Exec(`CREATE TRIGGER reject048 BEFORE INSERT ON schema_migrations WHEN NEW.version='048_certificate_runtime_indexes.sql' BEGIN SELECT RAISE(ABORT,'synthetic receipt failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err == nil {
		t.Fatal("failed runtime-index migration reported success")
	}
	assertCount(t, s, `SELECT count(*) FROM sqlite_master WHERE name IN ('idx_alert_rules_enabled_created','idx_certificate_reminders_unskipped_service')`, 0)
	assertCount(t, s, `SELECT count(*) FROM schema_migrations WHERE version='048_certificate_runtime_indexes.sql'`, 0)
	if history() != before {
		t.Fatal("failed index migration changed permanent history")
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject048`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	assertCount(t, s, `SELECT count(*) FROM sqlite_master WHERE name IN ('idx_alert_rules_enabled_created','idx_certificate_reminders_unskipped_service')`, 2)
	assertCount(t, s, `SELECT count(*) FROM schema_migrations WHERE version='048_certificate_runtime_indexes.sql'`, 1)
	if history() != before {
		t.Fatal("applied index migration changed permanent history")
	}
}

func TestCertificateHistoryEnabledRulesAndIndexSelection(t *testing.T) {
	s, _, rule, _ := closedCertificateHistory(t)
	ctx := context.Background()
	all, err := s.ListAlertRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	enabled, err := s.ListEnabledAlertRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) != 1 || all[0].ID != rule || len(enabled) != 0 {
		t.Fatal("administrative history or enabled-only workset changed")
	}
	active, err := s.CreateAlertRule(ctx, all[0].NodeID, all[0].ChannelID, "cpu", json.RawMessage(`{"threshold_percent":90}`), 900)
	if err != nil {
		t.Fatal(err)
	}
	enabled, err = s.ListEnabledAlertRules(ctx)
	if err != nil || len(enabled) != 1 || enabled[0].ID != active.ID {
		t.Fatal("enabled host rule missing from current workset", err)
	}
	all, err = s.ListAlertRules(ctx)
	if err != nil || len(all) != 2 {
		t.Fatal("administrative disabled history was filtered", err)
	}
	for _, item := range []struct{ query, index string }{
		{`SELECT id FROM alert_rules WHERE enabled=1 ORDER BY created_at`, "idx_alert_rules_enabled_created"},
		{`SELECT seq,rule_id FROM http_certificate_reminders WHERE service_id='fixture' AND skipped=0 ORDER BY seq`, "idx_certificate_reminders_unskipped_service"},
	} {
		rows, err := s.db.Query("EXPLAIN QUERY PLAN " + item.query)
		if err != nil {
			t.Fatal(err)
		}
		var plan strings.Builder
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			fmt.Fprintln(&plan, detail)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
		if !strings.Contains(plan.String(), item.index) {
			t.Fatalf("runtime query lost partial index %s: %s", item.index, plan.String())
		}
	}
}

func assertCertificateHistoryCount(t *testing.T, s *Store, query string, want int, args ...any) {
	t.Helper()
	var got int
	if err := s.db.QueryRow(query, args...).Scan(&got); err != nil || got != want {
		t.Fatalf("history count got %d want %d: %v", got, want, err)
	}
}

func closedCertificateHistory(t *testing.T) (*Store, HTTPService, string, time.Time) {
	t.Helper()
	s, service, _, nodes, settings := certificateFixture(t)
	at := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
	cert := httpcheck.Certificate{SHA256: strings.Repeat("ac", 32), NotBefore: at.Add(-time.Hour), NotAfter: at.Add(14 * 24 * time.Hour), Verified: true}
	for _, node := range nodes {
		certificateReport(t, s, service, node, at, []httpcheck.Certificate{cert}, "success", "")
	}
	prepareCertificatePool(t, s, service, settings, nodes, at.Add(time.Millisecond))
	recordCertificateEvents(t, s, nodes, at.Add(2*time.Millisecond))
	closed := at.Add(15 * 24 * time.Hour)
	prepareCertificatePool(t, s, service, settings, nodes, closed)
	var rule string
	var skipped, enabled bool
	if err := s.db.QueryRow(`SELECT l.rule_id,l.skipped,r.enabled FROM http_certificate_reminders l JOIN alert_rules r ON r.id=l.rule_id WHERE l.certificate_sha256=? AND l.days_before=14`, cert.SHA256).Scan(&rule, &skipped, &enabled); err != nil || !skipped || enabled {
		t.Fatal("closed real certificate history precondition", err)
	}
	return s, service, rule, closed
}

func TestCertificateHistoryClosedDefinitionsAreNotRewritten(t *testing.T) {
	s, service, rule, closed := closedCertificateHistory(t)
	ctx := context.Background()
	var config, updated string
	if err := s.db.QueryRow(`SELECT config_json,updated_at FROM alert_rules WHERE id=?`, rule).Scan(&config, &updated); err != nil {
		t.Fatal(err)
	}
	service.Name = "Current service renamed after historical closure"
	if _, err := s.SaveHTTPService(ctx, service); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareAlertPolicyRules(ctx, closed.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var afterConfig, afterUpdated string
	if err := s.db.QueryRow(`SELECT config_json,updated_at FROM alert_rules WHERE id=?`, rule).Scan(&afterConfig, &afterUpdated); err != nil {
		t.Fatal(err)
	}
	if afterConfig != config || afterUpdated != updated {
		t.Fatal("closed disabled certificate definition was rewritten by current synchronization")
	}
	assertCount(t, s, `SELECT count(*) FROM http_certificate_reminders`, 1)
	assertCertificateHistoryCount(t, s, `SELECT count(*) FROM alert_rules WHERE id=? AND enabled=0`, 1, rule)
}

func TestCertificateHistorySkippedButEnabledDefinitionIsStillRevoked(t *testing.T) {
	s, _, rule, closed := closedCertificateHistory(t)
	if _, err := s.db.Exec(`UPDATE alert_rules SET enabled=1 WHERE id=?`, rule); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareAlertPolicyRules(context.Background(), closed.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	assertCertificateHistoryCount(t, s, `SELECT count(*) FROM alert_rules WHERE id=? AND enabled=0`, 1, rule)
}

func TestCertificateHistoryPausedUnskippedDefinitionStillTracksSettings(t *testing.T) {
	s, service, _, nodes, settings := certificateFixture(t)
	at := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
	cert := httpcheck.Certificate{SHA256: strings.Repeat("ad", 32), NotBefore: at.Add(-time.Hour), NotAfter: at.Add(14 * 24 * time.Hour), Verified: true}
	for _, node := range nodes {
		certificateReport(t, s, service, node, at, []httpcheck.Certificate{cert}, "success", "")
	}
	prepareCertificatePool(t, s, service, settings, nodes, at.Add(time.Millisecond))
	service.Enabled = false
	service.Name = "Paused current service name"
	if _, err := s.SaveHTTPService(context.Background(), service); err != nil {
		t.Fatal(err)
	}
	if _, err := s.PrepareAlertPolicyRules(context.Background(), at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	var raw string
	var enabled bool
	if err := s.db.QueryRow(`SELECT r.config_json,r.enabled FROM alert_rules r JOIN http_certificate_reminders l ON l.rule_id=r.id WHERE l.certificate_sha256=?`, cert.SHA256).Scan(&raw, &enabled); err != nil {
		t.Fatal(err)
	}
	var config ServiceCertificateRuleConfig
	if err := json.Unmarshal([]byte(raw), &config); err != nil || enabled || config.ServiceName != service.Name {
		t.Fatal("paused unskipped definition retained stale configuration", err)
	}
}

func TestCertificateHistoryReobservedSkippedFingerprintKeepsOriginalIdentity(t *testing.T) {
	s, service, _, nodes, settings := certificateFixture(t)
	at := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
	a := httpcheck.Certificate{SHA256: strings.Repeat("ae", 32), NotBefore: at.Add(-time.Hour), NotAfter: at.Add(14 * 24 * time.Hour), Verified: true}
	b := a
	b.SHA256 = strings.Repeat("af", 32)
	for i, cert := range []httpcheck.Certificate{a, b, a} {
		point := at.Add(time.Duration(i) * time.Second)
		for _, node := range nodes {
			certificateReport(t, s, service, node, point, []httpcheck.Certificate{cert}, "success", "")
		}
		prepareCertificatePool(t, s, service, settings, nodes, point.Add(time.Millisecond))
	}
	assertCount(t, s, `SELECT count(*) FROM http_certificate_reminders`, 2)
	assertCertificateHistoryCount(t, s, `SELECT count(*) FROM http_certificate_reminders l JOIN alert_rules r ON r.id=l.rule_id WHERE l.certificate_sha256=? AND l.days_before=14 AND l.skipped=1 AND r.enabled=0`, 1, a.SHA256)
}

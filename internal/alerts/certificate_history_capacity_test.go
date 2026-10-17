package alerts

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
	v1 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func certificateHistoryDigest(t *testing.T, db *sql.DB) string {
	t.Helper()
	h := sha256.New()
	for _, query := range []string{
		`SELECT service_id,certificate_sha256,days_before,not_before,not_after,rule_id,queued_once,authorized_once,skipped,COALESCE(incident_id,''),COALESCE(delivery_id,''),created_at,updated_at FROM http_certificate_reminders ORDER BY seq`,
		`SELECT id,node_id,channel_id,kind,config_json,enabled,cooldown_seconds,created_at,updated_at FROM alert_rules WHERE kind='service_certificate' ORDER BY id`,
	} {
		rows, err := db.Query(query)
		if err != nil {
			t.Fatal(err)
		}
		cols, err := rows.Columns()
		if err != nil {
			rows.Close()
			t.Fatal(err)
		}
		for rows.Next() {
			values := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range values {
				ptrs[i] = &values[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			raw, err := json.Marshal(values)
			if err != nil {
				rows.Close()
				t.Fatal(err)
			}
			h.Write(raw)
			h.Write([]byte{'\n'})
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
	}
	return hex.EncodeToString(h.Sum(nil))
}

// SQL copies a real closed certificate definition/identity. This tests historical
// storage and current evaluation, not 100k real rotations or notification sends.
func TestPermanentCertificateHistoryCapacity(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_CERTIFICATE_HISTORY_CAPACITY") != "1" {
		t.Skip("opt-in disk-backed permanent certificate history")
	}
	must := func(err error) {
		t.Helper()
		if err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	root := t.TempDir()
	file := filepath.Join(root, "history.db")
	db, err := store.Open(ctx, file)
	must(err)
	defer db.Close()
	engine := New(db, strings.Repeat("g", 32), nil, nil, "https://probe.example.test")
	engine.EnableHTTPServiceAlerts(true)
	engine.SetHTTPAgentAvailability(func(string) bool { return true })
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "Synthetic history observer"})
	must(err)
	channel, err := engine.CreateChannel(ctx, "Synthetic never sent", "webhook", ChannelConfig{URL: "https://example.test/never-sent"})
	must(err)
	service, err := db.SaveHTTPService(ctx, store.HTTPService{Name: "Synthetic expired history", Enabled: true, IntervalSeconds: 30, NodeIDs: []string{node.ID}, Spec: httpcheck.Spec{URL: "https://example.test/synthetic", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 1000, MaxBodyBytes: 1024}})
	must(err)
	_, err = db.SaveServiceAlertSettings(ctx, store.ServiceAlertSettings{ServiceID: service.ID, ServiceRevision: service.Revision, ChannelID: channel.ID, CertificateRemindersEnabled: true, CooldownSeconds: 300})
	must(err)
	must(db.SaveAgentMetadata(ctx, node.ID, v1.Hello{Capabilities: []string{"http_probe.v1"}}))
	at := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
	must(db.SaveReport(ctx, node.ID, v1.Report{CapturedAt: at}))
	task, err := db.CreateHTTPTask(ctx, service.ID, node.ID, at, at)
	must(err)
	cert := httpcheck.Certificate{SHA256: strings.Repeat("ab", 32), NotBefore: at.Add(-time.Hour), NotAfter: at.Add(6 * 24 * time.Hour), Verified: true}
	must(db.SaveHTTPResult(ctx, node.ID, httpcheck.Result{TaskID: task.ID, ServiceID: service.ID, Revision: task.Revision, ScheduledAt: at, CompletedAt: at.Add(time.Millisecond), Outcome: "success", StatusCode: 200, Certificates: []httpcheck.Certificate{cert}}, at.Add(time.Millisecond)))
	must(engine.Tick(ctx, at.Add(2*time.Millisecond)))
	closed := at.Add(7 * 24 * time.Hour)
	must(db.SaveReport(ctx, node.ID, v1.Report{CapturedAt: closed}))
	must(engine.Tick(ctx, closed))
	must(db.DeleteHTTPService(ctx, service.ID, service.Revision))
	must(engine.Tick(ctx, closed.Add(time.Second)))
	_, err = engine.CreateRule(ctx, node.ID, channel.ID, "cpu", RuleConfig{ThresholdPercent: 90}, 900)
	must(err)
	must(engine.Tick(ctx, closed.Add(2*time.Second)))
	inspection, err := sql.Open("sqlite", file)
	must(err)
	defer inspection.Close()
	var exemplar string
	must(inspection.QueryRow(`SELECT l.rule_id FROM http_certificate_reminders l JOIN alert_rules r ON r.id=l.rule_id WHERE l.skipped=1 AND r.enabled=0 LIMIT 1`).Scan(&exemplar))
	var filled int
	must(inspection.QueryRow(`SELECT count(*) FROM http_certificate_reminders`).Scan(&filled))
	if filled == 0 || filled >= 1000 {
		t.Fatal("real history precondition")
	}
	levels := []map[string]any{}
	for _, count := range []int{1000, 10000, 100000} {
		for filled < count {
			n := min(500, count-filled)
			tx, err := inspection.BeginTx(ctx, nil)
			must(err)
			_, err = tx.ExecContext(ctx, `WITH RECURSIVE seq(i) AS (VALUES(?) UNION ALL SELECT i+1 FROM seq WHERE i+1<?)
INSERT INTO alert_rules(id,node_id,channel_id,kind,config_json,enabled,cooldown_seconds,created_at,updated_at)
SELECT 'history-rule-'||printf('%08d',i),r.node_id,r.channel_id,r.kind,json_set(r.config_json,'$.certificate_sha256',printf('%064x',i)),0,r.cooldown_seconds,r.created_at,r.updated_at FROM seq CROSS JOIN alert_rules r WHERE r.id=?`, filled, filled+n, exemplar)
			must(err)
			_, err = tx.ExecContext(ctx, `WITH RECURSIVE seq(i) AS (VALUES(?) UNION ALL SELECT i+1 FROM seq WHERE i+1<?)
INSERT INTO http_certificate_reminders(service_id,certificate_sha256,days_before,not_before,not_after,rule_id,queued_once,authorized_once,skipped,created_at,updated_at)
SELECT l.service_id,printf('%064x',i),l.days_before,l.not_before,l.not_after,'history-rule-'||printf('%08d',i),l.queued_once,l.authorized_once,l.skipped,l.created_at,l.updated_at FROM seq CROSS JOIN http_certificate_reminders l WHERE l.rule_id=?`, filled, filled+n, exemplar)
			must(err)
			must(tx.Commit())
			filled += n
		}
		var actual int
		must(inspection.QueryRow(`SELECT count(*) FROM http_certificate_reminders`).Scan(&actual))
		if actual != count {
			t.Fatal("history count differs")
		}
		beforeDigest := certificateHistoryDigest(t, inspection)
		var beforeReports int64
		must(inspection.QueryRow(`SELECT count(*) FROM metric_samples`).Scan(&beforeReports))
		type writes struct {
			attempts, successes int
			failures            []string
			maximumMS           float64
		}
		ready := make(chan struct{})
		stop := make(chan struct{})
		done := make(chan writes, 1)
		point := closed.Add(time.Duration(count) * time.Hour)
		go func() {
			w := writes{}
			for {
				select {
				case <-stop:
					done <- w
					return
				default:
				}
				w.attempts++
				start := time.Now()
				err := db.SaveReport(ctx, node.ID, v1.Report{CapturedAt: point.Add(time.Duration(w.attempts) * time.Millisecond)})
				w.maximumMS = max(w.maximumMS, float64(time.Since(start))/float64(time.Millisecond))
				if err == nil {
					w.successes++
				} else {
					w.failures = append(w.failures, "store_write_error")
				}
				if w.attempts == 1 {
					close(ready)
				}
				select {
				case <-stop:
					done <- w
					return
				case <-time.After(50 * time.Millisecond):
				}
			}
		}()
		<-ready
		var durations []float64
		var tickErr error
		for i := range 3 {
			start := time.Now()
			if tickErr = engine.Tick(ctx, point.Add(time.Duration(i)*time.Second)); tickErr != nil {
				break
			}
			durations = append(durations, time.Since(start).Seconds())
			time.Sleep(200 * time.Millisecond)
		}
		close(stop)
		w := <-done
		must(tickErr)
		var afterReports int64
		must(inspection.QueryRow(`SELECT count(*) FROM metric_samples`).Scan(&afterReports))
		if w.attempts == 0 || len(w.failures) != 0 || afterReports-beforeReports != int64(w.successes) || w.attempts != w.successes {
			t.Fatalf("concurrent report accounting: attempts=%d success=%d errors=%d stored=%d", w.attempts, w.successes, len(w.failures), afterReports-beforeReports)
		}
		if certificateHistoryDigest(t, inspection) != beforeDigest {
			t.Fatal("runtime rewrote permanent identities or closed definitions")
		}
		enabled, err := db.ListEnabledAlertRules(ctx)
		must(err)
		if len(enabled) != 1 || enabled[0].Kind != "cpu" {
			t.Fatal("current CPU rule or historical disablement lost")
		}
		levels = append(levels, map[string]any{"identities": actual, "full_tick_seconds": durations, "report_attempts": w.attempts, "report_stored": w.successes, "write_errors": len(w.failures), "max_store_write_ms": w.maximumMS, "history_sha256": beforeDigest})
	}
	fingerprint := certificateHistoryDigest(t, inspection)
	must(db.ApplyRetention(ctx, closed.Add(400*24*time.Hour), store.DefaultRetentionPolicy()))
	if certificateHistoryDigest(t, inspection) != fingerprint {
		t.Fatal("retention changed certificate history")
	}
	backup := filepath.Join(root, "consistent.db")
	must(db.ConsistentBackup(ctx, backup))
	reopened, err := store.Open(ctx, backup)
	must(err)
	defer reopened.Close()
	check, err := sql.Open("sqlite", backup)
	must(err)
	defer check.Close()
	if certificateHistoryDigest(t, check) != fingerprint {
		t.Fatal("backup/reopen lost full identity history")
	}
	must(New(nil, strings.Repeat("g", 32), nil, nil).VerifyRecoveryChannels(ctx, reopened))
	if output := os.Getenv("MYPROBE_TEST_CERTIFICATE_HISTORY_OUTPUT"); output != "" {
		info, err := os.Stat(output)
		must(err)
		if !filepath.IsAbs(output) || !info.IsDir() {
			t.Fatal("existing absolute aggregate output directory required")
		}
		source := os.Getenv("MYPROBE_TEST_SOURCE_COMMIT")
		if source == "" {
			source = "working-tree"
		}
		sourceHashes := map[string]string{}
		for _, name := range []string{"internal/alerts/service.go", "internal/alerts/certificate_history_capacity_test.go", "internal/store/certificate_reminders.go", "internal/store/alerts.go", "internal/store/alert_policy_rules.go", "internal/store/store.go", "internal/store/migrations/048_certificate_runtime_indexes.sql", "go.mod", "go.sum"} {
			body, err := os.ReadFile(filepath.Join("../..", name))
			must(err)
			digest := sha256.Sum256(body)
			sourceHashes[name] = hex.EncodeToString(digest[:])
		}
		result := map[string]any{"format": "myprobe.permanent-certificate-history-capacity.v1", "source_commit": source, "os": runtime.GOOS, "arch": runtime.GOARCH, "go": runtime.Version(), "levels": levels, "history_sha256": fingerprint, "retention_and_consistent_backup_preserved": true, "synthetic_channel_key_after_reopen_verified": true, "network_sends": 0, "passed": true, "limitations": []string{"SQL-cloned real closed/deleted-service history; no 100k real rotations or sends", "Three individual full engine ticks per level; no p95, Server API/RSS, target-hardware or 24h claim", "Concurrent production Store writes use normal SQLite busy timeout, without a custom per-write deadline"}}
		result["source_sha256"] = sourceHashes
		raw, err := json.MarshalIndent(result, "", "  ")
		must(err)
		f, err := os.OpenFile(filepath.Join(output, "certificate-history.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		must(err)
		_, err = f.Write(append(raw, '\n'))
		must(err)
		must(f.Sync())
		must(f.Close())
	}
	t.Logf("permanent certificate history: 1k/10k/100k, current CPU rule, concurrent report writes, unchanged history and full consistent backup/reopen passed")
}

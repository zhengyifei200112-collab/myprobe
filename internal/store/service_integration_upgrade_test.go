package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"io/fs"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/alertpolicy"
	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
)

func TestServiceIncidentIntegrationUpgradesEitherDevelopmentSchema(t *testing.T) {
	for _, source := range []string{"http", "incidents"} {
		t.Run(source, func(t *testing.T) {
			ctx := context.Background()
			path := filepath.Join(t.TempDir(), "source.db")
			db, err := sql.Open("sqlite", sqliteDSN(path))
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			entries, err := fs.ReadDir(migrations, "migrations")
			if err != nil {
				t.Fatal(err)
			}
			for _, entry := range entries {
				name := entry.Name()
				httpMigration := name >= "016_" && name < "021_"
				if source == "http" && (name >= "021_" || name >= "015_" && name < "016_") || source == "incidents" && httpMigration {
					continue
				}
				raw, err := migrations.ReadFile("migrations/" + name)
				if err != nil {
					t.Fatal(err)
				}
				if _, err := db.ExecContext(ctx, string(raw)); err != nil {
					t.Fatal(err)
				}
				if _, err := db.ExecContext(ctx, `INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`, name, nowText()); err != nil {
					t.Fatal(err)
				}
			}
			old := &Store{db: db, path: path}
			node, _, err := old.CreateNode(ctx, CreateNodeParams{Name: "upgrade observer"})
			if err != nil {
				t.Fatal(err)
			}
			at := time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
			var service HTTPService
			var completed, pending httpcheck.Task
			var group *NotificationAggregate
			var send AggregateSend
			var incident *Incident
			var policy AlertPolicy
			if source == "http" {
				service, err = old.SaveHTTPService(ctx, HTTPService{Name: "upgrade service", Enabled: true, NodeIDs: []string{node.ID}, IntervalSeconds: 30, Spec: httpcheck.Spec{URL: "http://example.test/", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 1000, MaxBodyBytes: 1024}})
				if err != nil {
					t.Fatal(err)
				}
				completed, err = old.CreateHTTPTask(ctx, service.ID, node.ID, at, at)
				if err != nil {
					t.Fatal(err)
				}
				result := httpcheck.Result{TaskID: completed.ID, ServiceID: service.ID, Revision: completed.Revision, ScheduledAt: completed.ScheduledAt, CompletedAt: at.Add(time.Millisecond), Outcome: "success", StatusCode: 200, DurationMS: 1}
				if err := old.SaveHTTPResult(ctx, node.ID, result, at.Add(time.Millisecond)); err != nil {
					t.Fatal(err)
				}
				pending, err = old.CreateHTTPTask(ctx, service.ID, node.ID, at.Add(30*time.Second), at.Add(30*time.Second))
				if err != nil {
					t.Fatal(err)
				}
			} else {
				channel, err := old.CreateNotificationChannel(ctx, "upgrade channel", "webhook", "synthetic config")
				if err != nil {
					t.Fatal(err)
				}
				policy, err = old.SaveAlertPolicy(ctx, AlertPolicy{Policy: alertpolicy.Policy{Key: "upgrade", Scope: alertpolicy.Scope{Kind: "all"}, Enabled: true}, Name: "upgrade policy", Kind: "cpu", ChannelID: channel.ID, Config: json.RawMessage(`{"threshold_percent":90,"aggregation_seconds":5}`), CooldownSeconds: 300})
				if err != nil {
					t.Fatal(err)
				}
				if err := old.SyncAlertPolicyRules(ctx, at); err != nil {
					t.Fatal(err)
				}
				rules, err := old.ListAlertRules(ctx)
				if err != nil || len(rules) != 1 {
					t.Fatal(err)
				}
				incident = observe(t, old, rules[0], node, at, true, true)
				group = claimAggregate(t, old, at.Add(5*time.Second))
				send = prepareAggregate(t, old, group, at.Add(5*time.Second))
				pauseFixtureChannel(t, old, channel.ID, at.Add(5*time.Second), at.Add(time.Hour))
			}
			if err := old.Close(); err != nil {
				t.Fatal(err)
			}
			integrated, err := Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			defer integrated.Close()
			if err := integrated.Migrate(ctx); err != nil {
				t.Fatal(err)
			}
			if source == "http" {
				current, err := integrated.HTTPService(ctx, service.ID)
				if err != nil || !reflect.DeepEqual(current, service) {
					t.Fatal("HTTP upgrade changed configuration")
				}
				observations, err := integrated.RecentHTTPResults(ctx, service.ID, 10)
				if err != nil || len(observations) != 2 || observations[0].TaskID != pending.ID || observations[0].Result != nil || observations[1].TaskID != completed.ID || observations[1].Result == nil || observations[1].Result.Outcome != "success" {
					t.Fatal("HTTP task/result identity lost")
				}
				result := httpcheck.Result{TaskID: pending.ID, ServiceID: service.ID, Revision: pending.Revision, ScheduledAt: pending.ScheduledAt, CompletedAt: pending.ScheduledAt.Add(time.Millisecond), Outcome: "failure", StatusCode: 503, ErrorClass: "status_mismatch", DurationMS: 1}
				if err := integrated.SaveHTTPResult(ctx, node.ID, result, result.CompletedAt); err != nil {
					t.Fatal("pending HTTP admission lost")
				}
				// The incident schema was absent in this source database and is usable now.
				channel, err := integrated.CreateNotificationChannel(ctx, "new incidents", "webhook", "synthetic")
				if err != nil {
					t.Fatal(err)
				}
				rule, err := integrated.CreateAlertRule(ctx, node.ID, channel.ID, "cpu", json.RawMessage(`{"threshold_percent":90}`), 300)
				if err != nil {
					t.Fatal(err)
				}
				observe(t, integrated, rule, node, at, true, true)
			} else {
				current, err := integrated.Incident(ctx, incident.ID)
				if err != nil || !reflect.DeepEqual(current, *incident) {
					t.Fatal("HTTP migration changed incident")
				}
				detail, err := integrated.AggregateContext(ctx, group.ID, 0, 25)
				if err != nil || detail.Aggregate.Status != "inflight" || len(detail.Attempts) != 1 || detail.Attempts[0].Content == nil {
					t.Fatal("aggregate history lost")
				}
				var payload, key string
				if err := integrated.db.QueryRow(`SELECT payload_json,idempotency_key FROM notification_aggregate_attempts WHERE aggregate_id=?`, group.ID).Scan(&payload, &key); err != nil || payload != string(send.Payload) || key != send.IdempotencyKey {
					t.Fatal("aggregate identity changed")
				}
				channel, err := integrated.NotificationChannel(ctx, policy.ChannelID)
				if err != nil || channel.DeliveryPausedUntil == nil || !channel.DeliveryPausedUntil.Equal(at.Add(time.Hour)) {
					t.Fatal("shared wait lost")
				}
				if err := integrated.CompleteAggregate(ctx, group.ID, group.LeaseToken, at.Add(6*time.Second), DeliveryOutcome{Delivered: true}); err != nil {
					t.Fatal("current aggregate lease lost")
				}
				if _, err := integrated.SaveHTTPService(ctx, HTTPService{Name: "new service", Enabled: true, NodeIDs: []string{node.ID}, IntervalSeconds: 30, Spec: httpcheck.Spec{URL: "http://example.test/", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 1000, MaxBodyBytes: 1024}}); err != nil {
					t.Fatal("HTTP schema unavailable")
				}
			}
			rows, err := integrated.db.Query(`PRAGMA foreign_key_check`)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			if rows.Next() {
				t.Fatal("integration left foreign-key damage")
			}
		})
	}
}

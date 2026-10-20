package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/alertpolicy"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

// This optional Store workload grows finished history, not node count or actual
// provider sends. Completed real Store traces are copied with all owned identity
// references rewritten. New observations/claims/completions still use Store APIs.
var notificationHistoryTables = []struct{ name, identity string }{
	{"alert_incidents", "id"},
	{"notification_aggregates", "id"},
	{"notification_deliveries", "id"},
	{"delivery_attempts", "delivery_id"},
	{"notification_aggregate_attempts", "aggregate_id"},
	{"notification_aggregate_attempt_members", "aggregate_id"},
	{"backup_runs", "id"},
	{"system_notifications", "id"},
	{"alert_events", "id"},
	{"audit_log", "object_id"},
}

func notificationHistoryMust(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

type notificationHistorySeed struct {
	s                             *Store
	channel, actor                string
	rule                          AlertRule
	node                          Node
	members                       []aggregateFixtureMember
	at                            time.Time
	incident, delivery, aggregate string
	protectedRun, protectedJob    string
	retryAt                       time.Time
}

func (f *notificationHistorySeed) cycle(t *testing.T, at time.Time) {
	t.Helper()
	ctx := context.Background()
	first := observe(t, f.s, f.rule, f.node, at, true, true)
	f.incident = first.ID
	for phase := range 2 {
		now := at.Add(time.Duration(phase*2) * time.Second)
		if phase == 1 {
			observe(t, f.s, f.rule, f.node, now, true, false)
		}
		job, err := f.s.ClaimDelivery(ctx, now, time.Minute)
		notificationHistoryMust(t, err)
		if job == nil || job.IncidentID != first.ID {
			t.Fatal("fresh single notification missing")
		}
		if phase == 0 {
			f.delivery = job.ID
		}
		notificationHistoryMust(t, f.s.CheckDeliveryLease(ctx, job.ID, job.LeaseToken, now))
		notificationHistoryMust(t, f.s.CompleteDelivery(ctx, job.ID, job.LeaseToken, now.Add(time.Second), DeliveryOutcome{Delivered: true}))
	}
	expectedMembers := map[string]bool{}
	for _, member := range f.members {
		incident := observe(t, f.s, member.rule, member.node, at, true, true)
		expectedMembers[incident.ID] = true
	}
	for phase := range 2 {
		now := at.Add(time.Duration(phase*40+30) * time.Second)
		if phase == 1 {
			for _, member := range f.members {
				observe(t, f.s, member.rule, member.node, at.Add(40*time.Second), true, false)
			}
		}
		g, err := f.s.ClaimAggregate(ctx, now, time.Minute)
		notificationHistoryMust(t, err)
		if g == nil {
			t.Fatal("fresh aggregate missing")
		}
		if phase == 0 {
			f.aggregate = g.ID
		}
		send, err := f.s.PrepareAggregateSend(ctx, g.ID, g.LeaseToken, now)
		notificationHistoryMust(t, err)
		if len(send.Payload) == 0 {
			t.Fatal("aggregate payload not frozen")
		}
		var payload AggregateNotification
		notificationHistoryMust(t, json.Unmarshal(send.Payload, &payload))
		if len(payload.Members) != len(expectedMembers) {
			t.Fatal("fresh aggregate has unexpected membership")
		}
		for _, member := range payload.Members {
			if !expectedMembers[member.IncidentID] {
				t.Fatal("fresh aggregate reclaimed historical membership")
			}
		}
		notificationHistoryMust(t, f.s.CompleteAggregate(ctx, g.ID, g.LeaseToken, now.Add(time.Second), DeliveryOutcome{Delivered: true}))
	}
	run, err := f.s.BeginBackupRun(ctx, "", []string{f.channel}, at)
	notificationHistoryMust(t, err)
	notificationHistoryMust(t, f.s.FinishBackupRun(ctx, run.ID, "failed", "backup_insufficient_space", nil, at))
	job, err := f.s.ClaimSystemNotification(ctx, at)
	notificationHistoryMust(t, err)
	if job == nil || job.RunID != run.ID {
		t.Fatal("fresh system reminder missing")
	}
	ok, err := f.s.AuthorizeSystemNotification(ctx, job.ID, job.LeaseToken, job.ChannelRevision, at)
	notificationHistoryMust(t, err)
	if !ok {
		t.Fatal("fresh system reminder not authorized")
	}
	notificationHistoryMust(t, f.s.CompleteSystemNotification(ctx, job.ID, job.LeaseToken, at.Add(time.Second), DeliveryOutcome{Delivered: true, ChannelRevision: job.ChannelRevision}))
	notificationHistoryMust(t, f.s.LogAudit(ctx, f.actor, "capacity.fixture", "incident", first.ID, "", map[string]any{"incident_id": first.ID, "outcome": "resolved"}))
}

func newNotificationHistorySeed(t *testing.T, s *Store) *notificationHistorySeed {
	t.Helper()
	ctx := context.Background()
	c, err := s.CreateNotificationChannel(ctx, "synthetic history channel", "webhook", "synthetic encrypted placeholder")
	notificationHistoryMust(t, err)
	f := &notificationHistorySeed{s: s, channel: c.ID, at: time.Now().UTC().Add(-48 * time.Hour).Truncate(time.Second)}
	actor, err := s.CreateUser(ctx, "synthetic history operator", "synthetic password hash")
	notificationHistoryMust(t, err)
	f.actor = actor.ID
	nodes := map[string]Node{}
	for n := 0; n < 3; n++ {
		node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: fmt.Sprintf("history-%d", n)})
		notificationHistoryMust(t, err)
		nodes[node.ID] = node
	}
	_, err = s.SaveAlertPolicy(ctx, AlertPolicy{Policy: alertpolicy.Policy{Key: "history", Scope: alertpolicy.Scope{Kind: "all"}, Enabled: true}, Name: "history", ChannelID: c.ID, Kind: "cpu", Config: json.RawMessage(`{"threshold_percent":90,"recovery_seconds":0,"aggregation_seconds":30}`), CooldownSeconds: 300})
	notificationHistoryMust(t, err)
	notificationHistoryMust(t, s.SyncAlertPolicyRules(ctx, f.at))
	rules, err := s.ListAlertRules(ctx)
	notificationHistoryMust(t, err)
	for _, r := range rules {
		f.members = append(f.members, aggregateFixtureMember{rule: r, node: nodes[r.NodeID]})
	}
	if len(f.members) != 3 {
		t.Fatal("expected three aggregate members")
	}
	f.node, _, err = s.CreateNode(ctx, CreateNodeParams{Name: "single history"})
	notificationHistoryMust(t, err)
	f.rule, err = s.CreateAlertRule(ctx, f.node.ID, c.ID, "cpu", json.RawMessage(`{"threshold_percent":90,"recovery_seconds":0}`), 300)
	notificationHistoryMust(t, err)
	f.cycle(t, f.at)
	return f
}

type notificationHistoryTable struct {
	name, identity string
	columns, keys  []string
	rows           [][]any
}

func notificationHistoryReadSeed(t *testing.T, s *Store) ([]notificationHistoryTable, []string) {
	return retainedHistoryReadSeed(t, s, notificationHistoryTables)
}

// Definitions are static test-owned table/column identifiers, never API input.
func retainedHistoryReadSeed(t *testing.T, s *Store, definitions []struct{ name, identity string }) ([]notificationHistoryTable, []string) {
	t.Helper()
	var tables []notificationHistoryTable
	var ids []string
	for _, definition := range definitions {
		table := notificationHistoryTable{name: definition.name, identity: definition.identity}
		rows, err := s.db.Query("PRAGMA table_xinfo(" + table.name + ")")
		notificationHistoryMust(t, err)
		keyOrder := map[int]string{}
		for rows.Next() {
			var index, notNull, pk, hidden int
			var name, kind string
			var defaultValue any
			notificationHistoryMust(t, rows.Scan(&index, &name, &kind, &notNull, &defaultValue, &pk, &hidden))
			if pk > 0 {
				keyOrder[pk] = name
			}
			if hidden == 0 && name != "seq" && !(name == "id" && kind == "INTEGER") {
				table.columns = append(table.columns, name)
			}
		}
		notificationHistoryMust(t, rows.Err())
		notificationHistoryMust(t, rows.Close())
		for n := 1; n <= len(keyOrder); n++ {
			table.keys = append(table.keys, keyOrder[n])
		}
		rows, err = s.db.Query("SELECT " + strings.Join(table.columns, ",") + " FROM " + table.name + " ORDER BY " + strings.Join(table.keys, ","))
		notificationHistoryMust(t, err)
		for rows.Next() {
			values := make([]any, len(table.columns))
			dest := make([]any, len(values))
			for n := range values {
				dest[n] = &values[n]
			}
			notificationHistoryMust(t, rows.Scan(dest...))
			table.rows = append(table.rows, values)
			for n, column := range table.columns {
				if column == "id" && table.identity == "id" {
					ids = append(ids, values[n].(string))
				}
			}
		}
		notificationHistoryMust(t, rows.Err())
		notificationHistoryMust(t, rows.Close())
		if len(table.rows) == 0 {
			t.Fatalf("real trace missing %s", table.name)
		}
		tables = append(tables, table)
	}
	return tables, ids
}

func notificationHistoryIDs(ids []string, copyNumber int) map[string]string {
	result := map[string]string{}
	for n, id := range ids {
		result[id] = fmt.Sprintf("capacity-%023x", copyNumber*len(ids)+n)
	}
	return result
}

func notificationHistoryGrow(t *testing.T, s *Store, tables []notificationHistoryTable, ids []string, first, last int) {
	t.Helper()
	tx, err := s.db.Begin()
	notificationHistoryMust(t, err)
	defer tx.Rollback()
	statements := make([]*sql.Stmt, len(tables))
	for n, table := range tables {
		marks := strings.TrimSuffix(strings.Repeat("?,", len(table.columns)), ",")
		statements[n], err = tx.Prepare("INSERT INTO " + table.name + "(" + strings.Join(table.columns, ",") + ") VALUES(" + marks + ")")
		notificationHistoryMust(t, err)
		defer statements[n].Close()
	}
	for copyNumber := first; copyNumber <= last; copyNumber++ {
		mapping := notificationHistoryIDs(ids, copyNumber)
		replacements := make([]string, 0, len(mapping)*2)
		for _, id := range ids {
			replacements = append(replacements, id, mapping[id])
		}
		replace := strings.NewReplacer(replacements...)
		for n, table := range tables {
			// Backup task history is explicitly bounded to the latest1000 rows.
			// Keep the original completed seed plus at most999 completed copies;
			// the separate oldest pending reminder exercises pruning protection.
			if copyNumber > 999 && (table.name == "backup_runs" || table.name == "system_notifications") {
				continue
			}
			for _, row := range table.rows {
				values := slices.Clone(row)
				for k, value := range values {
					if text, ok := value.(string); ok {
						values[k] = replace.Replace(text)
						if table.columns[k] == "idempotency_key" {
							values[k] = values[k].(string) + fmt.Sprintf(":capacity:%d", copyNumber)
						}
					}
				}
				_, err = statements[n].Exec(values...)
				notificationHistoryMust(t, err)
			}
		}
	}
	notificationHistoryMust(t, tx.Commit())
}

type notificationHistoryDigest struct {
	Rows   int    `json:"rows"`
	SHA256 string `json:"sha256"`
}

func notificationHistoryDigests(t *testing.T, s *Store, tables []notificationHistoryTable) map[string]notificationHistoryDigest {
	return retainedHistoryDigests(t, s, tables, true)
}

func retainedHistoryDigests(t *testing.T, s *Store, tables []notificationHistoryTable, copiesOnly bool) map[string]notificationHistoryDigest {
	t.Helper()
	result := map[string]notificationHistoryDigest{}
	for _, table := range tables {
		where := ""
		if copiesOnly {
			where = " WHERE " + table.identity + " LIKE 'capacity-%'"
		}
		rows, err := s.db.Query("SELECT * FROM " + table.name + where + " ORDER BY " + strings.Join(table.keys, ","))
		notificationHistoryMust(t, err)
		columns, err := rows.Columns()
		notificationHistoryMust(t, err)
		hash := sha256.New()
		encoder := json.NewEncoder(hash)
		notificationHistoryMust(t, encoder.Encode(columns))
		count := 0
		for rows.Next() {
			values := make([]any, len(columns))
			dest := make([]any, len(columns))
			for n := range values {
				dest[n] = &values[n]
			}
			notificationHistoryMust(t, rows.Scan(dest...))
			notificationHistoryMust(t, encoder.Encode(values))
			count++
		}
		notificationHistoryMust(t, rows.Err())
		notificationHistoryMust(t, rows.Close())
		result[table.name] = notificationHistoryDigest{count, hex.EncodeToString(hash.Sum(nil))}
	}
	return result
}

func notificationHistoryPermanentOnly(values map[string]notificationHistoryDigest) map[string]notificationHistoryDigest {
	result := make(map[string]notificationHistoryDigest)
	for name, value := range values {
		if name != "backup_runs" && name != "system_notifications" {
			result[name] = value
		}
	}
	return result
}

func notificationHistoryProtected(t *testing.T, s *Store, f *notificationHistorySeed) map[string]string {
	t.Helper()
	result := map[string]string{}
	for table, id := range map[string]string{"backup_runs": f.protectedRun, "system_notifications": f.protectedJob} {
		rows, err := s.db.Query("SELECT * FROM "+table+" WHERE id=?", id)
		notificationHistoryMust(t, err)
		columns, err := rows.Columns()
		notificationHistoryMust(t, err)
		if !rows.Next() {
			rows.Close()
			t.Fatal("old pending backup reminder was pruned")
		}
		values := make([]any, len(columns))
		dest := make([]any, len(values))
		for n := range values {
			dest[n] = &values[n]
		}
		notificationHistoryMust(t, rows.Scan(dest...))
		raw, err := json.Marshal(values)
		notificationHistoryMust(t, err)
		digest := sha256.Sum256(raw)
		result[table] = hex.EncodeToString(digest[:])
		if rows.Next() {
			rows.Close()
			t.Fatal("duplicate protected history identity")
		}
		notificationHistoryMust(t, rows.Err())
		notificationHistoryMust(t, rows.Close())
	}
	return result
}

func notificationHistoryKeepPending(t *testing.T, f *notificationHistorySeed) {
	t.Helper()
	ctx := context.Background()
	at := f.at.Add(80 * time.Second)
	run, err := f.s.BeginBackupRun(ctx, "", []string{f.channel}, at)
	notificationHistoryMust(t, err)
	notificationHistoryMust(t, f.s.FinishBackupRun(ctx, run.ID, "failed", "backup_insufficient_space", nil, at))
	job, err := f.s.ClaimSystemNotification(ctx, at)
	notificationHistoryMust(t, err)
	if job == nil || job.RunID != run.ID {
		t.Fatal("protected reminder missing")
	}
	allowed, err := f.s.AuthorizeSystemNotification(ctx, job.ID, job.LeaseToken, job.ChannelRevision, at)
	notificationHistoryMust(t, err)
	if !allowed {
		t.Fatal("protected reminder was not authorized")
	}
	f.retryAt = time.Now().UTC().Add(24 * time.Hour)
	notificationHistoryMust(t, f.s.CompleteSystemNotification(ctx, job.ID, job.LeaseToken, at.Add(time.Second), DeliveryOutcome{ErrorClass: "http_server_error", RetryAt: f.retryAt, ChannelRevision: job.ChannelRevision}))
	f.protectedRun, f.protectedJob = run.ID, job.ID
}

func notificationHistoryMetrics(t *testing.T, f *notificationHistorySeed, ids []string, copies int) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	mapping := notificationHistoryIDs(ids, copies/2)
	samples := map[string][]float64{}
	var writer sync.WaitGroup
	writer.Add(1)
	writes := make(chan map[string]any, 1)
	go func() {
		defer writer.Done()
		var attempts, successful, interrupted int
		var failure string
		started := time.Now()
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				writes <- map[string]any{"attempts": attempts, "successful": successful, "interrupted_at_shutdown": interrupted, "error": failure, "seconds": time.Since(started).Seconds()}
				return
			case at := <-ticker.C:
				attempts++
				err := f.s.SaveReport(ctx, f.node.ID, protocol.Report{CapturedAt: at.UTC(), CPU: protocol.CPUMetric{UsagePercent: 23}, Networks: []protocol.NetworkMetric{{Interface: "synthetic", RXTotalBytes: uint64(attempts * 100), TXTotalBytes: uint64(attempts * 200)}}})
				if err != nil && ctx.Err() == nil {
					failure = err.Error()
				} else if err == nil {
					successful++
				} else {
					interrupted++
				}
			}
		}
	}()
	defer func() { cancel(); writer.Wait() }()
	started := time.Now()
	for round := 0; round < 100 || time.Since(started) < 3*time.Second; round++ {
		measure := func(name string, fn func() error) {
			began := time.Now()
			notificationHistoryMust(t, fn())
			samples[name] = append(samples[name], time.Since(began).Seconds()*1000)
		}
		measure("incident_page", func() error {
			items, err := f.s.ListIncidents(ctx, IncidentFilter{Limit: 50, State: "resolved", NodeID: f.node.ID})
			if err == nil && len(items) != 50 {
				return fmt.Errorf("incident page size %d", len(items))
			}
			return err
		})
		measure("incident_deliveries", func() error {
			items, err := f.s.ListIncidentDeliveries(ctx, mapping[f.incident], 0, 100)
			if err == nil && (len(items) != 2 || items[0].Status != "delivered" || items[1].Status != "delivered") {
				return fmt.Errorf("finished single history changed")
			}
			return err
		})
		measure("attempt_page", func() error {
			page, err := f.s.ListDeliveryAttemptPage(ctx, mapping[f.delivery], 0, 100)
			if err == nil && (len(page.Attempts) != 1 || page.Attempts[0].Outcome != "delivered") {
				return fmt.Errorf("finished attempt history changed")
			}
			return err
		})
		measure("aggregate_page", func() error {
			page, err := f.s.ListNotificationAggregates(ctx, 0, 50)
			if err == nil && len(page.Aggregates) != 50 {
				return fmt.Errorf("aggregate page size %d", len(page.Aggregates))
			}
			return err
		})
		measure("aggregate_context", func() error {
			page, err := f.s.AggregateContext(ctx, mapping[f.aggregate], 0, 100)
			if err == nil && (len(page.Members) != 3 || len(page.Attempts) != 1 || len(page.Attempts[0].Members) != 3 || page.Totals.Delivered != 3) {
				return fmt.Errorf("aggregate membership/history changed")
			}
			return err
		})
		measure("queue_diagnostics", func() error {
			q := f.s.NotificationQueueDiagnostics(ctx, time.Now())
			if q.Status != "available" || q.Deliveries.Pending != 0 || q.Deliveries.Inflight != 0 || q.Aggregates.Pending != 0 || q.Aggregates.Inflight != 0 || q.System.Pending != 1 || q.System.PendingFuture != 1 || q.System.PendingDueByTime != 0 || q.System.Inflight != 0 {
				return fmt.Errorf("finished history became queued or unavailable")
			}
			return nil
		})
		measure("idle_claims", func() error {
			at := time.Now().UTC()
			d, err := f.s.ClaimDelivery(ctx, at, time.Minute)
			if err != nil {
				return err
			}
			g, err := f.s.ClaimAggregate(ctx, at, time.Minute)
			if err != nil {
				return err
			}
			n, err := f.s.ClaimSystemNotification(ctx, at)
			if err != nil {
				return err
			}
			if d != nil || g != nil || n != nil {
				return fmt.Errorf("finished history reclaimed")
			}
			return nil
		})
		measure("audit_page", func() error {
			items, err := f.s.ListAudit(ctx, 50, 0)
			if err == nil && len(items) != 50 {
				return fmt.Errorf("audit page size %d", len(items))
			}
			return err
		})
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
	}
	cancel()
	writer.Wait()
	write := <-writes
	if write["error"] != "" || write["successful"].(int) == 0 || write["attempts"].(int) != write["successful"].(int)+write["interrupted_at_shutdown"].(int) {
		t.Fatalf("concurrent reports: %+v", write)
	}
	metrics := map[string]any{"reports": write}
	for name, values := range samples {
		slices.Sort(values)
		metrics[name] = map[string]any{"samples": len(values), "p50_ms": values[(len(values)-1)/2], "p95_ms": values[(len(values)*95+99)/100-1], "max_ms": values[len(values)-1]}
	}
	return metrics
}

func TestNotificationPermanentHistoryCapacity(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_NOTIFICATION_HISTORY_CAPACITY") != "1" {
		t.Skip("explicit permanent notification history capacity workload")
	}
	maximum := 100000
	if text := os.Getenv("MYPROBE_TEST_NOTIFICATION_HISTORY_MAX"); text != "" {
		value, err := strconv.Atoi(text)
		if err != nil || (value != 1000 && value != 10000 && value != 100000) {
			t.Fatal("maximum must be1000,10000 or100000")
		}
		maximum = value
	}
	base := os.Getenv("MYPROBE_TEST_NOTIFICATION_HISTORY_OUTPUT")
	if base == "" {
		t.Fatal("absolute existing output directory is required")
	}
	if !filepath.IsAbs(base) {
		t.Fatal("output directory must be absolute")
	}
	directory, err := os.MkdirTemp(base, "notification-history-")
	notificationHistoryMust(t, err)
	s, err := Open(context.Background(), filepath.Join(directory, "history.db"))
	notificationHistoryMust(t, err)
	defer func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
		if !t.Failed() {
			resolved, err := filepath.EvalSymlinks(directory)
			notificationHistoryMust(t, err)
			resolvedBase, err := filepath.EvalSymlinks(base)
			notificationHistoryMust(t, err)
			relative, err := filepath.Rel(resolvedBase, resolved)
			notificationHistoryMust(t, err)
			if relative == "." || strings.HasPrefix(relative, "..") || filepath.IsAbs(relative) || !strings.HasPrefix(filepath.Base(resolved), "notification-history-") {
				t.Fatal("refuse cleanup outside the owned fixture directory")
			}
			notificationHistoryMust(t, os.RemoveAll(directory))
		} else {
			t.Log("failed synthetic fixture retained in explicit private output directory")
		}
	}()
	f := newNotificationHistorySeed(t, s)
	tables, ids := notificationHistoryReadSeed(t, s)
	notificationHistoryKeepPending(t, f)
	protected := notificationHistoryProtected(t, s, f)
	seedIncidents := len(tables[0].rows)
	if seedIncidents != 4 {
		t.Fatalf("seed incidents=%d", seedIncidents)
	}
	var results []map[string]any
	previous := 0
	for _, target := range []int{1000, 10000, 100000} {
		if target > maximum {
			break
		}
		copies := target / seedIncidents
		began := time.Now()
		notificationHistoryGrow(t, s, tables, ids, previous+1, copies)
		seedSeconds := time.Since(began).Seconds()
		previous = copies
		before := notificationHistoryDigests(t, s, tables)
		expected := map[string]int{"alert_incidents": target, "notification_deliveries": 2 * target, "delivery_attempts": 2 * target, "notification_aggregates": target / 2, "notification_aggregate_attempts": target / 2, "notification_aggregate_attempt_members": 3 * target / 2, "alert_events": 2 * target, "audit_log": target / 4, "backup_runs": min(copies, 999), "system_notifications": min(copies, 999)}
		for table, count := range expected {
			if before[table].Rows != count {
				t.Fatalf("%s rows=%d, want%d at%d incidents", table, before[table].Rows, count, target)
			}
		}
		metrics := notificationHistoryMetrics(t, f, ids, copies)
		if after := notificationHistoryDigests(t, s, tables); !reflect.DeepEqual(before, after) {
			t.Fatal("reads/idle claims/concurrent reports mutated old history")
		}
		info, err := os.Stat(s.path)
		notificationHistoryMust(t, err)
		var pages, pageSize int64
		notificationHistoryMust(t, s.db.QueryRow("PRAGMA page_count").Scan(&pages))
		notificationHistoryMust(t, s.db.QueryRow("PRAGMA page_size").Scan(&pageSize))
		var walBytes int64
		if wal, err := os.Stat(s.path + "-wal"); err == nil {
			walBytes = wal.Size()
		} else if !os.IsNotExist(err) {
			t.Fatal(err)
		}
		results = append(results, map[string]any{"historical_incidents": target, "seed_seconds": seedSeconds, "database_bytes": info.Size(), "sqlite_allocated_bytes": pages * pageSize, "wal_after_bytes_not_peak": walBytes, "historical_records": before, "reads_with_concurrent_reports": metrics})
	}
	before := notificationHistoryDigests(t, s, tables)
	f.cycle(t, f.at.Add(24*time.Hour))
	afterCycle := notificationHistoryDigests(t, s, tables)
	if !reflect.DeepEqual(notificationHistoryPermanentOnly(before), notificationHistoryPermanentOnly(afterCycle)) {
		for name, old := range before {
			if old != afterCycle[name] {
				t.Logf("changed historical table %s: before=%+v after=%+v", name, old, afterCycle[name])
			}
		}
		t.Fatal("fresh notification lifecycle mutated old history")
	}
	if !reflect.DeepEqual(protected, notificationHistoryProtected(t, s, f)) {
		t.Fatal("new backup pruned or rewrote an old pending reminder")
	}
	var backupCount int
	notificationHistoryMust(t, s.db.QueryRow("SELECT count(*) FROM backup_runs").Scan(&backupCount))
	if backupCount > 1001 {
		t.Fatal("terminal backup task history exceeded its explicit bound plus protected work")
	}
	before = afterCycle
	for n := 0; n < 1000; n++ {
		batch, err := s.RetainHistory(context.Background(), time.Now().UTC().Add(400*24*time.Hour), DefaultRetentionPolicy())
		notificationHistoryMust(t, err)
		if !batch.More {
			break
		}
		if n == 999 {
			t.Fatal("normal retention did not drain")
		}
	}
	if !reflect.DeepEqual(before, notificationHistoryDigests(t, s, tables)) {
		t.Fatal("metric retention deleted or changed permanent notification history")
	}
	backup := filepath.Join(directory, "snapshot.db")
	notificationHistoryMust(t, s.ConsistentBackup(context.Background(), backup))
	notificationHistoryMust(t, ValidateDatabaseFile(context.Background(), backup))
	restored, err := Open(context.Background(), backup)
	notificationHistoryMust(t, err)
	defer func() { notificationHistoryMust(t, restored.Close()) }()
	if !reflect.DeepEqual(before, notificationHistoryDigests(t, restored, tables)) {
		t.Fatal("consistent backup/reopen changed old identities, payloads or exact attempt membership")
	}
	var integrity string
	notificationHistoryMust(t, restored.db.QueryRow("PRAGMA integrity_check").Scan(&integrity))
	if integrity != "ok" {
		t.Fatal("restored integrity failed")
	}
	rows, err := restored.db.Query("PRAGMA foreign_key_check")
	notificationHistoryMust(t, err)
	if rows.Next() {
		rows.Close()
		t.Fatal("restored foreign key violation")
	}
	notificationHistoryMust(t, rows.Err())
	notificationHistoryMust(t, rows.Close())
	f.s = restored
	f.cycle(t, f.at.Add(36*time.Hour))
	if !reflect.DeepEqual(notificationHistoryPermanentOnly(before), notificationHistoryPermanentOnly(notificationHistoryDigests(t, restored, tables))) {
		t.Fatal("fresh post-restore notification lifecycle changed original history")
	}
	if !reflect.DeepEqual(protected, notificationHistoryProtected(t, restored, f)) {
		t.Fatal("old pending reminder did not survive pruning and restore")
	}
	job, err := restored.ClaimSystemNotification(context.Background(), f.retryAt.Add(time.Second))
	notificationHistoryMust(t, err)
	if job == nil || job.ID != f.protectedJob || job.RunID != f.protectedRun || job.AttemptCount != 2 {
		t.Fatal("restored original reminder did not retry exactly once")
	}
	allowed, err := restored.AuthorizeSystemNotification(context.Background(), job.ID, job.LeaseToken, job.ChannelRevision, f.retryAt.Add(time.Second))
	notificationHistoryMust(t, err)
	if !allowed {
		t.Fatal("restored original reminder not authorized")
	}
	notificationHistoryMust(t, restored.CompleteSystemNotification(context.Background(), job.ID, job.LeaseToken, f.retryAt.Add(2*time.Second), DeliveryOutcome{Delivered: true, ChannelRevision: job.ChannelRevision}))
	again, err := restored.ClaimSystemNotification(context.Background(), f.retryAt.Add(3*time.Second))
	notificationHistoryMust(t, err)
	if again != nil {
		t.Fatal("completed restored reminder was claimed again")
	}
	result := map[string]any{"format": "myprobe.notification-history-capacity.v1", "passed": true, "maximum_historical_incidents": maximum, "actual_nodes": 4, "levels": results, "fresh_single_aggregate_system_cycles": true, "metric_retention_400days_preserves_history": true, "consistent_backup_restores_exact_history": true, "integrity_and_foreign_keys": true, "backup_run_history_limit": 1000, "backup_run_pruning_exercised": maximum >= 10000, "protected_pending_reminder_preserved_and_retried_after_restore": true, "scope": "Store component only. Real Store seed/lifecycle calls with simulated transport outcomes; identity-rewritten copies grow permanent finished incident history. Backup runs/system reminders retain their explicit1000-task policy and protect pending work. No actual Provider/HTTP/Agent/browser,50-node resource SLO, continuous24h, physical2GiB or encrypted-application restore claim."}
	raw, err := json.MarshalIndent(result, "", "  ")
	notificationHistoryMust(t, err)
	file, err := os.OpenFile(filepath.Join(base, "notification-history-capacity.json"), os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	notificationHistoryMust(t, err)
	_, err = file.Write(append(raw, '\n'))
	notificationHistoryMust(t, err)
	notificationHistoryMust(t, file.Sync())
	notificationHistoryMust(t, file.Close())
}

package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

var incidentActionTables = []struct{ name, identity string }{
	{"alert_incidents", "id"}, {"notification_deliveries", "id"},
	{"incident_acknowledgements", "incident_id"}, {"incident_silences", "id"},
	{"incident_silence_requests", "silence_id"},
	{"incident_silence_decisions", "silence_id"}, {"delivery_silence_links", "silence_id"},
	{"audit_log", "object_id"},
}

type incidentActionSeed struct {
	actor, incident, silence     string
	ackKey, createKey, cancelKey string
	ack                          AcknowledgeIncidentResult
	create, cancel               SilenceMutationResult
	params                       CreateIncidentSilenceParams
	at                           time.Time
	node                         Node
	rule                         AlertRule
}

func newIncidentActionSeed(t *testing.T, s *Store) incidentActionSeed {
	t.Helper()
	ctx := context.Background()
	actor, err := s.CreateUser(ctx, "synthetic action operator", "synthetic hash")
	notificationHistoryMust(t, err)
	node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "action history"})
	notificationHistoryMust(t, err)
	channel, err := s.CreateNotificationChannel(ctx, "synthetic channel", "webhook", "synthetic placeholder")
	notificationHistoryMust(t, err)
	rule, err := s.CreateAlertRule(ctx, node.ID, channel.ID, "cpu", json.RawMessage(`{"threshold_percent":90,"recovery_seconds":0}`), 300)
	notificationHistoryMust(t, err)
	f := incidentActionSeed{actor: actor.ID, node: node, rule: rule, at: time.Now().UTC().Truncate(time.Second), ackKey: "action-ack", createKey: "action-create", cancelKey: "action-cancel", params: CreateIncidentSilenceParams{DurationSeconds: 60, Reason: "synthetic investigation"}}
	i := observe(t, s, rule, node, f.at, true, true)
	f.incident = i.ID
	f.ack, err = s.AcknowledgeIncident(ctx, i.ID, f.ackKey, "synthetic note", actor.ID)
	notificationHistoryMust(t, err)
	// Acknowledge uses the real wall clock; silence must follow that mutation.
	f.at = time.Now().UTC().Add(time.Second).Truncate(time.Millisecond)
	f.create, err = s.CreateIncidentSilence(ctx, i.ID, f.createKey, actor.ID, f.params, f.at)
	notificationHistoryMust(t, err)
	f.silence = f.create.SilenceID
	f.cancel, err = s.CancelIncidentSilence(ctx, i.ID, f.silence, f.cancelKey, actor.ID, 1, f.at.Add(time.Second))
	notificationHistoryMust(t, err)
	i = observe(t, s, rule, node, f.at.Add(2*time.Second), true, false)
	if i.State != "resolved" {
		t.Fatal("seed did not resolve")
	}
	// Its firing notification was canceled before sending, so recovery must not
	// invent a notification for a fault the recipient never received.
	job, err := s.ClaimDelivery(ctx, f.at.Add(3*time.Second), time.Minute)
	notificationHistoryMust(t, err)
	if job != nil {
		t.Fatal("suppressed seed unexpectedly queued a recovery")
	}
	return f
}

// Copies contain valid replay digests for their rewritten targets. Seed and new
// mutations use Store APIs; copying is only a deterministic historical fixture.
func growIncidentActionHistory(t *testing.T, s *Store, f incidentActionSeed, tables []notificationHistoryTable, ids []string, first, last int) {
	t.Helper()
	tx, err := s.db.Begin()
	notificationHistoryMust(t, err)
	defer tx.Rollback()
	statements := make([]*sql.Stmt, len(tables))
	for n, table := range tables {
		statements[n], err = tx.Prepare("INSERT INTO " + table.name + "(" + strings.Join(table.columns, ",") + ") VALUES(" + strings.TrimSuffix(strings.Repeat("?,", len(table.columns)), ",") + ")")
		notificationHistoryMust(t, err)
		defer statements[n].Close()
	}
	for copyNumber := first; copyNumber <= last; copyNumber++ {
		mapping := notificationHistoryIDs(ids, copyNumber)
		var replacements []string
		for _, id := range ids {
			replacements = append(replacements, id, mapping[id])
		}
		replace := strings.NewReplacer(replacements...)
		for n, table := range tables {
			for _, row := range table.rows {
				values := slices.Clone(row)
				for k, value := range values {
					if value, ok := value.(string); ok {
						values[k] = replace.Replace(value)
					}
				}
				for k, column := range table.columns {
					if column != "request_digest" {
						continue
					}
					switch table.name {
					case "incident_acknowledgements":
						values[k] = acknowledgementDigest(mapping[f.incident], f.actor, f.ack.Acknowledgement.Note)
					case "incident_silence_requests":
						if row[slices.Index(table.columns, "request_id")] == f.createKey {
							values[k] = silenceRequestDigest(struct {
								Operation, IncidentID, Actor string
								Params                       CreateIncidentSilenceParams
							}{"create", mapping[f.incident], f.actor, f.params})
						} else {
							values[k] = silenceRequestDigest(struct {
								Operation, IncidentID, SilenceID, Actor string
								Revision                                int64
							}{"cancel", mapping[f.incident], mapping[f.silence], f.actor, 1})
						}
					}
				}
				_, err := statements[n].Exec(values...)
				notificationHistoryMust(t, err)
			}
		}
	}
	notificationHistoryMust(t, tx.Commit())
}

func replayIncidentActionHistory(t *testing.T, s *Store, f incidentActionSeed, ids []string, copyNumber int) {
	t.Helper()
	m := notificationHistoryIDs(ids, copyNumber)
	ctx := context.Background()
	ack, err := s.AcknowledgeIncident(ctx, m[f.incident], m[f.ackKey], f.ack.Acknowledgement.Note, f.actor)
	notificationHistoryMust(t, err)
	expectedAck := f.ack
	expectedAck.Replayed, expectedAck.Acknowledgement.IncidentID = true, m[f.incident]
	if !reflect.DeepEqual(ack, expectedAck) {
		t.Fatal("old acknowledgement result changed")
	}
	created, err := s.CreateIncidentSilence(ctx, m[f.incident], m[f.createKey], f.actor, f.params, f.at.Add(400*24*time.Hour))
	notificationHistoryMust(t, err)
	expected := f.create
	expected.Replayed, expected.SilenceID = true, m[f.silence]
	if !reflect.DeepEqual(created, expected) {
		t.Fatal("old creation result changed")
	}
	canceled, err := s.CancelIncidentSilence(ctx, m[f.incident], m[f.silence], m[f.cancelKey], f.actor, 1, f.at.Add(400*24*time.Hour))
	notificationHistoryMust(t, err)
	expected = f.cancel
	expected.Replayed, expected.SilenceID = true, m[f.silence]
	if !reflect.DeepEqual(canceled, expected) {
		t.Fatal("old cancellation result changed")
	}
	_, err = s.AcknowledgeIncident(ctx, m[f.incident], m[f.ackKey], "changed", f.actor)
	if !errors.Is(err, ErrAcknowledgementRequestConflict) {
		t.Fatal("changed ACK replay accepted", err)
	}
	params := f.params
	params.DurationSeconds++
	_, err = s.CreateIncidentSilence(ctx, m[f.incident], m[f.createKey], f.actor, params, f.at)
	if !errors.Is(err, ErrSilenceRequestConflict) {
		t.Fatal("changed silence replay accepted", err)
	}
	_, err = s.CancelIncidentSilence(ctx, m[f.incident], m[f.silence], m[f.cancelKey], f.actor, 2, f.at)
	if !errors.Is(err, ErrSilenceRequestConflict) {
		t.Fatal("changed cancel replay accepted", err)
	}
}

func measureIncidentActionHistory(t *testing.T, s *Store, f incidentActionSeed, ids []string, copies int) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	var wg sync.WaitGroup
	var attempts, successful, interrupted int
	var writeError error
	wg.Add(1)
	go func() {
		defer wg.Done()
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case at := <-ticker.C:
				attempts++
				err := s.SaveReport(ctx, f.node.ID, protocol.Report{CapturedAt: at.UTC(), CPU: protocol.CPUMetric{UsagePercent: 20}})
				if err == nil {
					successful++
				} else if ctx.Err() != nil {
					interrupted++
				} else {
					writeError = err
					return
				}
			}
		}
	}()
	defer func() { cancel(); wg.Wait() }()
	samples := map[string][]float64{}
	m := notificationHistoryIDs(ids, copies/2)
	started := time.Now()
	for round := 0; round < 100 || time.Since(started) < 3*time.Second; round++ {
		measure := func(name string, fn func() error) {
			at := time.Now()
			notificationHistoryMust(t, fn())
			samples[name] = append(samples[name], time.Since(at).Seconds()*1000)
		}
		measure("acknowledgement", func() error {
			v, err := s.IncidentAcknowledgement(ctx, m[f.incident])
			if err == nil && (v.IncidentState != "resolved" || v.Acknowledgement == nil || v.Acknowledgement.Note != f.ack.Acknowledgement.Note) {
				return fmt.Errorf("acknowledgement context changed")
			}
			return err
		})
		measure("silence_page", func() error {
			v, err := s.ListIncidentSilences(ctx, m[f.incident], "", 100, f.at.Add(time.Hour))
			if err == nil && (len(v.Silences) != 1 || v.ActiveSilence != nil || v.Silences[0].Revision != 2 || v.Silences[0].CanceledAt == nil || len(v.Silences[0].Decisions) == 0) {
				return fmt.Errorf("silence context changed")
			}
			return err
		})
		measure("delivery_silence_links", func() error {
			var id string
			if err := s.db.QueryRowContext(ctx, `SELECT delivery_id FROM delivery_silence_links WHERE silence_id=?`, m[f.silence]).Scan(&id); err != nil {
				return err
			}
			v, err := s.ListDeliverySilences(ctx, id, "", 100, f.at.Add(time.Hour))
			if err == nil && (len(v.Silences) != 1 || v.Silences[0].RecordedAt == nil || v.Silences[0].ID != m[f.silence]) {
				return fmt.Errorf("delivery link changed")
			}
			return err
		})
		measure("old_replays_and_conflicts", func() error { replayIncidentActionHistory(t, s, f, ids, copies/2); return nil })
		if ctx.Err() != nil {
			t.Fatal(ctx.Err())
		}
	}
	cancel()
	wg.Wait()
	if writeError != nil || successful == 0 || attempts != successful+interrupted {
		t.Fatalf("concurrent reports: %d/%d/%d %v", attempts, successful, interrupted, writeError)
	}
	result := map[string]any{"reports": map[string]any{"attempts": attempts, "successful": successful, "interrupted_at_shutdown": interrupted, "error": "", "seconds": time.Since(started).Seconds()}}
	for name, values := range samples {
		slices.Sort(values)
		result[name] = map[string]any{"samples": len(values), "p50_ms": values[(len(values)-1)/2], "p95_ms": values[(len(values)*95+99)/100-1], "max_ms": values[len(values)-1]}
	}
	return result
}

type maintenanceHistorySeed struct {
	preview  MaintenancePreview
	first    MaintenanceCreateResult
	canceled MaintenanceCancelResult
}

func fillMaintenanceHistory(t *testing.T, s *Store, f incidentActionSeed) maintenanceHistorySeed {
	t.Helper()
	ctx := context.Background()
	p, err := s.PreviewMaintenanceWindow(ctx, MaintenanceDraft{Selection: MaintenanceSelection{Kind: "nodes", NodeIDs: []string{f.node.ID}}, Reason: "synthetic maintenance", Timezone: "UTC", StartsAt: f.at.Add(time.Hour), EndsAt: f.at.Add(2 * time.Hour)})
	notificationHistoryMust(t, err)
	result := maintenanceHistorySeed{preview: p}
	for n := range 1000 {
		created, err := s.CreateConfirmedMaintenance(ctx, p.Draft, p.Digest, fmt.Sprintf("maintenance-history-%d", n), f.actor)
		notificationHistoryMust(t, err)
		canceled, err := s.CancelConfirmedMaintenance(ctx, created.WindowID, 1, f.actor, f.at.Add(time.Second))
		notificationHistoryMust(t, err)
		if n == 0 {
			result.first, result.canceled = created, canceled
		}
	}
	checkMaintenanceHistory(t, s, f, result)
	return result
}

func checkMaintenanceHistory(t *testing.T, s *Store, f incidentActionSeed, original maintenanceHistorySeed) {
	t.Helper()
	ctx := context.Background()
	created, err := s.CreateConfirmedMaintenance(ctx, original.preview.Draft, original.preview.Digest, "maintenance-history-0", f.actor)
	notificationHistoryMust(t, err)
	if !created.Replayed || created.WindowID != original.first.WindowID {
		t.Fatal("full maintenance history lost create replay")
	}
	canceled, err := s.CancelConfirmedMaintenance(ctx, created.WindowID, 1, f.actor, f.at.Add(400*24*time.Hour))
	notificationHistoryMust(t, err)
	expected := original.canceled
	expected.Replayed = true
	if !reflect.DeepEqual(canceled, expected) {
		t.Fatal("full maintenance history lost cancellation replay")
	}
	_, err = s.CreateConfirmedMaintenance(ctx, original.preview.Draft, original.preview.Digest, "maintenance-over-limit", f.actor)
	if !errors.Is(err, ErrMaintenanceLimit) {
		t.Fatal("1001st maintenance window accepted", err)
	}
	draft := original.preview.Draft
	draft.Reason = "changed"
	_, err = s.CreateConfirmedMaintenance(ctx, draft, original.preview.Digest, "maintenance-history-0", f.actor)
	if !errors.Is(err, ErrMaintenanceRequestConflict) {
		t.Fatal("changed maintenance replay accepted", err)
	}
	seen, cursor := map[string]bool{}, ""
	for {
		items, next, err := s.ListMaintenanceWindows(ctx, cursor, 100)
		notificationHistoryMust(t, err)
		for _, item := range items {
			if seen[item.ID] || item.CanceledAt == nil || item.Selection == nil || item.CanceledBy != f.actor || item.Revision != 2 || !slices.Equal(item.NodeIDs, []string{f.node.ID}) {
				t.Fatal("maintenance pagination/history changed")
			}
			seen[item.ID] = true
		}
		if next == "" {
			break
		}
		if next <= cursor {
			t.Fatal("maintenance cursor did not advance")
		}
		cursor = next
	}
	if len(seen) != 1000 {
		t.Fatalf("maintenance pagination returned %d windows", len(seen))
	}
}

func TestIncidentActionHistoryCapacity(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_INCIDENT_ACTION_HISTORY") != "1" {
		t.Skip("explicit incident action history workload")
	}
	maximum := 100000
	if raw := os.Getenv("MYPROBE_TEST_INCIDENT_ACTION_MAX"); raw != "" {
		value, err := strconv.Atoi(raw)
		notificationHistoryMust(t, err)
		if value != 1000 && value != 10000 && value != 100000 {
			t.Fatal("maximum must be1000,10000 or100000")
		}
		maximum = value
	}
	base := os.Getenv("MYPROBE_TEST_INCIDENT_ACTION_OUTPUT")
	if !filepath.IsAbs(base) {
		t.Fatal("absolute existing output directory required")
	}
	directory, err := os.MkdirTemp(base, "incident-actions-")
	notificationHistoryMust(t, err)
	s, err := Open(context.Background(), filepath.Join(directory, "history.db"))
	notificationHistoryMust(t, err)
	defer func() {
		notificationHistoryMust(t, s.Close())
		if t.Failed() {
			t.Log("failed synthetic fixture retained in explicit private output directory")
			return
		}
		resolved, err := filepath.EvalSymlinks(directory)
		notificationHistoryMust(t, err)
		parent, err := filepath.EvalSymlinks(base)
		notificationHistoryMust(t, err)
		relative, err := filepath.Rel(parent, resolved)
		notificationHistoryMust(t, err)
		if relative == "." || strings.HasPrefix(relative, "..") || filepath.IsAbs(relative) || !strings.HasPrefix(filepath.Base(resolved), "incident-actions-") {
			t.Fatal("refuse cleanup outside owned fixture")
		}
		notificationHistoryMust(t, os.RemoveAll(resolved))
	}()
	f := newIncidentActionSeed(t, s)
	tables, ids := retainedHistoryReadSeed(t, s, incidentActionTables)
	ids = append(ids, f.ackKey, f.createKey, f.cancelKey)
	maintenance := fillMaintenanceHistory(t, s, f)
	maintenanceTables, _ := retainedHistoryReadSeed(t, s, []struct{ name, identity string }{
		{"maintenance_windows", "id"}, {"maintenance_window_nodes", "window_id"},
		{"maintenance_selections", "window_id"}, {"maintenance_create_requests", "window_id"},
		{"maintenance_cancel_actions", "window_id"},
	})
	allTables := append(slices.Clone(tables), maintenanceTables...)
	var levels []map[string]any
	previous := 0
	for _, target := range []int{1000, 10000, 100000} {
		if target > maximum {
			break
		}
		started := time.Now()
		growIncidentActionHistory(t, s, f, tables, ids, previous+1, target)
		previous = target
		seedSeconds := time.Since(started).Seconds()
		before := notificationHistoryDigests(t, s, tables)
		for _, table := range tables {
			if before[table.name].Rows != target*len(table.rows) {
				t.Fatalf("%s count=%d expected=%d", table.name, before[table.name].Rows, target*len(table.rows))
			}
		}
		metrics := measureIncidentActionHistory(t, s, f, ids, target)
		for _, n := range []int{1, target / 2, target} {
			replayIncidentActionHistory(t, s, f, ids, n)
		}
		if !reflect.DeepEqual(before, notificationHistoryDigests(t, s, tables)) {
			t.Fatal("reads/replays/reports mutated historical records")
		}
		var pages, pageSize int64
		notificationHistoryMust(t, s.db.QueryRow("PRAGMA page_count").Scan(&pages))
		notificationHistoryMust(t, s.db.QueryRow("PRAGMA page_size").Scan(&pageSize))
		levels = append(levels, map[string]any{"historical_incidents": target, "seed_seconds": seedSeconds, "sqlite_allocated_bytes": pages * pageSize, "historical_records": before, "reads_with_concurrent_reports": metrics})
	}
	before := notificationHistoryDigests(t, s, tables)
	allBefore := retainedHistoryDigests(t, s, allTables, false)
	for n := range 1000 {
		batch, err := s.RetainHistory(context.Background(), time.Now().UTC().Add(400*24*time.Hour), DefaultRetentionPolicy())
		notificationHistoryMust(t, err)
		if !batch.More {
			break
		}
		if n == 999 {
			t.Fatal("metric retention did not drain")
		}
	}
	if !reflect.DeepEqual(before, notificationHistoryDigests(t, s, tables)) {
		t.Fatal("metric retention altered action history")
	}
	if !reflect.DeepEqual(allBefore, retainedHistoryDigests(t, s, allTables, false)) {
		t.Fatal("metric retention altered full management history")
	}
	backup := filepath.Join(directory, "snapshot.db")
	notificationHistoryMust(t, s.ConsistentBackup(context.Background(), backup))
	notificationHistoryMust(t, ValidateDatabaseFile(context.Background(), backup))
	notificationHistoryMust(t, s.Close())
	s, err = Open(context.Background(), s.path)
	notificationHistoryMust(t, err)
	if !reflect.DeepEqual(allBefore, retainedHistoryDigests(t, s, allTables, false)) {
		t.Fatal("reopen changed full management history")
	}
	for _, n := range []int{1, maximum / 2, maximum} {
		replayIncidentActionHistory(t, s, f, ids, n)
	}
	checkMaintenanceHistory(t, s, f, maintenance)
	restored, err := Open(context.Background(), backup)
	notificationHistoryMust(t, err)
	defer func() { notificationHistoryMust(t, restored.Close()) }()
	if !reflect.DeepEqual(allBefore, retainedHistoryDigests(t, restored, allTables, false)) {
		t.Fatal("backup changed action history")
	}
	for _, n := range []int{1, maximum / 2, maximum} {
		replayIncidentActionHistory(t, restored, f, ids, n)
	}
	checkMaintenanceHistory(t, restored, f, maintenance)
	// New actions still commit against the restored original rule/node.
	i := observe(t, restored, f.rule, f.node, f.at.Add(time.Hour), true, true)
	if i.State != "firing" || i.ID == f.incident {
		t.Fatal("fresh incident blocked by old history")
	}
	_, err = restored.AcknowledgeIncident(context.Background(), i.ID, "fresh-ack", "fresh", f.actor)
	notificationHistoryMust(t, err)
	created, err := restored.CreateIncidentSilence(context.Background(), i.ID, "fresh-silence", f.actor, f.params, f.at.Add(time.Hour+time.Second))
	notificationHistoryMust(t, err)
	_, err = restored.CancelIncidentSilence(context.Background(), i.ID, created.SilenceID, "fresh-cancel", f.actor, 1, f.at.Add(time.Hour+2*time.Second))
	notificationHistoryMust(t, err)
	if !reflect.DeepEqual(before, notificationHistoryDigests(t, restored, tables)) {
		t.Fatal("fresh post-restore actions changed copied history")
	}
	var integrity string
	notificationHistoryMust(t, restored.db.QueryRow("PRAGMA integrity_check").Scan(&integrity))
	if integrity != "ok" {
		t.Fatal("integrity check failed")
	}
	rows, err := restored.db.Query("PRAGMA foreign_key_check")
	notificationHistoryMust(t, err)
	if rows.Next() {
		rows.Close()
		t.Fatal("foreign key violation")
	}
	notificationHistoryMust(t, rows.Err())
	notificationHistoryMust(t, rows.Close())
	result := map[string]any{"format": "myprobe.incident-action-history.v1", "passed": true, "maximum_historical_incidents": maximum, "actual_nodes": 1, "levels": levels, "full_restored_history": allBefore, "maintenance_windows": 1000, "maintenance_limit_replay_and_pagination": true, "metric_retention_400days_preserves_history": true, "reopen_and_consistent_backup_restore": true, "old_replays_and_changed_content_conflicts": true, "fresh_post_restore_actions": true, "integrity_and_foreign_keys": true, "scope": "Synthetic Store component. Copied resolved traces with rewritten IDs and request digests, one silence per incident. Maintenance uses1000 real create/cancel calls. No external provider, encrypted application restore,50-node SLO,24h or physical2GiB proof."}
	raw, err := json.MarshalIndent(result, "", "  ")
	notificationHistoryMust(t, err)
	file, err := os.OpenFile(filepath.Join(base, "incident-action-history-capacity.json"), os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	notificationHistoryMust(t, err)
	_, err = file.Write(append(raw, '\n'))
	notificationHistoryMust(t, err)
	notificationHistoryMust(t, file.Sync())
	notificationHistoryMust(t, file.Close())
}

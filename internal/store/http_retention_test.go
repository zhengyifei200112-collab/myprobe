package store

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

// SQL generates expired rows without spending hours running the scheduler.
// This is a retention fixture, not live Agent throughput or a 31-day soak.
func httpRetentionFixture(t testing.TB, tasks, epochs int) (*Store, time.Time) {
	return httpRetentionDataset(t, tasks, epochs, 1, 1)
}

func httpRetentionDataset(t testing.TB, tasks, epochs, observers, services int) (*Store, time.Time) {
	t.Helper()
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(httpFixtureDir(t), "retention.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	nodes := make([]Node, observers)
	ids := make([]string, observers)
	for i := range nodes {
		node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: fmt.Sprintf("Synthetic retention observer %03d", i)})
		if err != nil {
			t.Fatal(err)
		}
		nodes[i], ids[i] = node, node.ID
	}
	definitions := make([]HTTPService, services)
	for i := range definitions {
		v, err := s.SaveHTTPService(ctx, HTTPService{Name: fmt.Sprintf("Synthetic retention service %03d", i), Enabled: true, IntervalSeconds: 60, NodeIDs: ids, Spec: httpcheck.Spec{URL: "http://example.test/retention", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 1000, MaxBodyBytes: 1024}})
		if err != nil {
			t.Fatal(err)
		}
		definitions[i] = v
	}
	node, v := nodes[0], definitions[0]
	now := time.Date(2026, 10, 13, 12, 0, 0, 0, time.UTC)
	floor := now.Add(-HTTPHistoryRetention)
	old := floor.Add(-31 * 24 * time.Hour)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO http_tasks(id,service_id,node_id,revision,scheduled_at,expires_at,task_json,result_json,received_at) VALUES(?,?,?,1,?,?,?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	for i := 0; i < tasks; i++ {
		at := old.Add(time.Duration(i/(observers*services)) * time.Minute)
		definition, observer := definitions[i%services], nodes[(i/services)%observers]
		id := fmt.Sprintf("expired-%09d", i)
		// Real task shape/size, with only synthetic aliases and target.
		body := fmt.Sprintf(`{"id":%q,"service_id":%q,"revision":1,"scheduled_at":%q,"expires_at":%q,"spec":{"url":"http://example.test/retention","method":"GET","status_codes":[200],"timeout_ms":1000,"max_body_bytes":1024}}`, id, definition.ID, formatTime(at), formatTime(at.Add(time.Second)))
		result := fmt.Sprintf(`{"task_id":%q,"service_id":%q,"revision":1,"scheduled_at":%q,"completed_at":%q,"duration_ms":1000,"outcome":"success","status_code":200}`, id, definition.ID, formatTime(at), formatTime(at.Add(time.Second)))
		if _, err = stmt.ExecContext(ctx, id, definition.ID, observer.ID, formatTime(at), formatTime(at.Add(time.Second)), body, result, formatTime(at.Add(time.Second))); err != nil {
			t.Fatal(err)
		}
	}
	for _, at := range []string{floor.Format(time.RFC3339), floor.Format("2006-01-02T15:04:05.000Z"), floor.Add(time.Nanosecond).Format(time.RFC3339Nano), floor.Add(-time.Nanosecond).Format(time.RFC3339Nano)} {
		if _, err = stmt.ExecContext(ctx, "boundary-"+at, v.ID, node.ID, at, at, `{}`, nil, nil); err != nil {
			t.Fatal(err)
		}
	}
	epoch, err := tx.PrepareContext(ctx, `INSERT INTO http_schedule_epochs(service_id,node_id,revision,start_ns,end_ns,interval_seconds) VALUES(?,?,?,?,?,60)`)
	if err != nil {
		t.Fatal(err)
	}
	defer epoch.Close()
	for i := 0; i < epochs; i++ {
		if _, err = epoch.ExecContext(ctx, v.ID, node.ID, i+2, old.UnixNano(), floor.Add(-time.Second).UnixNano()); err != nil {
			t.Fatal(err)
		}
	}
	for i, end := range []time.Time{floor, floor.Add(time.Nanosecond)} {
		if _, err = epoch.ExecContext(ctx, v.ID, node.ID, epochs+2+i, old.UnixNano(), end.UnixNano()); err != nil {
			t.Fatal(err)
		}
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	return s, now
}

func TestHTTPRetentionCatchupBoundariesRollbackAndClock(t *testing.T) {
	s, now := httpRetentionFixture(t, 2003, 2001)
	ctx := context.Background()
	// Fail after task deletion to prove both tables and the watermark roll back.
	if _, err := s.db.Exec(`CREATE TRIGGER synthetic_prune_failure BEFORE DELETE ON http_schedule_epochs BEGIN SELECT RAISE(ABORT,'synthetic retention failure');END`); err != nil {
		t.Fatal(err)
	}
	batch, err := s.PruneHTTPHistory(ctx, now)
	if err == nil || batch != (HTTPRetentionBatch{}) {
		t.Fatalf("failed batch reported committed work: %+v %v", batch, err)
	}
	assertCount(t, s, "SELECT COUNT(*) FROM http_tasks", 2007)
	assertCount(t, s, "SELECT COUNT(*) FROM http_schedule_epochs", 2004)
	assertCount(t, s, "SELECT retained_from_ns FROM http_history_state", 0)
	if _, err := s.db.Exec(`DROP TRIGGER synthetic_prune_failure`); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		// A backward clock must still drain rows already hidden by the floor.
		at := now
		if i > 0 {
			at = now.Add(-time.Hour)
		}
		batch, err = s.PruneHTTPHistory(ctx, at)
		if err != nil {
			t.Fatal(err)
		}
		if !batch.RetainedFrom.Equal(now.Add(-HTTPHistoryRetention)) {
			t.Fatal("retention floor regressed")
		}
		if i < 2 && (batch.TasksDeleted != 1000 || batch.EpochsDeleted != 1000 || !batch.More) {
			t.Fatalf("unbounded/incomplete batch %d: %+v", i, batch)
		}
		if i == 2 && (batch.TasksDeleted != 4 || batch.EpochsDeleted != 2 || batch.More) {
			t.Fatalf("final batch: %+v", batch)
		}
		// Each batch has committed and released the writer before returning.
		nodes, err := s.ListNodes(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.SaveReport(ctx, nodes[0].ID, protocol.Report{CapturedAt: now, CPU: protocol.CPUMetric{UsagePercent: 10}}); err != nil {
			t.Fatal(err)
		}
	}
	assertCount(t, s, "SELECT COUNT(*) FROM http_tasks", 3)
	assertCount(t, s, "SELECT COUNT(*) FROM http_schedule_epochs", 2)
	batch, err = s.PruneHTTPHistory(ctx, now.Add(-time.Hour))
	if err != nil || batch.More || batch.TasksDeleted != 0 || batch.EpochsDeleted != 0 {
		t.Fatalf("empty batch: %+v %v", batch, err)
	}
	canceled, cancel := context.WithCancel(ctx)
	cancel()
	batch, err = s.PruneHTTPHistory(canceled, now.Add(time.Hour))
	if err == nil || batch != (HTTPRetentionBatch{}) {
		t.Fatalf("canceled batch: %+v %v", batch, err)
	}
	assertCount(t, s, "SELECT retained_from_ns FROM http_history_state", int(now.Add(-HTTPHistoryRetention).UnixNano()))
}

func TestHTTPRetentionConcurrentBatchesAndIndexedEpochs(t *testing.T) {
	s, now := httpRetentionFixture(t, 1500, 1500)
	ctx := context.Background()
	type outcome struct {
		batch HTTPRetentionBatch
		err   error
	}
	done := make(chan outcome, 2)
	for i := 0; i < 2; i++ {
		go func() { b, e := s.PruneHTTPHistory(ctx, now); done <- outcome{b, e} }()
	}
	var tasks, epochs int64
	for i := 0; i < 2; i++ {
		r := <-done
		if r.err != nil {
			t.Fatal(r.err)
		}
		tasks += r.batch.TasksDeleted
		epochs += r.batch.EpochsDeleted
	}
	if tasks != 1501 || epochs != 1501 {
		t.Fatalf("concurrent pruning lost or repeated rows: %d %d", tasks, epochs)
	}
	assertCount(t, s, "SELECT COUNT(*) FROM http_tasks", 3)
	assertCount(t, s, "SELECT COUNT(*) FROM http_schedule_epochs", 2)
	rows, err := s.db.QueryContext(ctx, `EXPLAIN QUERY PLAN SELECT rowid FROM http_schedule_epochs WHERE end_ns IS NOT NULL AND end_ns<=? ORDER BY end_ns LIMIT ?`, now.Add(-HTTPHistoryRetention).UnixNano(), httpRetentionBatchSize)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	indexed := false
	for rows.Next() {
		var id, parent, unused int
		var plan string
		if err := rows.Scan(&id, &parent, &unused, &plan); err != nil {
			t.Fatal(err)
		}
		indexed = indexed || strings.Contains(plan, "idx_http_epochs_retention")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if !indexed {
		t.Fatal("closed epoch pruning does not use the retention index")
	}
}

func TestHTTPRetentionIndexUpgradeAndConsistentRestore(t *testing.T) {
	s, now := httpRetentionFixture(t, 7, 3)
	ctx := context.Background()
	// Represent the previous development schema, retaining all existing rows.
	if _, err := s.db.Exec(`DROP INDEX idx_http_epochs_retention;DELETE FROM schema_migrations WHERE version='038_http_retention_catchup.sql'`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	assertCount(t, s, "SELECT COUNT(*) FROM http_tasks", 11)
	assertCount(t, s, "SELECT COUNT(*) FROM http_schedule_epochs", 6)
	assertCount(t, s, "SELECT retained_from_ns FROM http_history_state", 0)
	assertCount(t, s, "SELECT COUNT(*) FROM schema_migrations WHERE version='038_http_retention_catchup.sql'", 1)
	backupPath := filepath.Join(httpFixtureDir(t), "consistent-retention-backup.db")
	if err := s.ConsistentBackup(ctx, backupPath); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(ctx, backupPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	assertCount(t, restored, "SELECT COUNT(*) FROM sqlite_master WHERE type='index' AND name='idx_http_epochs_retention'", 1)
	batch, err := restored.PruneHTTPHistory(ctx, now)
	if err != nil || batch.TasksDeleted != 8 || batch.EpochsDeleted != 4 || batch.More {
		t.Fatalf("restored cleanup: %+v %v", batch, err)
	}
	assertCount(t, restored, "SELECT COUNT(*) FROM http_tasks", 3)
	assertCount(t, restored, "SELECT COUNT(*) FROM http_schedule_epochs", 2)
	assertCount(t, s, "SELECT COUNT(*) FROM http_tasks", 11)
}

func TestHTTPRetentionPreservesPermanentImportReplay(t *testing.T) {
	s, bundle, mapping, _, _ := serviceTransferFixture(t)
	ctx := context.Background()
	preview, err := s.PreviewServiceAlertImport(ctx, bundle, mapping)
	if err != nil {
		t.Fatal(err)
	}
	result, err := s.ApplyServiceAlertImport(ctx, "retention-original-request", bundle, mapping, preview.Digest)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.PruneHTTPHistory(ctx, time.Now().UTC().Add(60*24*time.Hour)); err != nil {
		t.Fatal(err)
	}
	again, err := s.ApplyServiceAlertImport(ctx, "retention-original-request", bundle, mapping, preview.Digest)
	if err != nil || !again.Replayed || result.Created != again.Created || result.Updated != again.Updated {
		t.Fatalf("permanent replay changed: %+v %v", again, err)
	}
	assertCount(t, s, "SELECT COUNT(*) FROM service_alert_imports", 1)
}

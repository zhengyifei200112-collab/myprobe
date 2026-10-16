package store

import (
	"context"
	"errors"
	"math"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func networkTaskFixture(t *testing.T) (*Store, string, Target, string, time.Time) {
	t.Helper()
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "network.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	n, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "Synthetic network task"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.CreateTarget(ctx, CreateTargetParams{Name: "Synthetic probe", Kind: protocol.TaskKindPing, Host: "example.test", IntervalSeconds: 30, TimeoutMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AssignTarget(ctx, n.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	var epoch string
	var start int64
	if err = s.db.QueryRow(`SELECT id,starts_at FROM network_schedule_epochs WHERE node_id=? AND target_id=? AND ends_at IS NULL`, n.ID, target.ID).Scan(&epoch, &start); err != nil {
		t.Fatal(err)
	}
	return s, n.ID, target, epoch, time.Unix(start, 0).UTC().Add(100 * time.Millisecond)
}

func TestNetworkTaskClaimPhaseOfflineAndRestart(t *testing.T) {
	s, _, _, epoch, now := networkTaskFixture(t)
	ctx := context.Background()
	slot := now.Truncate(time.Second)
	items, err := s.ListNetworkAssignments(ctx, now.Add(-time.Second))
	if err != nil || len(items) != 0 {
		t.Fatal("plan began before its anchor", err)
	}
	items, err = s.ListNetworkAssignments(ctx, now)
	if err != nil || len(items) != 1 || !items[0].Eligible || !items[0].ScheduledAt.Equal(slot) {
		t.Fatal("current slot not eligible", err)
	}
	first, err := s.ClaimNetworkTask(ctx, epoch, slot, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ClaimNetworkTask(ctx, epoch, slot, now); !errors.Is(err, ErrNetworkTaskDispatched) {
		t.Fatal("duplicate claim succeeded", err)
	}
	if err = s.FinishNetworkDispatch(ctx, first, "offline", now); err != nil {
		t.Fatal(err)
	}
	items, err = s.ListNetworkAssignments(ctx, now)
	if err != nil || items[0].Eligible {
		t.Fatal("offline retry did not back off", err)
	}
	second, err := s.ClaimNetworkTask(ctx, epoch, slot, now.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if second.Task.ID != first.Task.ID || second.LeaseToken == first.LeaseToken {
		t.Fatal("retry identity/ownership changed incorrectly")
	}
	if err = s.FinishNetworkDispatch(ctx, first, "", now.Add(time.Second)); !errors.Is(err, ErrNetworkTaskDispatched) {
		t.Fatal("stale owner changed dispatch", err)
	}
	if err = s.FinishNetworkDispatch(ctx, second, "write_error", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, s.DatabasePath())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if _, err = reopened.ClaimNetworkTask(ctx, epoch, slot, now.Add(2*time.Second)); !errors.Is(err, ErrNetworkTaskDispatched) {
		t.Fatal("restart resent an uncertain write", err)
	}
	items, err = reopened.ListNetworkAssignments(ctx, now.Add(128*time.Second))
	if err != nil || len(items) != 1 || items[0].Eligible {
		t.Fatal("missed dispatch window was replayed", err)
	}
	items, err = reopened.ListNetworkAssignments(ctx, now.Add(150*time.Second))
	if err != nil || !items[0].Eligible || !items[0].ScheduledAt.Equal(slot.Add(150*time.Second)) {
		t.Fatal("restart lost persistent phase", err)
	}
	assertCount(t, s, `SELECT COUNT(*) FROM network_probe_tasks`, 1)
}

func TestNetworkResultAtomicCommitReplayAndBindings(t *testing.T) {
	s, node, target, epoch, now := networkTaskFixture(t)
	ctx := context.Background()
	task, err := s.ClaimNetworkTask(ctx, epoch, now.Truncate(time.Second), now)
	if err != nil {
		t.Fatal(err)
	}
	r := protocol.LatencyResult{TaskID: task.Task.ID, TargetID: target.ID, Success: true, LatencyMS: 10, CompletedAt: now.Add(time.Second)}
	if _, err = s.AcceptNetworkResult(ctx, "wrong-node", protocol.TaskKindPing, r, now.Add(time.Second)); !errors.Is(err, ErrNetworkTaskRejected) {
		t.Fatal("wrong node accepted", err)
	}
	if _, err = s.AcceptNetworkResult(ctx, node, protocol.TaskKindTCPing, r, now.Add(time.Second)); !errors.Is(err, ErrNetworkTaskRejected) {
		t.Fatal("wrong kind accepted", err)
	}
	if _, err = s.db.Exec(`CREATE TRIGGER synthetic_network_result_failure BEFORE UPDATE ON network_probe_tasks WHEN NEW.state='completed' BEGIN SELECT RAISE(ABORT,'synthetic result failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.AcceptNetworkResult(ctx, node, protocol.TaskKindPing, r, now.Add(time.Second)); err == nil {
		t.Fatal("expected result commit failure")
	}
	assertCount(t, s, `SELECT COUNT(*) FROM latency_samples`, 0)
	assertCount(t, s, `SELECT COUNT(*) FROM network_probe_tasks WHERE state='sending'`, 1)
	if _, err = s.db.Exec(`DROP TRIGGER synthetic_network_result_failure`); err != nil {
		t.Fatal(err)
	}
	accepted, err := s.AcceptNetworkResult(ctx, node, protocol.TaskKindPing, r, now.Add(time.Second))
	if err != nil || !accepted {
		t.Fatal("retry after storage failure did not commit", err)
	}
	// A result can beat the sender's success bookkeeping.
	if err = s.FinishNetworkDispatch(ctx, task, "", now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	r.CompletedAt = r.CompletedAt.In(time.FixedZone("test", 8*3600))
	accepted, err = s.AcceptNetworkResult(ctx, node, protocol.TaskKindPing, r, now.Add(2*time.Second))
	if err != nil || accepted {
		t.Fatal("canonical-time replay not idempotent", err)
	}
	r.LatencyMS = 11
	if _, err = s.AcceptNetworkResult(ctx, node, protocol.TaskKindPing, r, now.Add(2*time.Second)); !errors.Is(err, ErrNetworkTaskRejected) {
		t.Fatal("conflicting result accepted", err)
	}
	assertCount(t, s, `SELECT COUNT(*) FROM latency_samples`, 1)
	assertCount(t, s, `SELECT COUNT(*) FROM network_probe_tasks WHERE state='completed' AND outcome='success'`, 1)
}

func TestNetworkResultConcurrentReplay(t *testing.T) {
	s, node, target, epoch, now := networkTaskFixture(t)
	ctx := context.Background()
	task, err := s.ClaimNetworkTask(ctx, epoch, now.Truncate(time.Second), now)
	if err != nil {
		t.Fatal(err)
	}
	r := protocol.LatencyResult{TaskID: task.Task.ID, TargetID: target.ID, Success: true, LatencyMS: 12, CompletedAt: now.Add(time.Second)}
	var workers sync.WaitGroup
	var inserted atomic.Int64
	problems := make(chan error, 8)
	for range 8 {
		workers.Add(1)
		go func() {
			defer workers.Done()
			accepted, err := s.AcceptNetworkResult(ctx, node, protocol.TaskKindPing, r, now.Add(time.Second))
			if err != nil {
				problems <- err
			}
			if accepted {
				inserted.Add(1)
			}
		}()
	}
	workers.Wait()
	close(problems)
	for err := range problems {
		t.Error(err)
	}
	if inserted.Load() != 1 {
		t.Fatalf("inserted = %d", inserted.Load())
	}
	assertCount(t, s, `SELECT COUNT(*) FROM latency_samples`, 1)
}

func TestNetworkDispatchExpiredLeaseKeepsPossibleResult(t *testing.T) {
	s, node, target, epoch, now := networkTaskFixture(t)
	ctx := context.Background()
	task, err := s.ClaimNetworkTask(ctx, epoch, now.Truncate(time.Second), now)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ExpireNetworkDispatchLeases(ctx, now.Add(6*time.Second)); err != nil {
		t.Fatal(err)
	}
	assertCount(t, s, `SELECT COUNT(*) FROM network_probe_tasks WHERE state='sent_unknown' AND dispatch_reason='dispatch_unknown'`, 1)
	r := protocol.LatencyResult{TaskID: task.Task.ID, TargetID: target.ID, Success: true, LatencyMS: 10, CompletedAt: now.Add(6 * time.Second)}
	accepted, err := s.AcceptNetworkResult(ctx, node, protocol.TaskKindPing, r, now.Add(6*time.Second))
	if err != nil || !accepted {
		t.Fatal("expired send lease discarded a valid result", err)
	}
	assertCount(t, s, `SELECT COUNT(*) FROM latency_samples`, 1)
}

func TestNetworkTaskUpgradeKeepsSeparateResultBoundary(t *testing.T) {
	ctx := context.Background()
	s := preHistoryIndexStore(t)
	for _, version := range []string{"041_history_retention_indexes.sql", "042_network_schedule_epochs.sql"} {
		body, err := migrations.ReadFile("migrations/" + version)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = s.db.Exec(string(body)); err != nil {
			t.Fatal(err)
		}
		if _, err = s.db.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`, version, nowText()); err != nil {
			t.Fatal(err)
		}
	}
	n, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "Synthetic pre-ledger node"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := createLegacyNetworkTarget(ctx, s, CreateTargetParams{Name: "Synthetic pre-ledger target", Kind: protocol.TaskKindPing, Host: "example.test", IntervalSeconds: 30, TimeoutMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AssignTarget(ctx, n.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	past := time.Now().Unix() - 86400
	if _, err = s.db.Exec(`UPDATE network_schedule_metadata SET known_since=?`, past); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE network_schedule_epochs SET starts_at=?`, past); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`CREATE TRIGGER synthetic_043_failure BEFORE INSERT ON schema_migrations WHEN NEW.version='043_network_probe_tasks.sql' BEGIN SELECT RAISE(ABORT,'synthetic migration failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err == nil {
		t.Fatal("expected migration failure")
	}
	assertCount(t, s, `SELECT COUNT(*) FROM pragma_table_info('network_schedule_metadata') WHERE name='known_results_since'`, 0)
	assertCount(t, s, `SELECT COUNT(*) FROM sqlite_master WHERE name='network_probe_tasks'`, 0)
	if _, err = s.db.Exec(`DROP TRIGGER synthetic_043_failure`); err != nil {
		t.Fatal(err)
	}
	before := time.Now().Unix()
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var planStart, resultStart, epochStart int64
	if err = s.db.QueryRow(`SELECT known_since,known_results_since FROM network_schedule_metadata WHERE singleton=1`).Scan(&planStart, &resultStart); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow(`SELECT starts_at FROM network_schedule_epochs WHERE node_id=? AND target_id=?`, n.ID, target.ID).Scan(&epochStart); err != nil {
		t.Fatal(err)
	}
	if planStart != past || epochStart != past || resultStart < before {
		t.Fatal("upgrade reset phase or invented historical result coverage")
	}
	path := filepath.Join(t.TempDir(), "result-boundary-restore.db")
	if _, err = s.db.Exec(`VACUUM INTO ?`, path); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var restoredPlan, restoredResult int64
	if err = restored.db.QueryRow(`SELECT known_since,known_results_since FROM network_schedule_metadata WHERE singleton=1`).Scan(&restoredPlan, &restoredResult); err != nil {
		t.Fatal(err)
	}
	if restoredPlan != planStart || restoredResult != resultStart {
		t.Fatal("restore changed result-history boundary")
	}
}

func TestNetworkDispatchLeaseCleanupIsBounded(t *testing.T) {
	s, node, target, _, now := networkTaskFixture(t)
	ctx := context.Background()
	_, err := s.db.Exec(`WITH RECURSIVE seq(i) AS (VALUES(0) UNION ALL SELECT i+1 FROM seq WHERE i+1<2501)
 INSERT INTO network_probe_tasks(id,source,node_id,target_id,kind,host,timeout_ms,dispatch_until_ns,expires_at_ns,created_at_ns,state,lease_token,lease_until_ns)
 SELECT 'synthetic-lease-'||i,'manual',?,?,'ping','example.test',1000,?,?,?,'sending','synthetic-owner-'||i,? FROM seq`, node, target.ID, now.Add(time.Second).UnixNano(), now.Add(time.Minute).UnixNano(), now.UnixNano(), now.Add(-time.Second).UnixNano())
	if err != nil {
		t.Fatal(err)
	}
	if err = s.ExpireNetworkDispatchLeases(ctx, now); err != nil {
		t.Fatal(err)
	}
	assertCount(t, s, `SELECT COUNT(*) FROM network_probe_tasks WHERE state='sent_unknown'`, 1000)
	assertCount(t, s, `SELECT COUNT(*) FROM network_probe_tasks WHERE state='sending'`, 1501)
	for range 2 {
		if err = s.ExpireNetworkDispatchLeases(ctx, now); err != nil {
			t.Fatal(err)
		}
	}
	assertCount(t, s, `SELECT COUNT(*) FROM network_probe_tasks WHERE state='sending'`, 0)
}

func TestNetworkResultExpiredChangedConfigurationAndManualOutcome(t *testing.T) {
	for _, scenario := range []string{"expired", "changed", "nonfinite", "manual"} {
		t.Run(scenario, func(t *testing.T) {
			s, node, target, epoch, now := networkTaskFixture(t)
			ctx := context.Background()
			var task NetworkTask
			var err error
			if scenario == "manual" {
				task, err = s.RecordManualNetworkTask(ctx, node, protocol.Task{ID: "synthetic-manual", TargetID: target.ID, Kind: protocol.TaskKindPing, Host: target.Host, TimeoutMS: 1000, ExpiresAt: now.Add(time.Minute)}, now)
			} else {
				task, err = s.ClaimNetworkTask(ctx, epoch, now.Truncate(time.Second), now)
			}
			if err != nil {
				t.Fatal(err)
			}
			r := protocol.LatencyResult{TaskID: task.Task.ID, TargetID: target.ID, Success: true, LatencyMS: 10, CompletedAt: now.Add(time.Second)}
			received := now.Add(time.Second)
			switch scenario {
			case "expired":
				received = task.Task.ExpiresAt.Add(time.Nanosecond)
				r.CompletedAt = received
			case "changed":
				if _, err = s.UpdateTarget(ctx, target.ID, UpdateTargetParams{Name: target.Name, Kind: target.Kind, Host: target.Host, IntervalSeconds: 60, TimeoutMS: 1000, Enabled: true}); err != nil {
					t.Fatal(err)
				}
			case "nonfinite":
				r.LatencyMS = math.NaN()
			case "manual":
				r.Success = false
				r.LatencyMS = 0
				r.ErrorClass = "unsupported"
			}
			accepted, err := s.AcceptNetworkResult(ctx, node, protocol.TaskKindPing, r, received)
			if scenario == "manual" {
				if err != nil || !accepted {
					t.Fatal(err)
				}
				assertCount(t, s, `SELECT COUNT(*) FROM network_probe_tasks WHERE source='manual' AND epoch_id IS NULL AND outcome='not_executed'`, 1)
			} else {
				if !errors.Is(err, ErrNetworkTaskRejected) || accepted {
					t.Fatal("invalid result accepted", err)
				}
				assertCount(t, s, `SELECT COUNT(*) FROM latency_samples`, 0)
			}
		})
	}
}

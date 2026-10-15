package store

import (
	"context"
	"errors"
	"fmt"
	"math"
	"path/filepath"
	"sync"
	"testing"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func networkArchiveFixture(t *testing.T) (*Store, NetworkQualityQuery, RetentionPolicy) {
	t.Helper()
	s, node, target, epoch, _ := networkTaskFixture(t)
	anchor := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	if _, err := s.db.Exec(`UPDATE network_schedule_epochs SET starts_at=?,interval_seconds=5 WHERE id=?`, anchor.Unix(), epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE network_schedule_metadata SET known_since=?,known_results_since=?`, anchor.Unix(), anchor.Unix()); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	for i := range 500 {
		if i%17 == 3 {
			continue
		} // No task: a real absent slot, no fabricated row.
		slot := anchor.Add(time.Duration(i) * 5 * time.Second)
		task, err := s.ClaimNetworkTask(ctx, epoch, slot, slot.Add(10*time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
		if i%17 == 4 {
			if err = s.FinishNetworkDispatch(ctx, task, "offline", slot.Add(20*time.Millisecond)); err != nil {
				t.Fatal(err)
			}
			continue
		}
		result := protocol.LatencyResult{TaskID: task.Task.ID, TargetID: target.ID, Success: true, LatencyMS: float64(i%100) / 3, CompletedAt: slot.Add(time.Second)}
		if i%17 == 5 {
			result.Success = false
			result.LatencyMS = 0
			result.ErrorClass = "timeout"
		}
		if i%17 == 6 {
			result.Success = false
			result.LatencyMS = 0
			result.ErrorClass = "unsupported"
		}
		if _, err = s.AcceptNetworkResult(ctx, node, target.Kind, result, slot.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	return s, NetworkQualityQuery{NodeID: node, TargetID: target.ID, Kind: target.Kind, Start: anchor, End: anchor.Add(45 * time.Minute), AsOf: anchor.Add(50 * time.Minute)}, RetentionPolicy{Raw: 10 * time.Minute, OneMinute: 30 * time.Minute, FiveMinute: 2 * time.Hour}
}

func assertNetworkQualityEquivalent(t *testing.T, want, got NetworkQualityStats) {
	t.Helper()
	if want.Planned != got.Planned || want.Pending != got.Pending || want.Success != got.Success || want.Failure != got.Failure || want.Missing != got.Missing || want.NotExecuted != got.NotExecuted || want.ObservedMissing != got.ObservedMissing || want.JitterPairs != got.JitterPairs || want.Totals.Causes != got.Totals.Causes {
		t.Fatalf("raw/archive population differs: want %+v; got %+v", want, got)
	}
	for name, pair := range map[string][2]*float64{"coverage": {want.Coverage, got.Coverage}, "failure": {want.FailureRate, got.FailureRate}, "mean": {want.RTTMean, got.RTTMean}, "min": {want.RTTMin, got.RTTMin}, "max": {want.RTTMax, got.RTTMax}, "jitter": {want.Jitter, got.Jitter}} {
		if (pair[0] == nil) != (pair[1] == nil) || pair[0] != nil && math.Abs(*pair[0]-*pair[1]) > 1e-9 {
			t.Fatalf("%s changed across archive", name)
		}
	}
	if got.QuantileMethod == "histogram_v1" {
		for name, values := range map[string][3]float64{"P50": {*want.P50, got.P50Bounds.LowerMS, got.P50Bounds.UpperMS}, "P95": {*want.P95, got.P95Bounds.LowerMS, got.P95Bounds.UpperMS}} {
			if values[0] < values[1] || values[0] > values[2] {
				t.Fatalf("%s exact %g outside archive [%g,%g]", name, values[0], values[1], values[2])
			}
		}
	}
}

func TestNetworkQualityArchiveMixedTiersIdempotentAndClockRollback(t *testing.T) {
	s, q, policy := networkArchiveFixture(t)
	ctx := context.Background()
	want, err := s.PublicNetworkQuality(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	for pass := 0; pass < 10; pass++ {
		started := time.Now()
		batch, err := s.RetainNetworkQuality(ctx, q.AsOf, policy)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("archive pass=%d duration=%s raw_buckets=%d minute_buckets=%d tasks_deleted=%d more=%v", pass, time.Since(started), batch.RawBuckets, batch.MinuteBuckets, batch.TasksDeleted, batch.More)
		if batch.RawBuckets > networkArchiveBuckets || batch.MinuteBuckets > networkArchiveBuckets {
			t.Fatal("unbounded archive batch")
		}
		got, err := s.PublicNetworkQuality(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		assertNetworkQualityEquivalent(t, want, got)
		if got.GranularitySeconds != 300 || got.QuantileMethod != "histogram_v1" {
			t.Fatal("long history lacks its actual resolution", got)
		}
		if !batch.More {
			break
		}
		if pass == 9 {
			t.Fatal("archive did not drain")
		}
	}
	var rawRows, minuteRows, fiveRows, payloadBytes, maximumPayload int64
	if err = s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM network_probe_tasks WHERE source='scheduled'),COALESCE(SUM(bucket_seconds=60),0),COALESCE(SUM(bucket_seconds=300),0),COALESCE(SUM(length(summary)),0),COALESCE(MAX(length(summary)),0) FROM network_quality_rollups`).Scan(&rawRows, &minuteRows, &fiveRows, &payloadBytes, &maximumPayload); err != nil {
		t.Fatal(err)
	}
	t.Logf("fixture planned=%d success=%d failure=%d missing=%d jitter_pairs=%d raw_rows=%d minute_rows=%d five_minute_rows=%d payload_bytes=%d max_payload_bytes=%d", want.Planned, want.Success, want.Failure, want.Missing, want.JitterPairs, rawRows, minuteRows, fiveRows, payloadBytes, maximumPayload)
	for _, query := range []string{`SELECT COUNT(*) FROM network_quality_rollups WHERE bucket_seconds=300`, `SELECT COUNT(*) FROM network_quality_rollups WHERE bucket_seconds=60`, `SELECT COUNT(*) FROM network_probe_tasks WHERE source='scheduled'`} {
		var n int
		if err = s.db.QueryRow(query).Scan(&n); err != nil || n == 0 {
			t.Fatal("mixed-tier fixture missing a source", query, err)
		}
	}
	batch, err := s.RetainNetworkQuality(ctx, q.AsOf.Add(-time.Hour), RetentionPolicy{Raw: time.Hour, OneMinute: 2 * time.Hour, FiveMinute: 3 * time.Hour})
	if err != nil || batch.More || batch.TasksDeleted != 0 || batch.RawBuckets != 0 || batch.MinuteBuckets != 0 {
		t.Fatal("rollback or expanded retention rebuilt deleted data", batch, err)
	}
	clipped := q
	clipped.Start = clipped.Start.Add(time.Second)
	clipped.End = clipped.End.Add(-time.Second)
	got, err := s.PublicNetworkQuality(ctx, clipped)
	if err != nil {
		t.Fatal(err)
	}
	if got.Availability != "partial_resolution" || !got.EffectiveStart.Equal(q.Start.Add(5*time.Minute)) || !got.EffectiveEnd.Equal(q.End.Add(-5*time.Minute)) {
		t.Fatal("summary boundary was split or silently expanded", got)
	}
	if _, err = s.RetainNetworkQuality(ctx, q.AsOf.Add(4*time.Hour), policy); err != nil {
		t.Fatal(err)
	}
	got, err = s.PublicNetworkQuality(ctx, q)
	if err != nil || got.Availability != "untracked_history" || got.Coverage != nil {
		t.Fatal("expired history became missing or healthy", got, err)
	}
}

func TestNetworkQualityArchiveRollbackAndCorruption(t *testing.T) {
	s, q, policy := networkArchiveFixture(t)
	ctx := context.Background()
	if _, err := s.db.Exec(`CREATE TRIGGER synthetic_network_archive_failure BEFORE DELETE ON network_probe_tasks BEGIN SELECT RAISE(ABORT,'synthetic archive failure'); END`); err != nil {
		t.Fatal(err)
	}
	batch, err := s.RetainNetworkQuality(ctx, q.AsOf, policy)
	if err == nil || batch != (NetworkQualityRetentionBatch{}) {
		t.Fatal("failed transaction reported committed work", batch, err)
	}
	assertCount(t, s, `SELECT COUNT(*) FROM network_quality_rollups`, 0)
	assertCount(t, s, `SELECT raw_floor FROM network_quality_history_state`, 0)
	if _, err = s.db.Exec(`DROP TRIGGER synthetic_network_archive_failure`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.RetainNetworkQuality(ctx, q.AsOf, policy); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE network_quality_rollups SET summary=zeroblob(length(summary))`); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PublicNetworkQuality(ctx, q); !errors.Is(err, ErrNetworkQualityUnavailable) {
		t.Fatal("corrupt archive became partial healthy data", err)
	}
}

func TestNetworkQualityConcurrentArchiveReadsAndLiveWrites(t *testing.T) {
	s, q, policy := networkArchiveFixture(t)
	ctx := context.Background()
	want, err := s.PublicNetworkQuality(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	errs := make(chan error, 16)
	for range 3 {
		wg.Go(func() {
			for range 4 {
				got, err := s.PublicNetworkQuality(ctx, q)
				if err != nil {
					errs <- err
					return
				}
				if got.Planned != want.Planned || got.Success != want.Success || got.Missing != want.Missing || got.JitterPairs != want.JitterPairs {
					errs <- fmt.Errorf("mixed snapshot counts during archival")
					return
				}
			}
		})
	}
	wg.Go(func() {
		for range 4 {
			b, err := s.RetainNetworkQuality(ctx, q.AsOf, policy)
			if err != nil {
				errs <- err
				return
			}
			if !b.More {
				return
			}
		}
	})
	// A current task remains claimable and accepts its result during old archival.
	wg.Go(func() {
		var epoch string
		if err := s.db.QueryRow(`SELECT id FROM network_schedule_epochs WHERE node_id=? AND ends_at IS NULL`, q.NodeID).Scan(&epoch); err != nil {
			errs <- err
			return
		}
		slot := q.AsOf
		task, err := s.ClaimNetworkTask(ctx, epoch, slot, slot.Add(time.Millisecond))
		if err != nil {
			errs <- err
			return
		}
		_, err = s.AcceptNetworkResult(ctx, q.NodeID, q.Kind, protocol.LatencyResult{TaskID: task.Task.ID, TargetID: q.TargetID, Success: true, LatencyMS: 11, CompletedAt: slot.Add(time.Second)}, slot.Add(time.Second))
		if err != nil {
			errs <- err
		}
	})
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

func TestNetworkQualityRollupMigrationRollback(t *testing.T) {
	s, _, _, _, _ := networkTaskFixture(t)
	if _, err := s.db.Exec(`DROP TABLE network_quality_rollups;DROP TABLE network_quality_history_state;DROP INDEX idx_network_tasks_scheduled_retention;DROP INDEX idx_network_tasks_manual_retention;DROP INDEX idx_network_epochs_retention;DELETE FROM schema_migrations WHERE version='044_network_quality_rollups.sql';CREATE TRIGGER synthetic_044_failure BEFORE INSERT ON schema_migrations WHEN NEW.version='044_network_quality_rollups.sql' BEGIN SELECT RAISE(ABORT,'synthetic migration failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err == nil {
		t.Fatal("migration failure not injected")
	}
	assertCount(t, s, `SELECT COUNT(*) FROM sqlite_master WHERE name IN ('network_quality_rollups','network_quality_history_state','idx_network_tasks_scheduled_retention')`, 0)
	if _, err := s.db.Exec(`DROP TRIGGER synthetic_044_failure`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(context.Background()); err != nil {
		t.Fatal(err)
	}
	assertCount(t, s, `SELECT COUNT(*) FROM network_quality_history_state WHERE raw_floor=0 AND minute_floor=0 AND retained_from=0`, 1)
}

func TestNetworkQualityArchiveFullRestoreAndEpochBreak(t *testing.T) {
	s, q, policy := networkArchiveFixture(t)
	ctx := context.Background()
	before, err := s.PublicNetworkQuality(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	var epoch string
	if err = s.db.QueryRow(`SELECT id FROM network_schedule_epochs WHERE node_id=?`, q.NodeID).Scan(&epoch); err != nil {
		t.Fatal(err)
	}
	boundary := q.Start.Add(20 * time.Minute).Unix()
	for _, statement := range []struct {
		query string
		args  []any
	}{
		{`UPDATE network_schedule_epochs SET ends_at=? WHERE id=?`, []any{boundary, epoch}},
		{`INSERT INTO network_schedule_epochs(id,node_id,target_id,kind,host,port,interval_seconds,timeout_ms,starts_at) SELECT 'synthetic-second-epoch',node_id,target_id,kind,host,port,interval_seconds,timeout_ms,? FROM network_schedule_epochs WHERE id=?`, []any{boundary, epoch}},
		{`UPDATE network_probe_tasks SET epoch_id='synthetic-second-epoch' WHERE epoch_id=? AND scheduled_at>=?`, []any{epoch, boundary}},
	} {
		if _, err = s.db.Exec(statement.query, statement.args...); err != nil {
			t.Fatal(err)
		}
	}
	want, err := s.PublicNetworkQuality(ctx, q)
	if err != nil || want.JitterPairs+1 != before.JitterPairs || want.Planned != before.Planned {
		t.Fatalf("epoch boundary pair mismatch: before planned=%d pairs=%d, after planned=%d pairs=%d: %v", before.Planned, before.JitterPairs, want.Planned, want.JitterPairs, err)
	}
	for range 10 {
		batch, err := s.RetainNetworkQuality(ctx, q.AsOf, policy)
		if err != nil {
			t.Fatal(err)
		}
		if !batch.More {
			break
		}
	}
	got, err := s.PublicNetworkQuality(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	assertNetworkQualityEquivalent(t, want, got)
	snapshot := filepath.Join(t.TempDir(), "network-quality-snapshot.db")
	if err = s.ConsistentBackup(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "network-quality-restored.db")
	if _, err = StageDatabaseRestore(ctx, path, snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err = ApplyPendingRestore(ctx, path, q.AsOf); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	actual, err := restored.PublicNetworkQuality(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	assertNetworkQualityEquivalent(t, got, actual)
	if !actual.AvailableFrom.Equal(got.AvailableFrom) || actual.GranularitySeconds != got.GranularitySeconds || actual.QuantileMethod != got.QuantileMethod {
		t.Fatal("restore changed history provenance")
	}
	if _, err = restored.ReadRecoveredState(ctx); err != nil {
		t.Fatal(err)
	}
	// Closed plans survive while retained data exists, and are removed only
	// after raw tasks and both rollup tiers have expired.
	assertCount(t, restored, `SELECT COUNT(*) FROM network_schedule_epochs WHERE id='synthetic-second-epoch' OR ends_at IS NOT NULL`, 2)
	for range 10 {
		batch, err := restored.RetainNetworkQuality(ctx, q.AsOf.Add(4*time.Hour), policy)
		if err != nil {
			t.Fatal(err)
		}
		if !batch.More {
			break
		}
	}
	assertCount(t, restored, `SELECT COUNT(*) FROM network_schedule_epochs WHERE ends_at IS NOT NULL`, 0)
	assertCount(t, restored, `SELECT COUNT(*) FROM network_schedule_epochs WHERE ends_at IS NULL`, 1)
}

func TestNetworkQualityShortPolicyPreservesAcceptingTasks(t *testing.T) {
	s, node, target, epoch, now := networkTaskFixture(t)
	slot := now.Truncate(time.Second)
	task, err := s.ClaimNetworkTask(context.Background(), epoch, slot, now)
	if err != nil {
		t.Fatal(err)
	}
	policy := RetentionPolicy{Raw: time.Nanosecond, OneMinute: time.Nanosecond, FiveMinute: time.Nanosecond}
	if _, err = s.RetainNetworkQuality(context.Background(), now, policy); err != nil {
		t.Fatal(err)
	}
	if accepted, err := s.AcceptNetworkResult(context.Background(), node, target.Kind, protocol.LatencyResult{TaskID: task.Task.ID, TargetID: target.ID, Success: true, LatencyMS: 5, CompletedAt: slot.Add(time.Second)}, slot.Add(time.Second)); err != nil || !accepted {
		t.Fatal("retention removed an accepting task", err)
	}
}

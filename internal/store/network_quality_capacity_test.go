package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/shirou/gopsutil/v4/disk"
	quality "github.com/zhengyifei200112-collab/myprobe/internal/networkquality"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

type networkArchiveCapacityEvidence struct {
	Format                                                             string `json:"format"`
	SourceCommit                                                       string `json:"source_commit,omitempty"`
	Platform                                                           string `json:"platform"`
	GoVersion                                                          string `json:"go_version"`
	Slots, SeededTasks, ArchivePasses, RawBuckets, MinuteBuckets       int64
	SeedSeconds, DrainSeconds, MaximumBatchMS                          float64
	ConcurrentArchivePasses                                            int64
	ConcurrentPhaseSeconds                                             float64
	WriterSamples                                                      int
	WriterP95MS, WriterMaximumMS                                       float64
	CurrentQuerySamples                                                int
	YearSeriesMS                                                       float64
	YearSeriesPoints                                                   int
	CurrentQueryP95MS                                                  float64
	YearQueryMS, ThirtyDayQueryMS, RestoreQueryMS                      float64
	RawRows, MinuteRows, FiveMinuteRows, SummaryPayloadBytes           int64
	DatabaseAllocatedBytes, DatabaseReusableBytes, SampledPeakWALBytes int64
	FinalDatabaseFileBytes, FinalWALFileBytes, SnapshotFileBytes       int64
	PeakGoHeapAllocBytes                                               uint64
	ResourceSamples                                                    int64
	Planned, Success, Failure, Missing, JitterPairs                    uint64
	QuantileMethod                                                     string
	P50Bounds, P95Bounds                                               *quality.Bounds
	Scope                                                              string
}

func capacityP95(values []float64) float64 {
	if len(values) == 0 {
		return 0
	}
	ordered := append([]float64(nil), values...)
	sort.Float64s(ordered)
	return ordered[(len(ordered)*95+99)/100-1]
}

// This is a real SQLite storage/competition/restore experiment with synthetic
// completed task identities. It does not claim a year of live Agent traffic.
// First measure concurrent writes/reads using production's 100ms pacing, then
// stop those probes and fast-drain the remaining year to verify storage/math.
// Never attribute unpaced-drain throughput to the production worker.
func TestNetworkQualityArchiveYearCapacity(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_NETWORK_ARCHIVE_CAPACITY") != "1" {
		t.Skip("opt-in year network ledger capacity")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()
	s, node, target, epoch, _ := networkTaskFixture(t)
	usage, err := disk.Usage(filepath.Dir(s.path))
	if err != nil || usage.Free < 4<<30 {
		t.Fatal("disposable capacity volume requires 4 GiB free", err)
	}
	anchor := time.Now().UTC().Truncate(5 * time.Minute).Add(-365 * 24 * time.Hour)
	if _, err = s.db.Exec(`UPDATE network_schedule_epochs SET starts_at=?,interval_seconds=60 WHERE id=?`, anchor.Unix(), epoch); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE network_schedule_metadata SET known_since=?,known_results_since=?`, anchor.Unix(), anchor.Unix()); err != nil {
		t.Fatal(err)
	}
	q := NetworkQualityQuery{NodeID: node, TargetID: target.ID, Kind: target.Kind, Start: anchor, End: anchor.Add(365 * 24 * time.Hour), AsOf: anchor.Add(365*24*time.Hour + 75*time.Second)}
	evidence := networkArchiveCapacityEvidence{Format: "myprobe.network-archive-capacity.v1", SourceCommit: os.Getenv("MYPROBE_TEST_SOURCE_COMMIT"), Platform: runtime.GOOS + "/" + runtime.GOARCH, GoVersion: runtime.Version(), Slots: 365 * 24 * 60, Scope: "one synthetic node/target; real SQLite Store archival; current manual task/result writes and queries overlap first 100 batches at production 100ms pacing, then probes stop and remaining history fast-drains without production pacing; direct SQL historical seed, no live Gateway/main; Go heap and sampled WAL are not process RSS or exact WAL peak"}
	var resourceMu sync.Mutex
	resourceDone := make(chan struct{})
	resourceStopped := make(chan struct{})
	go func() {
		defer close(resourceStopped)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-resourceDone:
				return
			case <-ticker.C:
				var m runtime.MemStats
				runtime.ReadMemStats(&m)
				var wal int64
				if info, err := os.Stat(s.path + "-wal"); err == nil {
					wal = info.Size()
				}
				resourceMu.Lock()
				evidence.PeakGoHeapAllocBytes = max(evidence.PeakGoHeapAllocBytes, m.HeapAlloc)
				evidence.SampledPeakWALBytes = max(evidence.SampledPeakWALBytes, wal)
				evidence.ResourceSamples++
				resourceMu.Unlock()
			}
		}
	}()
	defer func() { close(resourceDone); <-resourceStopped }()
	// Independent oracle: fixed integer frequencies and closed-form counts;
	// never use the production histogram/sequence merge to predict itself.
	var rttFrequency [1000]uint64
	var expectedRTTSum float64
	started := time.Now()
	tx, err := s.networkWrite(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	insert, err := tx.PrepareContext(ctx, `INSERT INTO network_probe_tasks(id,source,epoch_id,node_id,target_id,scheduled_at,kind,host,timeout_ms,dispatch_until_ns,expires_at_ns,created_at_ns,state,lease_token,lease_until_ns,dispatch_reason,received_at_ns,result_json,result_sha,outcome) VALUES(?,'scheduled',?,?,?,?,?,?,1000,?,?,?,?,'synthetic-capacity',?,?, ?,?,?,?)`)
	if err != nil {
		t.Fatal(err)
	}
	for i := int64(0); i < evidence.Slots; i++ {
		if i%20 == 3 {
			continue
		}
		slot := anchor.Add(time.Duration(i) * time.Minute)
		id := fmt.Sprintf("synthetic-year-%06d", i)
		outcome := quality.Success
		reason := ""
		rtt := 1 + float64(i%1000)/10
		state := "completed"
		dispatch := ""
		switch i % 20 {
		case 0:
			outcome = quality.Failure
			reason = "timeout"
			rtt = 0
		case 1:
			outcome = quality.NotExecuted
			reason = "busy"
			rtt = 0
		case 2:
			outcome = quality.Unobserved
			reason = "offline"
			rtt = 0
			state = "ready"
			dispatch = "offline"
		}
		var raw, sha, storedOutcome any
		var received any
		if state == "completed" {
			r := protocol.LatencyResult{TaskID: id, TargetID: target.ID, Success: outcome == quality.Success, LatencyMS: rtt, ErrorClass: reason, CompletedAt: slot.Add(time.Second)}
			encoded, err := json.Marshal(r)
			if err != nil {
				t.Fatal(err)
			}
			sum := sha256.Sum256(encoded)
			raw, sha, storedOutcome, received = string(encoded), hex.EncodeToString(sum[:]), string(outcome), slot.Add(time.Second).UnixNano()
		}
		if _, err = insert.ExecContext(ctx, id, epoch, node, target.ID, slot.Unix(), target.Kind, target.Host, slot.Add(5*time.Second).UnixNano(), slot.Add(16*time.Second).UnixNano(), slot.UnixNano(), state, slot.Add(5*time.Second).UnixNano(), dispatch, received, raw, sha, storedOutcome); err != nil {
			t.Fatal(err)
		}
		if outcome == quality.Success {
			rttFrequency[i%1000]++
			expectedRTTSum += rtt
		}
		evidence.SeededTasks++
		if i > 0 && i%100000 == 0 {
			t.Logf("seeded slots=%d tasks=%d elapsed=%s", i, evidence.SeededTasks, time.Since(started))
		}
	}
	insert.Close()
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	evidence.SeedSeconds = time.Since(started).Seconds()
	t.Logf("seed complete tasks=%d elapsed=%s", evidence.SeededTasks, time.Since(started))
	for _, statement := range []struct {
		name, query string
		args        []any
	}{
		{"manual expiry", `SELECT id FROM network_probe_tasks WHERE source='manual' AND expires_at_ns<? ORDER BY expires_at_ns LIMIT ?`, []any{q.AsOf.Add(-7 * 24 * time.Hour).UnixNano(), 1000}},
		{"scheduled expiry", `SELECT id FROM network_probe_tasks WHERE source='scheduled' AND scheduled_at<? ORDER BY scheduled_at LIMIT ?`, []any{q.Start.Unix(), 1000}},
		{"raw bucket", `SELECT t.epoch_id,t.scheduled_at,e.target_id,e.starts_at,e.interval_seconds FROM network_probe_tasks t JOIN network_schedule_epochs e ON e.id=t.epoch_id WHERE t.source='scheduled' AND t.scheduled_at>=? AND t.scheduled_at<? ORDER BY t.scheduled_at,t.epoch_id LIMIT 1`, []any{q.Start.Unix(), q.End.Add(-7 * 24 * time.Hour).Unix()}},
	} {
		rows, err := s.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+statement.query, statement.args...)
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			var id, parent, unused int
			var detail string
			if err = rows.Scan(&id, &parent, &unused, &detail); err != nil {
				t.Fatal(err)
			}
			t.Logf("query plan %s: %s", statement.name, detail)
		}
		rows.Close()
	}
	// An unarchived year exceeds the explicit query work budget. It must fail,
	// not return a percentile based on its first 350000 rows.
	if _, err = s.PublicNetworkQuality(ctx, q); !errors.Is(err, ErrNetworkQualityUnavailable) {
		t.Fatal("raw query budget was silently truncated", err)
	}
	var probeMu sync.Mutex
	var writeMS, readMS []float64
	probeDone := make(chan struct{})
	probeStopped := make(chan struct{})
	probeError := make(chan error, 1)
	go func() {
		defer close(probeStopped)
		ticker := time.NewTicker(100 * time.Millisecond)
		defer ticker.Stop()
		sequence := 0
		for {
			select {
			case <-probeDone:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				sequence++
				now := time.Now().UTC()
				probeCtx, probeCancel := context.WithTimeout(ctx, 2*time.Second)
				began := time.Now()
				task, err := s.RecordManualNetworkTask(probeCtx, node, protocol.Task{ID: fmt.Sprintf("synthetic-current-%06d", sequence), Kind: target.Kind, TargetID: target.ID, Host: target.Host, TimeoutMS: 1000, ExpiresAt: now.Add(16 * time.Second)}, now)
				if err != nil {
					err = fmt.Errorf("record current task: %w", err)
				}
				if err == nil {
					_, err = s.AcceptNetworkResult(probeCtx, node, target.Kind, protocol.LatencyResult{TaskID: task.Task.ID, TargetID: target.ID, Success: true, LatencyMS: 3, CompletedAt: now}, now)
					if err != nil {
						err = fmt.Errorf("accept current result: %w", err)
					}
				}
				elapsed := float64(time.Since(began)) / float64(time.Millisecond)
				if err != nil {
					probeCancel()
					probeError <- err
					return
				}
				probeMu.Lock()
				writeMS = append(writeMS, elapsed)
				probeMu.Unlock()
				if sequence%10 == 0 {
					current := q
					current.Start = q.End.Add(-5 * time.Minute)
					began = time.Now()
					_, err = s.PublicNetworkQuality(probeCtx, current)
					elapsed = float64(time.Since(began)) / float64(time.Millisecond)
					if err != nil {
						probeCancel()
						probeError <- fmt.Errorf("read current quality: %w", err)
						return
					}
					probeMu.Lock()
					readMS = append(readMS, elapsed)
					probeMu.Unlock()
				}
				probeCancel()
			}
		}
	}()
	stopped := false
	stopProbes := func() {
		if !stopped {
			close(probeDone)
			<-probeStopped
			stopped = true
		}
	}
	defer func() {
		stopProbes()
	}()
	started = time.Now()
	for {
		passStart := time.Now()
		batch, err := s.RetainNetworkQuality(ctx, q.AsOf, DefaultRetentionPolicy())
		if err != nil {
			t.Fatal(err)
		}
		evidence.ArchivePasses++
		evidence.RawBuckets += batch.RawBuckets
		evidence.MinuteBuckets += batch.MinuteBuckets
		evidence.MaximumBatchMS = max(evidence.MaximumBatchMS, float64(time.Since(passStart))/float64(time.Millisecond))
		if evidence.ArchivePasses <= 10 {
			t.Logf("initial batch pass=%d elapsed=%s raw=%d minute=%d", evidence.ArchivePasses, time.Since(passStart), batch.RawBuckets, batch.MinuteBuckets)
		}
		if evidence.ArchivePasses%2000 == 0 {
			t.Logf("archive passes=%d raw_buckets=%d elapsed=%s", evidence.ArchivePasses, evidence.RawBuckets, time.Since(started))
		}
		if evidence.ArchivePasses <= 100 {
			evidence.ConcurrentArchivePasses++
		}
		if evidence.ArchivePasses == 100 {
			stopProbes()
			evidence.ConcurrentPhaseSeconds = time.Since(started).Seconds()
		}
		select {
		case err := <-probeError:
			t.Fatal("current result/query failed during archival", err)
		default:
		}
		if !batch.More {
			break
		}
		if evidence.ArchivePasses < 100 {
			time.Sleep(100 * time.Millisecond)
		} else {
			runtime.Gosched()
		}
	}
	evidence.DrainSeconds = time.Since(started).Seconds()
	stopProbes()
	select {
	case err := <-probeError:
		t.Fatal(err)
	default:
	}
	probeMu.Lock()
	evidence.WriterSamples = len(writeMS)
	evidence.WriterP95MS = capacityP95(writeMS)
	for _, v := range writeMS {
		evidence.WriterMaximumMS = max(evidence.WriterMaximumMS, v)
	}
	evidence.CurrentQuerySamples = len(readMS)
	evidence.CurrentQueryP95MS = capacityP95(readMS)
	probeMu.Unlock()
	if evidence.WriterSamples == 0 || evidence.CurrentQuerySamples == 0 {
		t.Fatal("capacity experiment did not overlap current writes and queries")
	}
	started = time.Now()
	got, err := s.PublicNetworkQuality(ctx, q)
	evidence.YearQueryMS = float64(time.Since(started)) / float64(time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	groups := uint64(evidence.Slots / 20)
	var causes [len(quality.CauseNames)]uint64
	causes[0], causes[4], causes[7] = groups, groups, groups
	if got.Planned != uint64(evidence.Slots) || got.Success != 16*groups || got.Failure != groups || got.Missing != 3*groups || got.NotExecuted != groups || got.ObservedMissing != groups || got.JitterPairs != 15*groups || got.Totals.Causes != causes || math.Abs(got.Totals.JitterSum-1.5*float64(groups)) > 1e-5 || math.Abs(got.Totals.RTTSum-expectedRTTSum) > 1e-4 {
		t.Fatal("year mixed-tier statistics differ from generated slot oracle")
	}
	if got.Coverage == nil || math.Abs(*got.Coverage-85) > 1e-9 || got.FailureRate == nil || math.Abs(*got.FailureRate-100.0/17) > 1e-9 {
		t.Fatal("year rates differ from independent 20-slot pattern")
	}
	for _, p := range []struct {
		percent int
		actual  *quality.Bounds
	}{{50, got.P50Bounds}, {95, got.P95Bounds}} {
		rank := (got.Success*uint64(p.percent) + 99) / 100
		var seen uint64
		var exact float64
		for value, count := range rttFrequency {
			seen += count
			if seen >= rank {
				exact = 1 + float64(value)/10
				break
			}
		}
		if p.actual == nil || exact < p.actual.LowerMS || exact > p.actual.UpperMS {
			t.Fatal("year distribution excludes independently ranked true quantile")
		}
	}
	evidence.Planned, evidence.Success, evidence.Failure, evidence.Missing, evidence.JitterPairs = got.Planned, got.Success, got.Failure, got.Missing, got.JitterPairs
	evidence.QuantileMethod, evidence.P50Bounds, evidence.P95Bounds = got.QuantileMethod, got.P50Bounds, got.P95Bounds
	seriesQuery := q
	seriesQuery.Series = true
	started = time.Now()
	series, err := s.PublicNetworkQuality(ctx, seriesQuery)
	evidence.YearSeriesMS = float64(time.Since(started)) / float64(time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	evidence.YearSeriesPoints = len(series.Points)
	if len(series.Points) == 0 || len(series.Points) > MaxNetworkQualityPoints {
		t.Fatal("year series exceeded point budget")
	}
	assertNetworkQualityEquivalent(t, got, series)
	var pointPlan, pointSuccess, pointFailure, pointMissing uint64
	for _, point := range series.Points {
		pointPlan += point.Stats.Planned
		pointSuccess += point.Stats.Success
		pointFailure += point.Stats.Failure
		pointMissing += point.Stats.Missing
	}
	if pointPlan != got.Planned || pointSuccess != got.Success || pointFailure != got.Failure || pointMissing != got.Missing {
		t.Fatal("year points lost or duplicated planned population")
	}
	thirty := q
	thirty.Start = q.End.Add(-30 * 24 * time.Hour)
	started = time.Now()
	_, err = s.PublicNetworkQuality(ctx, thirty)
	evidence.ThirtyDayQueryMS = float64(time.Since(started)) / float64(time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow(`SELECT (SELECT COUNT(*) FROM network_probe_tasks WHERE source='scheduled'),COALESCE(SUM(bucket_seconds=60),0),COALESCE(SUM(bucket_seconds=300),0),COALESCE(SUM(length(summary)),0) FROM network_quality_rollups`).Scan(&evidence.RawRows, &evidence.MinuteRows, &evidence.FiveMinuteRows, &evidence.SummaryPayloadBytes); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRow(`SELECT (SELECT page_count FROM pragma_page_count)*(SELECT page_size FROM pragma_page_size),(SELECT freelist_count FROM pragma_freelist_count)*(SELECT page_size FROM pragma_page_size)`).Scan(&evidence.DatabaseAllocatedBytes, &evidence.DatabaseReusableBytes); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(s.path); err == nil {
		evidence.FinalDatabaseFileBytes = info.Size()
	}
	if info, err := os.Stat(s.path + "-wal"); err == nil {
		evidence.FinalWALFileBytes = info.Size()
	}
	snapshot := filepath.Join(t.TempDir(), "year-snapshot.db")
	if err = s.ConsistentBackup(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(snapshot); err == nil {
		evidence.SnapshotFileBytes = info.Size()
	}
	restoredPath := filepath.Join(t.TempDir(), "year-restored.db")
	if _, err = StageDatabaseRestore(ctx, restoredPath, snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err = ApplyPendingRestore(ctx, restoredPath, q.AsOf); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(ctx, restoredPath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	started = time.Now()
	actual, err := restored.PublicNetworkQuality(ctx, q)
	evidence.RestoreQueryMS = float64(time.Since(started)) / float64(time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	assertNetworkQualityEquivalent(t, got, actual)
	if _, err = restored.ReadRecoveredState(ctx); err != nil {
		t.Fatal(err)
	}
	resourceMu.Lock()
	encoded, err := json.MarshalIndent(evidence, "", "  ")
	resourceMu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if output := os.Getenv("MYPROBE_TEST_NETWORK_ARCHIVE_OUTPUT"); output != "" {
		if err = os.WriteFile(output, append(encoded, '\n'), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Logf("capacity evidence: %s", encoded)
}

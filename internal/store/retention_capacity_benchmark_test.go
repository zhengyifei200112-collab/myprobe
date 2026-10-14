package store

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
	"time"
)

// Dense file-backed Store evidence; no live Gateway, HTTP API or 24-hour claim.
func TestHistoryRetentionDenseCapacity(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_HISTORY_CAPACITY") != "1" {
		t.Skip("dense disposable SQLite capacity fixture is opt-in")
	}
	count := 5
	variant := os.Getenv("MYPROBE_TEST_HISTORY_VARIANT")
	if variant == "" {
		variant = "full"
	}
	if variant != "baseline" && variant != "lookup" && variant != "full" {
		t.Fatal("invalid capacity variant")
	}
	if value := os.Getenv("MYPROBE_TEST_HISTORY_NODES"); value != "" {
		var err error
		count, err = strconv.Atoi(value)
		if err != nil {
			t.Fatal(err)
		}
	}
	s, now, ids := historyCapacityFixture(t, count)
	ctx := context.Background()
	fileSize := func(name string) int64 {
		info, err := os.Stat(name)
		if err != nil {
			return 0
		}
		return info.Size()
	}
	beforeDB := fileSize(s.path)
	indexStarted := time.Now()
	if variant != "baseline" {
		definitions, err := migrations.ReadFile("migrations/041_history_retention_indexes.sql")
		if err != nil {
			t.Fatal(err)
		}
		var statements []string
		for _, statement := range strings.Split(string(definitions), ";") {
			var lines []string
			for _, line := range strings.Split(statement, "\n") {
				if !strings.HasPrefix(strings.TrimSpace(line), "--") {
					lines = append(lines, line)
				}
			}
			clean := strings.TrimSpace(strings.Join(lines, "\n"))
			if strings.HasPrefix(clean, "CREATE INDEX") {
				statements = append(statements, clean)
			}
		}
		if variant == "lookup" {
			statements = statements[:3]
		}
		for _, statement := range statements {
			if strings.TrimSpace(statement) == "" {
				continue
			}
			if _, err := s.db.ExecContext(ctx, statement); err != nil {
				t.Fatal(err)
			}
		}
	}
	indexElapsed := time.Since(indexStarted)
	if _, err := s.db.ExecContext(ctx, "PRAGMA wal_checkpoint(TRUNCATE)"); err != nil {
		t.Fatal(err)
	}
	indexedDB := fileSize(s.path)
	stop, done := make(chan struct{}), make(chan struct{})
	writeTimes := []float64{}
	successful, failed := 0, 0
	go func() {
		defer close(done)
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		sequence := 0
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
			}
			started := time.Now()
			err := s.SaveReport(ctx, ids[sequence%len(ids)], historyCapacityReport(time.Now().UTC(), historyRawPerNode+1+sequence/len(ids)))
			writeTimes = append(writeTimes, float64(time.Since(started))/float64(time.Millisecond))
			sequence++
			if err != nil {
				failed++
			} else {
				successful++
			}
		}
	}()
	operationCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	started := time.Now()
	statements := standardRetentionStatements()
	if variant != "full" {
		statements = legacyHistoryStatements()
	}
	err := s.applyRetentionStatements(operationCtx, now, DefaultRetentionPolicy(), statements)
	elapsed := time.Since(started)
	cancel()
	close(stop)
	<-done
	status := "success"
	if err != nil {
		status = "failed_or_cancelled"
	}
	counts := map[string]int64{}
	for _, table := range []string{"metric_samples", "latency_samples", "metric_rollups", "latency_rollups", "traffic_rollups"} {
		var count int64
		if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
			t.Fatal(err)
		}
		counts[table] = count
	}
	wantRaw := int64(count*(historyRawPerNode+1) + successful)
	wantLatency := int64(count * (7*24*60 + 60) * 3)
	if status == "success" {
		wantRaw -= int64(count * 240)
		wantLatency -= int64(count * 180)
	}
	if counts["metric_samples"] != wantRaw || counts["latency_samples"] != wantLatency {
		t.Fatal("retention did not atomically preserve generated/writer rows")
	}
	for _, table := range []string{"metric_rollups", "traffic_rollups", "latency_rollups"} {
		want := int64(count * (historyMinutePerNode + historyFiveMinutePerNode))
		if table == "latency_rollups" {
			want *= 3
		}
		if counts[table] != want {
			t.Fatalf("%s lost or duplicated rollup buckets", table)
		}
	}
	sort.Float64s(writeTimes)
	var writeP95, writeMax float64
	if len(writeTimes) > 0 {
		writeP95 = writeTimes[(len(writeTimes)*95+99)/100-1]
		writeMax = writeTimes[len(writeTimes)-1]
	}
	evidence := map[string]any{"schema": "myprobe.history_retention_capacity.v1", "nodes": count, "targets_per_node": 3, "report_seconds": 15, "target_seconds": 60, "raw_days": 7, "minute_days": 23, "five_minute_days": 335, "catchup_seconds": 3600, "elapsed_ms": float64(elapsed) / float64(time.Millisecond), "retention_status": status, "successful_report_writes": successful, "failed_report_writes": failed, "report_write_p95_ms": writeP95, "report_write_max_ms": writeMax, "database_before_bytes": beforeDB, "database_after_bytes": fileSize(s.path), "wal_after_bytes": fileSize(s.path + "-wal"), "after_counts": counts, "go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "logical_cpus": runtime.NumCPU()}
	evidence["variant"] = variant
	evidence["index_build_ms"] = float64(indexElapsed) / float64(time.Millisecond)
	evidence["database_after_indexes_bytes"] = indexedDB
	definitions, definitionErr := migrations.ReadFile("migrations/041_history_retention_indexes.sql")
	if definitionErr != nil {
		t.Fatal(definitionErr)
	}
	definitionHash := sha256.Sum256(definitions)
	queryHash := sha256.Sum256([]byte(strings.Join([]string{statements.rawMetrics, statements.rawLatency, statements.rawTraffic, statements.minuteMetrics, statements.minuteLatency, statements.minuteTraffic, statements.pruneMetrics}, "\x00")))
	evidence["migration_sha256"] = fmt.Sprintf("%x", definitionHash)
	evidence["queries_sha256"] = fmt.Sprintf("%x", queryHash)
	evidence["recorded_at"] = time.Now().UTC()
	evidence["node_id_length"] = len(ids[0])
	encoded, encodeErr := json.Marshal(evidence)
	if encodeErr != nil {
		t.Fatal(encodeErr)
	}
	t.Log(string(encoded))
	if output := os.Getenv("MYPROBE_TEST_HISTORY_OUTPUT"); output != "" {
		if err := os.WriteFile(output, append(encoded, '\n'), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	if err != nil {
		t.Logf("measured retention did not complete: %v", err)
	}
	if len(writeTimes) == 0 {
		t.Fatal("report writer did not run")
	}
	if variant == "full" && (err != nil || failed != 0) {
		t.Fatal("optimized retention or concurrent report writes failed")
	}
}

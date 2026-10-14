package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"runtime"
	"sort"
	"testing"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

// Isolated disk SQLite cleanup with concurrent synthetic reports. It does not
// measure live Gateway/Agent, Server RSS, browser load or a 24-hour duration.
func BenchmarkHTTPRetentionCatchup(b *testing.B) {
	for _, size := range []int{10000, 50000, 432000} {
		b.Run(fmt.Sprintf("tasks-%d", size), func(b *testing.B) {
			for iteration := 0; iteration < b.N; iteration++ {
				b.StopTimer()
				s, now := httpRetentionDataset(b, size, 10000, 100, 3)
				ctx := context.Background()
				nodes, err := s.ListNodes(ctx)
				if err != nil {
					b.Fatal(err)
				}
				fileSize := func(name string) int64 {
					info, err := os.Stat(name)
					if err != nil {
						return 0
					}
					return info.Size()
				}
				dbBefore, walBefore := fileSize(s.path), fileSize(s.path+"-wal")
				stop, done := make(chan struct{}), make(chan struct{})
				writes := []float64{}
				var writeErr error
				go func() {
					defer close(done)
					ticker := time.NewTicker(10 * time.Millisecond)
					defer ticker.Stop()
					for {
						select {
						case <-stop:
							return
						case <-ticker.C:
							started := time.Now()
							writeErr = s.SaveReport(ctx, nodes[len(writes)%len(nodes)].ID, protocol.Report{CapturedAt: time.Now().UTC(), CPU: protocol.CPUMetric{UsagePercent: 10}})
							writes = append(writes, float64(time.Since(started))/float64(time.Millisecond))
							if writeErr != nil {
								return
							}
						}
					}
				}()
				// Always join the writer before testing cleanup or fixture teardown.
				var cleanupErr error
				passes, deletedTasks, deletedEpochs := 0, int64(0), int64(0)
				maxBatchMS, peakWAL := float64(0), walBefore
				b.StartTimer()
				started := time.Now()
				for {
					batchStarted := time.Now()
					batch, err := s.PruneHTTPHistory(ctx, now)
					batchMS := float64(time.Since(batchStarted)) / float64(time.Millisecond)
					if batchMS > maxBatchMS {
						maxBatchMS = batchMS
					}
					if err != nil {
						cleanupErr = err
						break
					}
					passes++
					deletedTasks += batch.TasksDeleted
					deletedEpochs += batch.EpochsDeleted
					if wal := fileSize(s.path + "-wal"); wal > peakWAL {
						peakWAL = wal
					}
					if !batch.More {
						break
					}
					time.Sleep(100 * time.Millisecond)
				}
				elapsed := time.Since(started)
				b.StopTimer()
				close(stop)
				<-done
				if cleanupErr != nil {
					b.Fatal(cleanupErr)
				}
				if writeErr != nil {
					b.Fatal(writeErr)
				}
				if deletedTasks != int64(size+1) || deletedEpochs != 10001 {
					b.Fatalf("lost retention rows: %d %d", deletedTasks, deletedEpochs)
				}
				var remainingTasks, remainingEpochs, freePages int64
				if err := s.db.QueryRow(`SELECT COUNT(*) FROM http_tasks`).Scan(&remainingTasks); err != nil {
					b.Fatal(err)
				}
				if err := s.db.QueryRow(`SELECT COUNT(*) FROM http_schedule_epochs`).Scan(&remainingEpochs); err != nil {
					b.Fatal(err)
				}
				if err := s.db.QueryRow(`PRAGMA freelist_count`).Scan(&freePages); err != nil {
					b.Fatal(err)
				}
				if remainingTasks != 3 || remainingEpochs != 301 || len(writes) == 0 {
					b.Fatalf("retained data/report writes: %d %d %d", remainingTasks, remainingEpochs, len(writes))
				}
				sort.Float64s(writes)
				p95 := writes[(len(writes)*95+99)/100-1]
				evidence := map[string]any{
					"batch_limit": httpRetentionBatchSize, "tasks": size, "closed_epochs": 10000, "nodes": 100, "services": 3, "passes": passes,
					"deleted_tasks": deletedTasks, "deleted_epochs": deletedEpochs, "elapsed_ms": float64(elapsed) / float64(time.Millisecond), "max_batch_ms": maxBatchMS,
					"successful_report_writes": len(writes), "report_write_p95_ms": p95, "report_write_max_ms": writes[len(writes)-1],
					"database_before_bytes": dbBefore, "database_after_bytes": fileSize(s.path), "allocated_wal_peak_bytes": peakWAL, "free_pages_after": freePages,
					"go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH, "logical_cpus": runtime.NumCPU(),
				}
				encoded, err := json.Marshal(evidence)
				if err != nil {
					b.Fatal(err)
				}
				b.Log(string(encoded))
				b.ReportMetric(maxBatchMS, "max-batch-ms")
				b.ReportMetric(float64(deletedTasks)/elapsed.Seconds(), "tasks/s")
			}
		})
	}
}

package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"path/filepath"
	"reflect"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"modernc.org/sqlite"
)

func historyReadFileFixture(t *testing.T, options OpenOptions) (*Store, Node, time.Time) {
	t.Helper()
	original, node, base := rawCoveringFixture(t)
	path := filepath.Join(t.TempDir(), "history # ' read.db")
	if err := original.ConsistentBackup(context.Background(), path); err != nil {
		t.Fatal(err)
	}
	database, err := OpenWithOptions(context.Background(), path, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	return database, node, base
}

func TestHistoryReadFileResultsMatchWriterConnection(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		name := "mapped"
		if disabled {
			name = "buffered"
		}
		t.Run(name, func(t *testing.T) {
			database, node, base := historyReadFileFixture(t, OpenOptions{DisableHistoryMmap: disabled})
			legacy := &Store{db: database.db, path: database.path}
			ctx := context.Background()
			for _, phase := range []string{"original", "updated", "hidden", "deleted"} {
				switch phase {
				case "updated":
					if _, err := database.db.Exec(`UPDATE metric_samples SET cpu_usage=cpu_usage+0.25,net_rx_total=net_rx_total+3 WHERE node_id=?; UPDATE targets SET name='changed after capture',enabled=0`, node.ID); err != nil {
						t.Fatal(err)
					}
				case "hidden":
					if _, err := database.db.Exec("UPDATE nodes SET hidden=1 WHERE id=?", node.ID); err != nil {
						t.Fatal(err)
					}
				case "deleted":
					if _, err := database.db.Exec("DELETE FROM nodes WHERE id=?", node.ID); err != nil {
						t.Fatal(err)
					}
				}
				for _, admin := range []bool{false, true} {
					for _, window := range [][2]time.Time{{base, base.Add(5 * time.Minute)}, {base.Add(time.Nanosecond), base.Add(time.Second)}} {
						metric, latency, traffic := database.MetricHistoryRange, database.LatencyHistoryRange, database.TrafficHistoryRange
						oldMetric, oldLatency, oldTraffic := legacy.MetricHistoryRange, legacy.LatencyHistoryRange, legacy.TrafficHistoryRange
						if admin {
							metric, latency, traffic = database.AdminMetricHistoryRange, database.AdminLatencyHistoryRange, database.AdminTrafficHistoryRange
							oldMetric, oldLatency, oldTraffic = legacy.AdminMetricHistoryRange, legacy.AdminLatencyHistoryRange, legacy.AdminTrafficHistoryRange
						}
						m, err := metric(ctx, node.ID, window[0], window[1], 60)
						om, oldErr := oldMetric(ctx, node.ID, window[0], window[1], 60)
						if err != nil || oldErr != nil || !reflect.DeepEqual(m, om) {
							t.Fatalf("%s admin=%v metric mismatch: %v %v", phase, admin, err, oldErr)
						}
						l, err := latency(ctx, node.ID, window[0], window[1], 60)
						ol, oldErr := oldLatency(ctx, node.ID, window[0], window[1], 60)
						if err != nil || oldErr != nil || !reflect.DeepEqual(l, ol) {
							t.Fatalf("%s admin=%v latency mismatch: %v %v", phase, admin, err, oldErr)
						}
						tr, err := traffic(ctx, node.ID, window[0], window[1], 60)
						otr, oldErr := oldTraffic(ctx, node.ID, window[0], window[1], 60)
						if err != nil || oldErr != nil || !reflect.DeepEqual(tr, otr) {
							t.Fatalf("%s admin=%v traffic mismatch: %v %v", phase, admin, err, oldErr)
						}
					}
				}
				stats := database.historyDB.Stats()
				if stats.InUse != 0 || stats.Idle != 0 || stats.OpenConnections != 0 {
					t.Fatalf("%s retained history connections: %+v", phase, stats)
				}
			}
		})
	}
}

func TestHistoryReadPoolIsReadonlyBoundedAndLeavesWriterUsable(t *testing.T) {
	database, node, _ := historyReadFileFixture(t, OpenOptions{})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if database.historyDB == nil || database.historyDB == database.db {
		t.Fatal("file history must own a separate pool")
	}
	a, err := database.historyDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	b, err := database.historyDB.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer b.Close()
	var readOnly, writerReadOnly, writerMmap int
	if err := a.QueryRowContext(ctx, "PRAGMA query_only").Scan(&readOnly); err != nil || readOnly != 1 {
		t.Fatalf("reader query_only=%d error=%v", readOnly, err)
	}
	if _, err := a.ExecContext(ctx, "UPDATE nodes SET name='unexpected write' WHERE id=?", node.ID); err == nil {
		t.Fatal("history connection allowed a database write")
	}
	if err := database.db.QueryRowContext(ctx, "PRAGMA query_only").Scan(&writerReadOnly); err != nil || writerReadOnly != 0 {
		t.Fatalf("writer query_only changed: %d %v", writerReadOnly, err)
	}
	if err := database.db.QueryRowContext(ctx, "PRAGMA mmap_size").Scan(&writerMmap); err != nil || writerMmap != 0 {
		t.Fatalf("writer mapping changed: %d %v", writerMmap, err)
	}
	if _, _, err := database.CreateNode(ctx, CreateNodeParams{Name: "writer remains available"}); err != nil {
		t.Fatal("two held readers blocked main writer", err)
	}
	queued, stop := context.WithCancel(ctx)
	done := make(chan error, 1)
	before := database.historyDB.Stats().WaitCount
	go func() {
		var value int
		done <- database.historyDB.QueryRowContext(queued, "SELECT 1").Scan(&value)
	}()
	for database.historyDB.Stats().WaitCount == before && ctx.Err() == nil {
		runtime.Gosched()
	}
	stop()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("queued reader error=%v", err)
		}
	case <-ctx.Done():
		t.Fatal("queued history reader did not stop", ctx.Err())
	}
	if stats := database.historyDB.Stats(); stats.MaxOpenConnections != 2 || stats.InUse != 2 || stats.WaitCount != before+1 {
		t.Fatalf("reader budget changed: %+v", stats)
	}
	if err := a.Close(); err != nil {
		t.Fatal(err)
	}
	if err := b.Close(); err != nil {
		t.Fatal(err)
	}
	if stats := database.historyDB.Stats(); stats.OpenConnections != 0 || stats.Idle != 0 || stats.InUse != 0 {
		t.Fatalf("idle history connection retained: %+v", stats)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal("repeated close", err)
	}
	if err := database.historyDB.PingContext(ctx); err == nil {
		t.Fatal("history pool usable after Store.Close")
	}
}

var historyReadCancelOnce sync.Once

type historyReadCancellation struct {
	cancel context.CancelFunc
	called atomic.Bool
}

var historyReadCancel atomic.Pointer[historyReadCancellation]

func TestHistoryReadCancellationDoesNotPinWAL(t *testing.T) {
	historyReadCancelOnce.Do(func() {
		sqlite.MustRegisterScalarFunction("myprobe_history_read_cancel", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
			if plan := historyReadCancel.Load(); plan != nil {
				plan.called.Store(true)
				plan.cancel()
			}
			return args[0], nil
		})
	})
	defer historyReadCancel.Store(nil)
	database, _, _ := historyReadFileFixture(t, OpenOptions{})
	ctx, stop := context.WithTimeout(context.Background(), 20*time.Second)
	defer stop()
	if _, err := database.db.ExecContext(ctx, "CREATE TABLE history_cancel(v INTEGER); INSERT INTO history_cancel VALUES(1)"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 80; i++ {
		if _, err := database.db.ExecContext(ctx, "UPDATE history_cancel SET v=v+1"); err != nil {
			t.Fatal(err)
		}
		query, cancel := context.WithCancel(ctx)
		plan := &historyReadCancellation{cancel: cancel}
		historyReadCancel.Store(plan)
		rows, err := database.historyDB.QueryContext(query, "SELECT myprobe_history_read_cancel(v) FROM history_cancel")
		if rows != nil {
			if closeErr := rows.Close(); closeErr != nil && !errors.Is(closeErr, context.Canceled) {
				t.Fatal(closeErr)
			}
		}
		callbackCanceled := plan.called.Load() && query.Err() == context.Canceled
		cancel()
		if err != nil && !errors.Is(err, context.Canceled) {
			t.Fatalf("cycle %d: %v", i, err)
		}
		if !callbackCanceled {
			t.Fatal("first-row cancellation was not exercised")
		}
		if stats := database.historyDB.Stats(); stats.InUse != 0 || stats.OpenConnections != 0 {
			t.Fatalf("cycle %d: leaked reader %+v", i, stats)
		}
		var busy, frames, copied int
		if err := database.db.QueryRowContext(ctx, "PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &frames, &copied); err != nil || busy != 0 || frames != copied {
			t.Fatalf("cycle %d pinned WAL: %d %d %d %v", i, busy, frames, copied, err)
		}
	}
}

func TestHistoryReadMemoryStoreKeepsItsExistingPool(t *testing.T) {
	database, err := Open(context.Background(), ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	if database.historyDB != nil || database.historyQueryDB() != database.db {
		t.Fatal("in-memory history changed databases or pools")
	}
}

// This case also runs unchanged against the previous implementation. Reserving
// the main pool holds no SQLite transaction/lock: only connection ownership.
func TestHistoryReadIndependentOfBusyMainPool(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(t.TempDir(), "main-pool.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	node, _, err := database.CreateNode(ctx, CreateNodeParams{Name: "read isolation"})
	if err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	if _, err := database.db.Exec(`INSERT INTO metric_samples(node_id,captured_at,cpu_usage,memory_used,memory_total,disk_used,disk_total,net_rx_rate,net_tx_rate,net_rx_total,net_tx_total,report_json) VALUES(?,?,17,0,0,0,0,0,0,0,0,'{}')`, node.ID, formatTime(base)); err != nil {
		t.Fatal(err)
	}
	var held []*sql.Conn
	defer func() {
		for _, connection := range held {
			connection.Close()
		}
	}()
	for i := 0; i < 4; i++ {
		connection, err := database.db.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, connection)
	}
	readContext, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	points, err := database.MetricHistoryRange(readContext, node.ID, base, base.Add(time.Minute), 60)
	if err != nil || len(points) != 1 || points[0].CPUPercent != 17 {
		t.Fatalf("main-pool occupancy blocked history: points=%v error=%v", points, err)
	}
}

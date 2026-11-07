package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/url"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"modernc.org/sqlite"
)

type trafficReadConnector struct {
	dsn       string
	afterRaw  func() error
	cancelRaw context.CancelFunc
}

func (c trafficReadConnector) Driver() driver.Driver { return &sqlite.Driver{} }
func (c trafficReadConnector) Connect(context.Context) (driver.Conn, error) {
	connection, err := c.Driver().Open(c.dsn)
	if err != nil {
		return nil, err
	}
	return &trafficReadConnection{Conn: connection, plan: c}, nil
}

type trafficReadConnection struct {
	driver.Conn
	plan trafficReadConnector
}

func (c *trafficReadConnection) QueryContext(ctx context.Context, query string, args []driver.NamedValue) (driver.Rows, error) {
	rows, err := c.Conn.(driver.QueryerContext).QueryContext(ctx, query, args)
	if err != nil || query != trafficHistoryRangeQuery {
		return rows, err
	}
	return &trafficReadRows{Rows: rows, after: c.plan.afterRaw, cancel: c.plan.cancelRaw}, nil
}

type trafficReadRows struct {
	driver.Rows
	after  func() error
	cancel context.CancelFunc
	seen   int
	once   sync.Once
	err    error
}

func (r *trafficReadRows) Next(values []driver.Value) error {
	err := r.Rows.Next(values)
	if err == nil {
		r.seen++
		if r.seen == 2 && r.cancel != nil {
			r.cancel()
		}
	}
	return err
}

func (r *trafficReadRows) Close() error {
	r.once.Do(func() {
		r.err = r.Rows.Close()
		if r.err == nil && r.after != nil {
			r.err = r.after()
		}
	})
	return r.err
}

func installTrafficReadObserver(t *testing.T, database *Store, plan trafficReadConnector) {
	t.Helper()
	if err := database.historyDB.Close(); err != nil {
		t.Fatal(err)
	}
	plan.dsn = "file:" + url.PathEscape(filepath.ToSlash(database.path)) + "?mode=ro&_pragma=query_only(1)&_pragma=busy_timeout(5000)"
	database.historyDB = sql.OpenDB(plan)
	database.historyDB.SetMaxOpenConns(2)
	database.historyDB.SetMaxIdleConns(0)
}

func trafficStreamFixture(t *testing.T) (*Store, Node, time.Time) {
	t.Helper()
	database, node, base := historyReadFileFixture(t, OpenOptions{DisableHistoryMmap: true})
	if _, err := database.db.Exec("DELETE FROM metric_samples; DELETE FROM traffic_rollups"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if _, err := database.db.Exec(`INSERT INTO metric_samples(node_id,captured_at,cpu_usage,memory_used,memory_total,disk_used,disk_total,net_rx_rate,net_tx_rate,net_rx_total,net_tx_total,report_json) VALUES(?,?,0,0,0,0,0,0,0,?,?,'{}')`, node.ID, formatTime(base.Add(time.Duration(i)*time.Minute)), 100+i*50, 200+i*100); err != nil {
			t.Fatal(err)
		}
	}
	return database, node, base
}

func TestTrafficHistoryStreamingKeepsOneRetentionSnapshot(t *testing.T) {
	database, node, base := trafficStreamFixture(t)
	var once sync.Once
	var moved bool
	installTrafficReadObserver(t, database, trafficReadConnector{afterRaw: func() error {
		var err error
		once.Do(func() {
			var transaction *sql.Tx
			transaction, err = database.db.BeginTx(context.Background(), nil)
			if err != nil {
				return
			}
			defer transaction.Rollback()
			if _, err = transaction.Exec(`INSERT INTO traffic_rollups VALUES(?,60,?,50,100),(?,60,?,50,100)`, node.ID, formatTime(base.Add(time.Minute)), node.ID, formatTime(base.Add(2*time.Minute))); err != nil {
				return
			}
			if _, err = transaction.Exec("DELETE FROM metric_samples WHERE node_id=?", node.ID); err != nil {
				return
			}
			err = transaction.Commit()
			moved = err == nil
		})
		return err
	}})
	for attempt := 0; attempt < 2; attempt++ {
		points, err := database.TrafficHistoryRange(context.Background(), node.ID, base, base.Add(3*time.Minute), 60)
		if err != nil || !moved || len(points) != 2 || !points[0].Time.Equal(base.Add(time.Minute)) || points[0].RXBytes != 50 || points[0].TXBytes != 100 || !points[1].Time.Equal(base.Add(2*time.Minute)) || points[1].RXBytes != 100 || points[1].TXBytes != 200 {
			t.Fatalf("attempt=%d concurrent raw-to-rollup move lost/doubled traffic: %+v, %v", attempt, points, err)
		}
	}
	if stats := database.historyDB.Stats(); stats.OpenConnections != 0 || stats.InUse != 0 {
		t.Fatal("retained reader", stats)
	}
	var busy, frames, copied int
	if err := database.db.QueryRow("PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &frames, &copied); err != nil || busy != 0 || frames != copied {
		t.Fatalf("reader pinned WAL: %d/%d/%d %v", busy, frames, copied, err)
	}
}

func TestTrafficHistoryStreamingCancellationReleasesSnapshot(t *testing.T) {
	database, node, base := trafficStreamFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	installTrafficReadObserver(t, database, trafficReadConnector{cancelRaw: cancel})
	points, err := database.TrafficHistoryRange(ctx, node.ID, base, base.Add(3*time.Minute), 60)
	if !errors.Is(err, context.Canceled) || points != nil {
		t.Fatalf("cancelled scan returned data: %+v %v", points, err)
	}
	deadline := time.Now().Add(time.Second)
	for database.historyDB.Stats().InUse != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if stats := database.historyDB.Stats(); stats.OpenConnections != 0 || stats.InUse != 0 {
		t.Fatal("cancelled snapshot retained reader", stats)
	}
	var busy, frames, copied int
	if err := database.db.QueryRow("PRAGMA wal_checkpoint(PASSIVE)").Scan(&busy, &frames, &copied); err != nil || busy != 0 || frames != copied {
		t.Fatalf("cancelled reader pinned WAL: %d/%d/%d %v", busy, frames, copied, err)
	}
}

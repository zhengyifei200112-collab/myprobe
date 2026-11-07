package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"net/url"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
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
	for _, legacy := range []bool{false, true} {
		name := "indexed"
		if legacy {
			name = "legacy_rescan"
		}
		t.Run(name, func(t *testing.T) { trafficHistorySnapshotCase(t, legacy) })
	}
}

func trafficHistorySnapshotCase(t *testing.T, legacy bool) {
	t.Helper()
	database, node, base := trafficStreamFixture(t)
	if legacy {
		spelling := base.In(time.FixedZone("synthetic", 2*3600)).Format("2006-01-02T15:04:05.000000000-07:00")
		if _, err := database.db.Exec("UPDATE metric_samples SET captured_at=? WHERE node_id=? AND net_rx_total=100", spelling, node.ID); err != nil {
			t.Fatal(err)
		}
	}
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
		points, err := database.TrafficHistoryRange(context.Background(), node.ID, base, base.Add(3*time.Hour), 60)
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

func TestTrafficHistoryStreamingLegacyTimestampOrder(t *testing.T) {
	for _, test := range []struct {
		name string
		at   []string
	}{
		{"offset", []string{"2026-01-01T12:00:00+02:00", "2026-01-01T11:00:00Z", "2026-01-01T12:00:00Z"}},
		{"comma_fraction", []string{"2026-01-01T00:00:00.1Z", "2026-01-01T00:00:00,9Z", "2026-01-01T00:00:01Z"}},
	} {
		t.Run(test.name, func(t *testing.T) {
			database, node, _ := historyFixture(t)
			for i, at := range test.at {
				if _, err := parseTime(at); err != nil {
					t.Fatal("invalid reference timestamp", err)
				}
				if _, err := database.db.Exec(`INSERT INTO metric_samples(node_id,captured_at,cpu_usage,memory_used,memory_total,disk_used,disk_total,net_rx_rate,net_tx_rate,net_rx_total,net_tx_total,report_json) VALUES(?,?,0,0,0,0,0,0,0,?,?,'{}')`, node.ID, at, 100+i*50, 200+i*100); err != nil {
					t.Fatal(err)
				}
			}
			base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
			points, err := database.TrafficHistoryRange(context.Background(), node.ID, base, base.Add(24*time.Hour), 86400)
			if err != nil || len(points) != 1 || points[0].RXBytes != 100 || points[0].TXBytes != 200 {
				t.Fatalf("parsed-time counter order changed: %+v %v", points, err)
			}
		})
	}
}

func TestTrafficHistoryStreamingMixedLegacySpellingsMatchCounterOracle(t *testing.T) {
	database, node, _ := historyFixture(t)
	type referenceCounter struct {
		trafficSample
		spelling string
		id       int64
	}
	var counters []referenceCounter
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	offsets := []int{0, 3600, -3600, 19800, -12600, 14 * 3600, -14 * 3600}
	nanoseconds := []int{0, 1, 100000000, 999499999, 999500000, 999999999}
	for i := 0; i < 84; i++ {
		at := base.Add(time.Duration(i%5-2)*time.Second + time.Duration(nanoseconds[i%len(nanoseconds)]))
		local := at.In(time.FixedZone("synthetic", offsets[i%len(offsets)]))
		spelling := local.Format("2006-01-02T15:04:05.000000000-07:00")
		switch i % 4 {
		case 0:
			spelling = local.Format(time.RFC3339Nano)
		case 1:
			spelling = strings.Replace(spelling, ".", ",", 1)
		case 2:
			spelling = spelling[:len(spelling)-6] + "00" + spelling[len(spelling)-6:]
		}
		parsed, err := parseTime(spelling)
		if err != nil || !parsed.Equal(at) {
			t.Fatalf("invalid reference spelling %q: %v", spelling, err)
		}
		rx, tx := uint64(i*37%251), uint64(i*91%503)
		insert, err := database.db.Exec(`INSERT INTO metric_samples(node_id,captured_at,cpu_usage,memory_used,memory_total,disk_used,disk_total,net_rx_rate,net_tx_rate,net_rx_total,net_tx_total,report_json) VALUES(?,?,0,0,0,0,0,0,0,?,?,'{}')`, node.ID, spelling, rx, tx)
		if err != nil {
			t.Fatal(err)
		}
		id, err := insert.LastInsertId()
		if err != nil {
			t.Fatal(err)
		}
		counters = append(counters, referenceCounter{trafficSample: trafficSample{at: parsed, rx: rx, tx: tx}, spelling: spelling, id: id})
	}
	// The independent chronological comparator is the previous counter contract;
	// it does not use either SQL ordering expression.
	sort.Slice(counters, func(i, j int) bool {
		a, b := counters[i], counters[j]
		if !a.at.Equal(b.at) {
			return a.at.Before(b.at)
		}
		if a.spelling != b.spelling {
			return a.spelling < b.spelling
		}
		return a.id < b.id
	})
	var rx, tx uint64
	want := map[int64][2]uint64{}
	for i := 1; i < len(counters); i++ {
		a, b := counters[i-1], counters[i]
		if b.rx >= a.rx {
			rx = b.rx - a.rx
		} else {
			rx = b.rx
		}
		if b.tx >= a.tx {
			tx = b.tx - a.tx
		} else {
			tx = b.tx
		}
		key := b.at.Unix() / 60 * 60
		value := want[key]
		value[0] += rx
		value[1] += tx
		want[key] = value
	}
	var keys []int64
	for key := range want {
		keys = append(keys, key)
	}
	sort.Slice(keys, func(i, j int) bool { return keys[i] < keys[j] })
	var expected []TrafficHistoryPoint
	rx, tx = 0, 0
	for _, key := range keys {
		rx += want[key][0]
		tx += want[key][1]
		expected = append(expected, TrafficHistoryPoint{Time: time.Unix(key, 0).UTC(), RXBytes: rx, TXBytes: tx, Total: rx + tx})
	}
	for _, reverse := range []string{"OFF", "ON"} {
		if _, err := database.db.Exec("PRAGMA reverse_unordered_selects=" + reverse); err != nil {
			t.Fatal(err)
		}
		got, err := database.TrafficHistoryRange(context.Background(), node.ID, base.Add(-48*time.Hour), base.Add(48*time.Hour), 60)
		if err != nil || !reflect.DeepEqual(got, expected) {
			t.Fatalf("legacy mixed-spelling counter order reverse=%s: got%+v want%+v err%v", reverse, got, expected, err)
		}
	}
}

package main

import (
	"context"
	"database/sql"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

type retentionFixture struct {
	apply func(context.Context, time.Time, store.RetentionPolicy) (store.HistoryRetentionBatch, error)
	prune func(context.Context, time.Time) (store.HTTPRetentionBatch, error)
}

func (f retentionFixture) RetainHistory(ctx context.Context, now time.Time, p store.RetentionPolicy) (store.HistoryRetentionBatch, error) {
	return f.apply(ctx, now, p)
}
func (f retentionFixture) PruneHTTPHistory(ctx context.Context, now time.Time) (store.HTTPRetentionBatch, error) {
	return f.prune(ctx, now)
}

func retentionWorker(t *testing.T, database retentionDatabase, interval time.Duration) (context.CancelFunc, <-chan struct{}) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runRetention(ctx, database, store.DefaultRetentionPolicy(), interval, slog.New(slog.NewTextHandler(io.Discard, nil)))
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("retention worker did not stop")
		}
	})
	return cancel, done
}

func TestRetentionWorkerImmediatelyDrainsAndYields(t *testing.T) {
	called := make(chan time.Time, 4)
	var passes atomic.Int64
	retentionWorker(t, retentionFixture{
		apply: func(context.Context, time.Time, store.RetentionPolicy) (store.HistoryRetentionBatch, error) {
			return store.HistoryRetentionBatch{}, nil
		},
		prune: func(_ context.Context, now time.Time) (store.HTTPRetentionBatch, error) {
			called <- now
			return store.HTTPRetentionBatch{TasksDeleted: 1000, More: passes.Add(1) == 1}, nil
		},
	}, time.Hour)
	var first time.Time
	for i := 0; i < 2; i++ {
		select {
		case at := <-called:
			if i == 0 {
				first = at
			} else if at.Sub(first) < 80*time.Millisecond {
				t.Fatal("cleanup did not yield between batches")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("cleanup waited for the hourly timer")
		}
	}
	select {
	case <-called:
		t.Fatal("empty backlog was polled continuously")
	case <-time.After(200 * time.Millisecond):
	}
}

func TestRetentionWorkerRetriesFailureWithoutWaitingForHour(t *testing.T) {
	called := make(chan time.Time, 4)
	var passes atomic.Int64
	retentionWorker(t, retentionFixture{
		apply: func(context.Context, time.Time, store.RetentionPolicy) (store.HistoryRetentionBatch, error) {
			return store.HistoryRetentionBatch{}, nil
		},
		prune: func(_ context.Context, now time.Time) (store.HTTPRetentionBatch, error) {
			called <- now
			if passes.Add(1) == 1 {
				return store.HTTPRetentionBatch{}, errors.New("synthetic transient failure")
			}
			return store.HTTPRetentionBatch{}, nil
		},
	}, time.Hour)
	var first time.Time
	for i := 0; i < 2; i++ {
		select {
		case at := <-called:
			if i == 0 {
				first = at
			} else if at.Sub(first) < 4*time.Second {
				t.Fatal("failure retry has no backoff")
			}
		case <-time.After(7 * time.Second):
			t.Fatal("transient cleanup failure stalled until the next hourly run")
		}
	}
}

func TestRetentionWorkerDrainsHostBacklogWhenHTTPIsIdle(t *testing.T) {
	called := make(chan time.Time, 4)
	var passes atomic.Int64
	cancel, _ := retentionWorker(t, retentionFixture{
		apply: func(_ context.Context, now time.Time, _ store.RetentionPolicy) (store.HistoryRetentionBatch, error) {
			called <- now
			return store.HistoryRetentionBatch{RawBuckets: 1, More: passes.Add(1) == 1}, nil
		},
		prune: func(context.Context, time.Time) (store.HTTPRetentionBatch, error) {
			return store.HTTPRetentionBatch{}, nil
		},
	}, time.Hour)
	var first time.Time
	for i := range 2 {
		select {
		case at := <-called:
			if i == 0 {
				first = at
			} else if at.Sub(first) < 80*time.Millisecond {
				t.Fatal("host catch-up did not release the writer between passes")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("host backlog waited for the hourly interval")
		}
	}
	cancel()
}

func TestRetentionWorkerKeepsHostRollupScheduleAndCancels(t *testing.T) {
	rolled := make(chan struct{}, 8)
	_, done := retentionWorker(t, retentionFixture{
		apply: func(context.Context, time.Time, store.RetentionPolicy) (store.HistoryRetentionBatch, error) {
			rolled <- struct{}{}
			return store.HistoryRetentionBatch{}, nil
		},
		prune: func(context.Context, time.Time) (store.HTTPRetentionBatch, error) {
			return store.HTTPRetentionBatch{More: true}, nil
		},
	}, 50*time.Millisecond)
	select {
	case <-rolled:
	case <-time.After(2 * time.Second):
		t.Fatal("HTTP backlog prevented host rollups")
	}
	select {
	case <-done:
		t.Fatal("worker stopped before cancellation")
	default:
	}
}

func TestRetentionWorkerDrainsRealDatabaseBeforeHourlyTimer(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(retentionFixtureDir(t), "retention-worker.db")
	database, err := store.Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { database.Close() })
	node, _, err := database.CreateNode(ctx, store.CreateNodeParams{Name: "Synthetic worker observer"})
	if err != nil {
		t.Fatal(err)
	}
	service, err := database.SaveHTTPService(ctx, store.HTTPService{Name: "Synthetic worker service", Enabled: true, IntervalSeconds: 60, NodeIDs: []string{node.ID}, Spec: httpcheck.Spec{URL: "http://example.test/retention", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 1000, MaxBodyBytes: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	inspection, err := sql.Open("sqlite", "file:"+url.PathEscape(filepath.ToSlash(path))+"?_pragma=foreign_keys(1)&_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer inspection.Close()
	old := time.Now().UTC().Add(-store.HTTPHistoryRetention - time.Hour).Format(time.RFC3339)
	if _, err := inspection.Exec(`WITH RECURSIVE batch(n) AS (SELECT 1 UNION ALL SELECT n+1 FROM batch WHERE n<2500)
 INSERT INTO http_tasks(id,service_id,node_id,revision,scheduled_at,expires_at,task_json)
 SELECT 'synthetic-'||n,?,?,1,strftime('%Y-%m-%dT%H:%M:%SZ',?, '+'||n||' seconds'),?, '{}' FROM batch`, service.ID, node.ID, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := inspection.Exec(`INSERT INTO http_tasks(id,service_id,node_id,revision,scheduled_at,expires_at,task_json) VALUES('retained',?,?,1,?,?,'{}')`, service.ID, node.ID, time.Now().UTC().Format(time.RFC3339Nano), time.Now().UTC().Add(time.Hour).Format(time.RFC3339Nano)); err != nil {
		t.Fatal(err)
	}
	retentionWorker(t, database, time.Hour)
	deadline := time.Now().Add(5 * time.Second)
	for {
		var count int
		if err := inspection.QueryRow(`SELECT COUNT(*) FROM http_tasks`).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("real backlog did not drain before the hourly timer: %d", count)
		}
		time.Sleep(10 * time.Millisecond)
	}
	var retained string
	if err := inspection.QueryRow(`SELECT id FROM http_tasks`).Scan(&retained); err != nil || retained != "retained" {
		t.Fatalf("retained row: %s %v", retained, err)
	}
}

func retentionFixtureDir(t *testing.T) string {
	t.Helper()
	root := os.Getenv("MYPROBE_HTTP_TEST_DIR")
	if root == "" {
		return t.TempDir()
	}
	if !filepath.IsAbs(root) {
		t.Fatal("fixture root must be absolute")
	}
	if err := os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "server-"+strings.ReplaceAll(t.Name(), "/", "_")+"-")
	if err != nil {
		t.Fatal(err)
	}
	return dir
}

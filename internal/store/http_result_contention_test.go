package store

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
)

func httpResultContentionFixture(t *testing.T) (context.Context, *Store, string, httpcheck.Result) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	db, err := Open(ctx, filepath.Join(httpFixtureDir(t), "http-result.db"))
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	t.Cleanup(func() { cancel(); db.Close() })
	node, _, err := db.CreateNode(ctx, CreateNodeParams{Name: "HTTP writer fixture"})
	if err != nil {
		t.Fatal(err)
	}
	service, err := db.SaveHTTPService(ctx, HTTPService{Name: "HTTP writer fixture", Enabled: true, IntervalSeconds: 60, NodeIDs: []string{node.ID}, Spec: httpcheck.Spec{URL: "http://example.invalid/", Method: "GET", StatusCodes: []int{200, 204}, TimeoutMS: 5000, MaxBodyBytes: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	task, err := db.CreateHTTPTask(ctx, service.ID, node.ID, now, now)
	if err != nil {
		t.Fatal(err)
	}
	result := httpcheck.Result{TaskID: task.ID, ServiceID: task.ServiceID, Revision: task.Revision, ScheduledAt: task.ScheduledAt, CompletedAt: now.Add(time.Millisecond), Outcome: "success", StatusCode: 200, DurationMS: 1}
	if err := result.ValidateFor(task, time.Now().UTC()); err != nil {
		t.Fatalf("invalid contention fixture before writer acquisition: %v", err)
	}
	return ctx, db, node.ID, result
}

func TestHTTPResultSurvivesWriterBusyTimeout(t *testing.T) {
	for _, revoked := range []bool{false, true} {
		name := "writer_released"
		if revoked {
			name = "service_revoked_before_release"
		}
		t.Run(name, func(t *testing.T) {
			ctx, db, nodeID, result := httpResultContentionFixture(t)
			lock, err := db.db.BeginTx(ctx, nil)
			if err != nil {
				t.Fatal(err)
			}
			defer lock.Rollback()
			query := "UPDATE http_services SET revision=revision WHERE id=?"
			if revoked {
				query = "UPDATE http_services SET enabled=0 WHERE id=?"
			}
			if _, err = lock.ExecContext(ctx, query, result.ServiceID); err != nil {
				t.Fatal(err)
			}
			finished := make(chan error, 1)
			go func() { finished <- db.SaveHTTPResult(ctx, nodeID, result, time.Now().UTC()) }()
			timer := time.NewTimer(6 * time.Second)
			defer timer.Stop()
			var returned error
			early := false
			select {
			case returned = <-finished:
				early = true
				t.Errorf("HTTP result returned before transient writer was released: %v", returned)
				<-timer.C
			case <-timer.C:
			}
			if revoked {
				err = lock.Commit()
			} else {
				err = lock.Rollback()
			}
			if err != nil {
				t.Fatal(err)
			}
			if !early {
				select {
				case returned = <-finished:
				case <-ctx.Done():
					t.Fatal("HTTP result did not complete after releasing writer")
				}
			}
			var stored sql.NullString
			if err = db.db.QueryRowContext(ctx, "SELECT result_json FROM http_tasks WHERE id=?", result.TaskID).Scan(&stored); err != nil {
				t.Fatal(err)
			}
			if revoked {
				if !errors.Is(returned, ErrHTTPTaskRejected) || stored.Valid {
					t.Fatalf("revoked service result admitted: err=%v persisted=%v", returned, stored.Valid)
				}
				return
			}
			if returned != nil || !stored.Valid {
				t.Fatalf("completed result lost to transient writer: err=%v persisted=%v", returned, stored.Valid)
			}
			result.StatusCode = 204
			if err = db.SaveHTTPResult(ctx, nodeID, result, time.Now().UTC()); !errors.Is(err, ErrHTTPTaskRejected) {
				t.Fatalf("duplicate result replaced original: %v", err)
			}
			var after string
			if err = db.db.QueryRowContext(ctx, "SELECT result_json FROM http_tasks WHERE id=?", result.TaskID).Scan(&after); err != nil || after != stored.String {
				t.Fatalf("original result changed: err=%v", err)
			}
		})
	}
}

func TestHTTPResultWriterWaitCancellationDoesNotPersist(t *testing.T) {
	ctx, db, nodeID, result := httpResultContentionFixture(t)
	lock, err := db.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Rollback()
	if _, err = lock.ExecContext(ctx, "UPDATE http_services SET revision=revision WHERE id=?", result.ServiceID); err != nil {
		t.Fatal(err)
	}
	request, cancel := context.WithCancel(ctx)
	finished := make(chan error, 1)
	go func() { finished <- db.SaveHTTPResult(request, nodeID, result, time.Now().UTC()) }()
	// Keep the writer beyond the connection's normal five-second busy timeout,
	// then cancel during the retry window before releasing the owned writer.
	timer := time.NewTimer(6 * time.Second)
	defer timer.Stop()
	select {
	case err = <-finished:
		t.Errorf("HTTP result returned before cancellation: %v", err)
		cancel()
	case <-timer.C:
		cancel()
		err = <-finished
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("writer wait ignored caller cancellation: %v", err)
	}
	if err = lock.Rollback(); err != nil {
		t.Fatal(err)
	}
	var stored sql.NullString
	if err = db.db.QueryRowContext(ctx, "SELECT result_json FROM http_tasks WHERE id=?", result.TaskID).Scan(&stored); err != nil || stored.Valid {
		t.Fatalf("canceled result persisted: err=%v persisted=%v", err, stored.Valid)
	}
}

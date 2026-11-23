package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
)

func TestHTTPDispatchCanceledWhileWaitingForWriter(t *testing.T) {
	ctx := context.Background()
	database, err := Open(ctx, filepath.Join(httpFixtureDir(t), "http-dispatch-cancel.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	node, _, err := database.CreateNode(ctx, CreateNodeParams{Name: "dispatch cancellation"})
	if err != nil {
		t.Fatal(err)
	}
	service, err := database.SaveHTTPService(ctx, HTTPService{
		Name: "dispatch cancellation", Enabled: true, IntervalSeconds: 30, NodeIDs: []string{node.ID},
		Spec: httpcheck.Spec{URL: "http://example.test/", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 100, MaxBodyBytes: 1024},
	})
	if err != nil {
		t.Fatal(err)
	}
	blocker, err := database.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer blocker.Rollback()
	if _, err := blocker.ExecContext(ctx, "UPDATE http_services SET name=name WHERE id=?", service.ID); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(ctx)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := database.CreateHTTPDispatchTask(canceled, service.ID, node.ID, time.Now().UTC())
		done <- err
	}()
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := blocker.Rollback(); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled dispatch: %v", err)
		}
	case <-time.After(6 * time.Second):
		t.Fatal("canceled dispatch did not exit")
	}
	var tasks int
	if err := database.db.QueryRowContext(ctx, "SELECT count(*) FROM http_tasks").Scan(&tasks); err != nil || tasks != 0 {
		t.Fatalf("canceled dispatch retained %d tasks: %v", tasks, err)
	}
}

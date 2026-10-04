package store

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
)

func TestHTTPTaskResultBinding(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "tasks.db")
	s, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.SaveHTTPService(ctx, HTTPService{Name: "fixture", Enabled: true, IntervalSeconds: 60, NodeIDs: []string{node.ID}, Spec: httpcheck.Spec{URL: "http://example.com", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 5000, MaxBodyBytes: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	task, err := s.CreateHTTPTask(ctx, v.ID, node.ID, now, now)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateHTTPTask(ctx, v.ID, node.ID, now, now); !errors.Is(err, ErrHTTPTaskRejected) {
		t.Fatalf("duplicate slot: %v", err)
	}
	if _, err = s.CreateHTTPTask(ctx, v.ID, "other", now, now); !errors.Is(err, ErrHTTPTaskRejected) {
		t.Fatalf("unassigned node: %v", err)
	}
	// Pending task identity and its original specification survive restart.
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	valid := httpcheck.Result{TaskID: task.ID, ServiceID: task.ServiceID, Revision: task.Revision, ScheduledAt: task.ScheduledAt, CompletedAt: now.Add(time.Second), Outcome: "success", StatusCode: 200, DurationMS: 1000}
	if err = s.SaveHTTPResult(ctx, "other", valid, now.Add(time.Second)); !errors.Is(err, ErrHTTPTaskRejected) {
		t.Fatalf("wrong node: %v", err)
	}
	bad := valid
	bad.ServiceID = "other"
	if err = s.SaveHTTPResult(ctx, node.ID, bad, now.Add(time.Second)); !errors.Is(err, ErrHTTPTaskRejected) {
		t.Fatalf("wrong identity: %v", err)
	}
	if err = s.SaveHTTPResult(ctx, node.ID, valid, task.ExpiresAt.Add(61*time.Second)); !errors.Is(err, ErrHTTPTaskRejected) {
		t.Fatalf("late: %v", err)
	}
	var wg sync.WaitGroup
	results := make(chan error, 8)
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); results <- s.SaveHTTPResult(ctx, node.ID, valid, now.Add(time.Second)) }()
	}
	wg.Wait()
	close(results)
	accepted := 0
	for result := range results {
		if result == nil {
			accepted++
		} else if !errors.Is(result, ErrHTTPTaskRejected) {
			t.Fatal(result)
		}
	}
	if accepted != 1 {
		t.Fatalf("accepted %d results", accepted)
	}
	var encoded string
	if err = s.db.QueryRowContext(ctx, `SELECT result_json FROM http_tasks WHERE id=?`, task.ID).Scan(&encoded); err != nil {
		t.Fatal(err)
	}
	var stored httpcheck.Result
	if err = json.Unmarshal([]byte(encoded), &stored); err != nil || stored.StatusCode != 200 {
		t.Fatalf("stored: %+v %v", stored, err)
	}

	// A current task becomes ineligible as soon as configuration advances.
	later := now.Add(time.Minute)
	oldTask, err := s.CreateHTTPTask(ctx, v.ID, node.ID, later, later)
	if err != nil {
		t.Fatal(err)
	}
	v.Name = "new revision"
	v, err = s.SaveHTTPService(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	valid.TaskID, valid.ScheduledAt, valid.CompletedAt = oldTask.ID, later, later.Add(time.Second)
	if err = s.SaveHTTPResult(ctx, node.ID, valid, later.Add(time.Second)); !errors.Is(err, ErrHTTPTaskRejected) {
		t.Fatalf("old revision: %v", err)
	}
	v.Enabled = false
	if _, err = s.SaveHTTPService(ctx, v); err != nil {
		t.Fatal(err)
	}
	if _, err = s.CreateHTTPTask(ctx, v.ID, node.ID, later, later); !errors.Is(err, ErrHTTPTaskRejected) {
		t.Fatalf("disabled: %v", err)
	}
}

package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
)

func TestHTTPStatisticsMissingAndMaturity(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "stats.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	v, err := s.SaveHTTPService(ctx, HTTPService{Name: "fixture", Enabled: true, IntervalSeconds: 30, NodeIDs: []string{node.ID}, Spec: httpcheck.Spec{URL: "http://example.com", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 5000, MaxBodyBytes: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	for i, outcome := range []string{"success", "failure", "unobserved", "", "success"} {
		if outcome == "" {
			continue
		} // Offline slot: no durable task, still expected.
		at := v.UpdatedAt.Add(time.Duration(i) * 30 * time.Second)
		task, err := s.CreateHTTPTask(ctx, v.ID, node.ID, at, at)
		if err != nil {
			t.Fatal(err)
		}
		r := httpcheck.Result{TaskID: task.ID, ServiceID: v.ID, Revision: uint64(v.Revision), ScheduledAt: at, CompletedAt: at.Add(time.Second), DurationMS: 1000, Outcome: outcome, StatusCode: 200}
		if outcome == "failure" {
			r.StatusCode = 500
			r.ErrorClass = "status_mismatch"
		}
		if outcome == "unobserved" {
			r.StatusCode = 0
			r.ErrorClass = "busy"
		}
		if err = s.SaveHTTPResult(ctx, node.ID, r, at.Add(time.Second)); err != nil {
			t.Fatal(err)
		}
	}
	end := v.UpdatedAt.Add(120 * time.Second)
	got, err := s.HTTPServiceStatistics(ctx, v.ID, node.ID, v.UpdatedAt, end, end.Add(126*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if got.Expected != 4 || got.Success != 1 || got.Failure != 1 || got.Missing != 2 || got.Unobserved != 1 || got.SuccessRate == nil || *got.SuccessRate != 0.5 || got.Coverage == nil || *got.Coverage != 0.5 {
		t.Fatalf("counts: %+v", got)
	}
	fresh, err := s.HTTPServiceStatistics(ctx, v.ID, node.ID, v.UpdatedAt, end, v.UpdatedAt.Add(time.Minute))
	if err != nil || fresh.Expected != 0 || fresh.SuccessRate != nil || fresh.Coverage != nil || !fresh.End.Equal(fresh.Start) {
		t.Fatalf("immature: %+v %v", fresh, err)
	}
	empty, err := s.HTTPServiceStatistics(ctx, v.ID, node.ID, v.UpdatedAt.Add(time.Second), v.UpdatedAt.Add(29*time.Second), end.Add(126*time.Second))
	if err != nil || empty.Expected != 0 || empty.SuccessRate != nil || empty.Coverage != nil {
		t.Fatalf("empty: %+v %v", empty, err)
	}
}

package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
)

func TestHTTPTransportFailureCountsInSuccessRate(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(httpFixtureDir(t), "transport-statistics.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "transport observer"})
	if err != nil {
		t.Fatal(err)
	}
	service, err := s.SaveHTTPService(ctx, HTTPService{Name: "transport fixture", Enabled: true, IntervalSeconds: 30, NodeIDs: []string{node.ID}, Spec: httpcheck.Spec{URL: "http://example.test", Method: "GET", StatusCodes: []int{200}, TimeoutMS: 1000, MaxBodyBytes: 1024}})
	if err != nil {
		t.Fatal(err)
	}
	for i, outcome := range []string{"success", "failure"} {
		at := service.UpdatedAt.Add(time.Duration(i) * 30 * time.Second)
		task, err := s.CreateHTTPTask(ctx, service.ID, node.ID, at, at)
		if err != nil {
			t.Fatal(err)
		}
		result := httpcheck.Result{TaskID: task.ID, ServiceID: service.ID, Revision: uint64(service.Revision), ScheduledAt: at, CompletedAt: at.Add(time.Millisecond), DurationMS: 1, Outcome: outcome, StatusCode: 200}
		if outcome == "failure" {
			result.ErrorClass = "transport"
		}
		if err = s.SaveHTTPResult(ctx, node.ID, result, result.CompletedAt); err != nil {
			t.Fatal(err)
		}
	}
	end := service.UpdatedAt.Add(time.Minute)
	got, err := s.HTTPServiceStatistics(ctx, service.ID, node.ID, service.UpdatedAt, end, end.Add(126*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if got.Expected != 2 || got.Success != 1 || got.Failure != 1 || got.Missing != 0 || got.Unobserved != 0 || got.SuccessRate == nil || *got.SuccessRate != 0.5 || got.Coverage == nil || *got.Coverage != 1 {
		t.Fatalf("transport failure inflated success rate or reduced coverage: %+v", got)
	}
}

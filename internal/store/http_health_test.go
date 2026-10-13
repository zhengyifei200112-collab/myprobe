package store

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func TestHTTPHealthSnapshotUsesCurrentAssignmentAndLatestPlannedSlot(t *testing.T) {
	s, service, _, node, _ := serviceAlertFixture(t)
	ctx := context.Background()
	other, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "second"})
	if err != nil {
		t.Fatal(err)
	}
	service.NodeIDs = append(service.NodeIDs, other.ID)
	service, err = s.SaveHTTPService(ctx, service)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SaveAgentMetadata(ctx, node.ID, protocol.Hello{AgentVersion: "fixture", Hostname: "fixture", OS: "fixture", Capabilities: []string{"http_probe.v1"}}); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveReport(ctx, node.ID, protocol.Report{CapturedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Add(time.Second).Truncate(time.Second)
	// Exercise the actual batched statement's plan, not a lookalike query.
	plan, err := s.db.QueryContext(ctx, "EXPLAIN QUERY PLAN "+httpHealthQuery(1), at.UTC().Format("2006-01-02T15:04:05.000000000"), service.ID)
	if err != nil {
		t.Fatal(err)
	}
	var details []string
	for plan.Next() {
		var id, parent, unused int
		var detail string
		if err := plan.Scan(&id, &parent, &unused, &detail); err != nil {
			t.Fatal(err)
		}
		details = append(details, detail)
	}
	if err := plan.Err(); err != nil {
		t.Fatal(err)
	}
	plan.Close()
	if !strings.Contains(strings.Join(details, "\n"), "idx_http_observer_latest") {
		t.Fatal("latest-observer lookup lost its index")
	}
	save := func(id string, when time.Time, outcome string, code int) httpcheck.Task {
		t.Helper()
		task, err := s.CreateHTTPTask(ctx, service.ID, id, when, when)
		if err != nil {
			t.Fatal(err)
		}
		r := httpcheck.Result{TaskID: task.ID, ServiceID: task.ServiceID, Revision: task.Revision, ScheduledAt: task.ScheduledAt, CompletedAt: when.Add(time.Millisecond), Outcome: outcome, StatusCode: code, DurationMS: 1}
		if outcome == "failure" {
			r.ErrorClass = "status_mismatch"
		}
		if err := s.SaveHTTPResult(ctx, id, r, r.CompletedAt); err != nil {
			t.Fatal(err)
		}
		return task
	}
	save(node.ID, at, "failure", 503)
	successful := save(other.ID, at, "success", 200)
	pending, err := s.CreateHTTPTask(ctx, service.ID, node.ID, at.Add(time.Nanosecond), at.Add(time.Nanosecond))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateHTTPTask(ctx, service.ID, node.ID, at.Add(time.Second), at.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	snapshots, err := s.HTTPHealthSnapshots(ctx, []string{service.ID}, at.Add(time.Millisecond))
	if err != nil || len(snapshots) != 1 || snapshots[0].Service.NodeCount != 2 {
		t.Fatal("snapshot missing", err)
	}
	for _, o := range snapshots[0].Observers {
		if o.NodeID == node.ID && (o.Observation == nil || o.Observation.TaskID != pending.ID || o.Observation.Result != nil || o.LastSeenAt == nil || len(o.Capabilities) != 1) {
			t.Fatal("pending plan, host or metadata missing")
		}
		if o.NodeID == other.ID && (o.Observation == nil || o.Observation.TaskID != successful.ID || o.Observation.Result == nil || o.Observation.Result.Outcome != "success") {
			t.Fatal("observers shared evidence")
		}
	}
	raw, _ := json.Marshal(snapshots)
	if strings.Contains(string(raw), "example.test") || strings.Contains(string(raw), "not-a-secret") || strings.Contains(string(raw), "spec") {
		t.Fatal("private request specification read into snapshot")
	}
	// An assignment/version change excludes every prior-revision result.
	service.NodeIDs = []string{other.ID}
	service, err = s.SaveHTTPService(ctx, service)
	if err != nil {
		t.Fatal(err)
	}
	snapshots, err = s.HTTPHealthSnapshots(ctx, []string{service.ID}, at.Add(2*time.Second))
	if err != nil || len(snapshots) != 1 || len(snapshots[0].Observers) != 1 || snapshots[0].Observers[0].NodeID != other.ID || snapshots[0].Observers[0].Observation != nil || snapshots[0].Service.Revision != service.Revision {
		t.Fatal("old revision/assignment leaked", err)
	}
	if _, err := s.db.Exec(`DELETE FROM nodes WHERE id=?`, other.ID); err != nil {
		t.Fatal(err)
	}
	snapshots, err = s.HTTPHealthSnapshots(ctx, []string{service.ID}, at.Add(2*time.Second))
	if err != nil || len(snapshots) != 1 || snapshots[0].Service.NodeCount != 0 || len(snapshots[0].Observers) != 0 {
		t.Fatal("zero observers disappeared or manufactured result", err)
	}
	for _, ids := range [][]string{{service.ID, service.ID}, make([]string, 101), {""}} {
		if _, err := s.HTTPHealthSnapshots(ctx, ids, at); !errors.Is(err, ErrInvalidHTTPService) {
			t.Fatal("invalid snapshot bounds accepted")
		}
	}
	empty, err := s.HTTPHealthSnapshots(ctx, nil, at)
	if err != nil || len(empty) != 0 {
		t.Fatal(err)
	}
	missing, err := s.HTTPHealthSnapshots(ctx, []string{"missing"}, at)
	if err != nil || len(missing) != 0 {
		t.Fatal("missing service fabricated", err)
	}
}

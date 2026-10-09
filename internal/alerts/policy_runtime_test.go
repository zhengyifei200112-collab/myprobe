package alerts

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/alertpolicy"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestPolicyRuntimeAppliesTagsNewNodesAndRestart(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	recorder := &recordingSender{}
	s := New(db, strings.Repeat("s", 32), recorder, nil)
	if s.PolicyEvaluationStatus().State != "pending" {
		t.Fatal("unstarted evaluator reported ready")
	}
	channel, err := s.CreateChannel(ctx, "fixture", "webhook", ChannelConfig{URL: "https://example.com/unused"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.SavePolicy(ctx, store.AlertPolicy{Policy: alertpolicy.Policy{Key: "expiry", Enabled: true, Scope: alertpolicy.Scope{Kind: "tags", Tags: []string{"prod"}, TagMode: "all"}}, Name: "expiry", ChannelID: channel.ID, Kind: "expiry", Config: json.RawMessage(`{"days_before":1,"recovery_seconds":0}`), CooldownSeconds: 900})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	expiry := now.Add(time.Hour)
	create := func(name string) store.Node {
		t.Helper()
		n, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: name, Tags: []string{"prod"}})
		if err != nil {
			t.Fatal(err)
		}
		return updateNodeExpiry(t, db, n, &expiry)
	}
	node := create("first")
	if err := tickAndDeliver(s, ctx, now); err != nil || recorder.count() != 1 {
		t.Fatalf("first policy: %d %v", recorder.count(), err)
	}
	if status := s.PolicyEvaluationStatus(); status.State != "ready" || status.LastAppliedAt == nil || !status.LastAppliedAt.Equal(now) {
		t.Fatalf("status %+v", status)
	}
	incidents, err := db.ListIncidents(ctx, store.IncidentFilter{Limit: 10, State: "firing"})
	if err != nil || len(incidents) != 1 {
		t.Fatalf("incidents %+v %v", incidents, err)
	}
	firstID := incidents[0].ID
	// A new service instance must prepare again without duplicating the fault.
	s = New(db, strings.Repeat("s", 32), recorder, nil)
	if err := tickAndDeliver(s, ctx, now.Add(time.Second)); err != nil || recorder.count() != 1 {
		t.Fatalf("restart duplicated: %d %v", recorder.count(), err)
	}
	create("future")
	if err := tickAndDeliver(s, ctx, now.Add(2*time.Second)); err != nil || recorder.count() != 2 {
		t.Fatalf("future node: %d %v", recorder.count(), err)
	}
	node.Tags = nil
	updateNodeExpiry(t, db, node, &expiry)
	if err := tickAndDeliver(s, ctx, now.Add(3*time.Second)); err != nil || recorder.count() != 2 {
		t.Fatalf("scope exit sent recovery: %d %v", recorder.count(), err)
	}
	closed, err := db.Incident(ctx, firstID)
	if err != nil || closed.State != "resolved" || closed.ResolutionReason != "rule_disabled" {
		t.Fatalf("closure %+v %v", closed, err)
	}
	p.Enabled = false
	if _, err = s.SavePolicy(ctx, p); err != nil {
		t.Fatal(err)
	}
	if err = tickAndDeliver(s, ctx, now.Add(4*time.Second)); err != nil || recorder.count() != 2 {
		t.Fatalf("disable: %d %v", recorder.count(), err)
	}
	if items, err := db.ListIncidents(ctx, store.IncidentFilter{Limit: 10, State: "firing"}); err != nil || len(items) != 0 {
		t.Fatalf("disabled incidents: %+v %v", items, err)
	}
}

func TestPolicyRuntimeFailureKeepsLastSuccessfulPreparation(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	s := New(db, strings.Repeat("s", 32), &recordingSender{}, nil)
	now := time.Now().UTC()
	if err = s.Tick(ctx, now); err != nil {
		t.Fatal(err)
	}
	// The returned timestamp must not expose mutable service state.
	status := s.PolicyEvaluationStatus()
	*status.LastAppliedAt = time.Time{}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if err = s.Tick(ctx, now.Add(time.Second)); err == nil {
		t.Fatal("closed database accepted")
	}
	status = s.PolicyEvaluationStatus()
	if status.State != "error" || status.LastAppliedAt == nil || !status.LastAppliedAt.Equal(now) {
		t.Fatalf("failure erased success evidence: %+v", status)
	}
}

func TestPolicyRuntimePreparationFailureDoesNotStartDeliveryWorkers(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	recorder := &recordingSender{}
	s := New(db, strings.Repeat("s", 32), recorder, nil)
	s.interval = time.Hour
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "capacity fixture"})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := s.CreateChannel(ctx, "fixture", "webhook", ChannelConfig{URL: "https://example.com/unused"})
	if err != nil {
		t.Fatal(err)
	}
	var first store.AlertRule
	for index := 0; index < 1001; index++ {
		rule, err := s.CreateRule(ctx, node.ID, channel.ID, "cpu", RuleConfig{ThresholdPercent: 90}, 900)
		if err != nil {
			t.Fatal(err)
		}
		if index == 0 {
			first = rule
		}
	}
	now := time.Now().UTC()
	incident, err := db.ObserveAlert(ctx, first, node, store.AlertObservation{At: now, Known: true, Active: true})
	if err != nil || incident == nil {
		t.Fatalf("queued fault: %+v %v", incident, err)
	}
	done := make(chan struct{})
	go func() { defer close(done); s.Run(ctx) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("service did not stop")
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for s.PolicyEvaluationStatus().State != "error" {
		if time.Now().After(deadline) {
			t.Fatal("preparation error not reported")
		}
		time.Sleep(10 * time.Millisecond)
	}
	cancel()
	<-done
	if recorder.count() != 0 {
		t.Fatal("workers sent before successful preparation")
	}
	jobs, err := db.ListIncidentDeliveries(context.Background(), incident.ID, 0, 10)
	if err != nil || len(jobs) != 1 || jobs[0].AttemptCount != 0 {
		t.Fatalf("queued job changed: %+v %v", jobs, err)
	}
	policies, err := db.ListAlertPolicies(context.Background())
	if err != nil || len(policies) != 0 {
		t.Fatalf("partial migration: %+v %v", policies, err)
	}
}

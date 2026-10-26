package alerts

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

type blockingSender struct{ entered chan struct{} }

func (s *blockingSender) Deliver(ctx context.Context, _ string, _ ChannelConfig, _ Notification) error {
	s.entered <- struct{}{}
	<-ctx.Done()
	return ctx.Err()
}

func TestRunEvaluatesWhileAllWorkersAreBlockedAndCancels(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sender := &blockingSender{entered: make(chan struct{}, 8)}
	s := New(db, strings.Repeat("s", 32), sender, nil)
	s.interval = 10 * time.Millisecond
	channel, err := s.CreateChannel(ctx, "blocked", "webhook", ChannelConfig{URL: "https://example.com/unused"})
	if err != nil {
		t.Fatal(err)
	}
	create := func() {
		t.Helper()
		node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "blocked worker fixture"})
		if err != nil {
			t.Fatal(err)
		}
		expiry := time.Now().Add(time.Hour)
		node = updateNodeExpiry(t, db, node, &expiry)
		if _, err := s.CreateRule(ctx, node.ID, channel.ID, "expiry", RuleConfig{DaysBefore: 1}, 300); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 4; i++ {
		create()
	}
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("workers did not stop")
		}
	}()
	for i := 0; i < 4; i++ {
		select {
		case <-sender.entered:
		case <-time.After(3 * time.Second):
			t.Fatal("worker did not start")
		}
	}
	create()
	deadline := time.Now().Add(3 * time.Second)
	for {
		items, err := db.ListIncidents(ctx, store.IncidentFilter{Limit: 10, State: "firing"})
		if err != nil {
			t.Fatal(err)
		}
		if len(items) == 5 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("evaluation blocked behind sends: %d incidents", len(items))
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case <-sender.entered:
		t.Fatal("more than four concurrent sends")
	default:
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("service did not cancel blocked sends")
	}
}

func TestStartupRevalidatesQueuedResourceFaultBeforeSending(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sender := &blockingSender{entered: make(chan struct{}, 4)}
	s := New(db, strings.Repeat("s", 32), sender, nil)
	s.interval = time.Hour
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "stale restart fixture"})
	if err != nil {
		t.Fatal(err)
	}
	channel, err := s.CreateChannel(ctx, "fixture", "webhook", ChannelConfig{URL: "https://example.com/unused"})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := s.CreateRule(ctx, node.ID, channel.ID, "cpu", RuleConfig{ThresholdPercent: 90}, 300)
	if err != nil {
		t.Fatal(err)
	}
	incident, err := db.ObserveAlert(ctx, rule, node, store.AlertObservation{At: time.Now().Add(-time.Hour), Known: true, Active: true, Message: "old fault"})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	defer func() {
		cancel()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("service did not stop")
		}
	}()
	deadline := time.Now().Add(3 * time.Second)
	for {
		current, err := db.Incident(ctx, incident.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.ObservationStale {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("initial evaluation did not mark missing metrics stale")
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case <-sender.entered:
		t.Fatal("stale queued fault sent at startup")
	case <-time.After(50 * time.Millisecond):
	}
	jobs, err := db.ListIncidentDeliveries(ctx, incident.ID, 0, 10)
	if err != nil || len(jobs) != 1 || jobs[0].AttemptCount != 0 {
		t.Fatalf("stale job consumed an attempt: %+v %v", jobs, err)
	}
}

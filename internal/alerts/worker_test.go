package alerts

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestWorkerRetriesDurableJobAndSanitizesErrors(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "worker"})
	if err != nil {
		t.Fatal(err)
	}
	sender := &recordingSender{failCount: 1}
	s := New(db, strings.Repeat("s", 32), sender, nil)
	channel, err := s.CreateChannel(ctx, "test", "webhook", ChannelConfig{URL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	rule, err := s.CreateRule(ctx, node.ID, channel.ID, "cpu", RuleConfig{ThresholdPercent: 90}, 300)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	incident, err := db.ObserveAlert(ctx, rule, node, store.AlertObservation{At: now, Known: true, Active: true, Message: "CPU high"})
	if err != nil || incident.State != "firing" {
		t.Fatalf("observe: %v %v", incident, err)
	}
	if worked, err := s.DeliverOne(ctx, now); !worked || err != nil {
		t.Fatalf("deliver: %v %v", worked, err)
	}
	if worked, err := s.DeliverOne(ctx, now.Add(20*time.Second)); worked || err != nil {
		t.Fatalf("early retry: %v %v", worked, err)
	}
	if worked, err := s.DeliverOne(ctx, now.Add(40*time.Second)); !worked || err != nil {
		t.Fatalf("retry: %v %v", worked, err)
	}
	if worked, err := s.DeliverOne(ctx, now.Add(time.Minute)); worked || err != nil {
		t.Fatalf("duplicate: %v %v", worked, err)
	}
	events, err := db.ListAlertEvents(ctx, 10)
	if err != nil || len(events) != 2 {
		t.Fatalf("events: %v %v", events, err)
	}
	if events[1].DeliveryError != "delivery_failed" || events[0].State != "firing" || sender.count() != 2 {
		t.Fatalf("unexpected delivery history: %+v", events)
	}
}

func TestRetryDelayBounds(t *testing.T) {
	for attempt, base := range []time.Duration{30 * time.Second, 2 * time.Minute, 10 * time.Minute, 30 * time.Minute} {
		for i := 0; i < 100; i++ {
			if delay := retryDelay(attempt + 1); delay < base || delay > base+base/5 {
				t.Fatalf("delay %v outside bounds for attempt %d", delay, attempt+1)
			}
		}
	}
}

func TestTickRecordsIncidentsWithoutSendingOrDecrypting(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "independent evaluation"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	expiry := now.Add(time.Hour)
	node = updateNodeExpiry(t, db, node, &expiry)
	recorder := &recordingSender{}
	configured := New(db, strings.Repeat("s", 32), recorder, nil)
	channel, err := configured.CreateChannel(ctx, "ops", "webhook", ChannelConfig{URL: "https://example.com"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = configured.CreateRule(ctx, node.ID, channel.ID, "expiry", RuleConfig{DaysBefore: 1}, 300)
	if err != nil {
		t.Fatal(err)
	}
	withoutKey := New(db, "", recorder, nil)
	if err := withoutKey.Tick(ctx, now); err != nil {
		t.Fatal(err)
	}
	if recorder.count() != 0 {
		t.Fatal("evaluation performed network I/O")
	}
	job, err := db.ClaimDelivery(ctx, now, time.Minute)
	if err != nil || job == nil {
		t.Fatalf("incident did not enqueue without key: %v %v", job, err)
	}
	incident, err := db.Incident(ctx, job.IncidentID)
	if err != nil || incident.State != "firing" {
		t.Fatalf("incident: %+v %v", incident, err)
	}
}

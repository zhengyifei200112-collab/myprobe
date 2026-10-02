package alerts

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestObservationFreshBoundaries(t *testing.T) {
	now := time.Now().UTC()
	for _, tc := range []struct {
		age      time.Duration
		interval int
		fresh    bool
	}{
		{time.Minute, 5, true}, {time.Minute + time.Nanosecond, 5, false},
		{3 * time.Minute, 60, true}, {3*time.Minute + time.Nanosecond, 60, false},
		{-30 * time.Second, 5, true}, {-31 * time.Second, 5, false},
	} {
		if got := observationFresh(now.Add(-tc.age), now, tc.interval); got != tc.fresh {
			t.Errorf("%+v got %v", tc, got)
		}
	}
	if observationFresh(time.Time{}, now, 5) {
		t.Fatal("zero timestamp was fresh")
	}
}

func TestResourceEvaluationDoesNotRecoverFromMissingOrStaleSamples(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "freshness"})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	if err := db.SaveReport(ctx, node.ID, protocol.Report{CapturedAt: now}); err != nil {
		t.Fatal(err)
	}
	s := New(db, "", &recordingSender{}, nil)
	for _, kind := range []string{"disk", "bandwidth"} {
		_, _, known, err := s.evaluate(ctx, store.AlertRule{Kind: kind, Config: json.RawMessage(`{"threshold_percent":90,"threshold_bytes_per_second":100}`)}, node, now)
		if err != nil || known {
			t.Fatalf("missing %s metric known=%v error=%v", kind, known, err)
		}
	}
	rule := store.AlertRule{Kind: "cpu", Config: json.RawMessage(`{"threshold_percent":90}`)}
	_, _, known, err := s.evaluate(ctx, rule, node, now)
	if err != nil || !known {
		t.Fatalf("fresh CPU: %v %v", known, err)
	}
	_, _, known, err = s.evaluate(ctx, rule, node, now.Add(61*time.Second))
	if err != nil || known {
		t.Fatalf("stale CPU: %v %v", known, err)
	}
}

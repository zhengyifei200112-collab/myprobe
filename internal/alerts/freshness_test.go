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

func TestRecoveryConfigurationPreservesExplicitZero(t *testing.T) {
	for _, value := range []int{0, 20, 2592000} {
		raw, err := normalizeRuleConfig("cpu", RuleConfig{DurationSeconds: 60, RecoverySeconds: &value})
		if err != nil {
			t.Fatal(err)
		}
		var decoded RuleConfig
		if err := json.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		if decoded.RecoverySeconds == nil || *decoded.RecoverySeconds != value {
			t.Fatalf("recovery lost: %s", raw)
		}
	}
	raw, err := normalizeRuleConfig("cpu", RuleConfig{DurationSeconds: 60})
	if err != nil {
		t.Fatal(err)
	}
	var decoded RuleConfig
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.RecoverySeconds == nil || *decoded.RecoverySeconds != 60 {
		t.Fatalf("default: %s", raw)
	}
	for _, invalid := range []int{-1, 2592001} {
		if _, err := normalizeRuleConfig("cpu", RuleConfig{RecoverySeconds: &invalid}); err == nil {
			t.Fatalf("accepted %d", invalid)
		}
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

func TestLatencyRecoveryRequiresEveryTargetToBeFresh(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "latency fixture"})
	if err != nil {
		t.Fatal(err)
	}
	s := New(db, "", &recordingSender{}, nil)
	rule := store.AlertRule{Kind: "latency", Config: json.RawMessage(`{"threshold_milliseconds":100}`)}
	now := time.Now().UTC()
	check := func(at time.Time, wantActive, wantKnown bool) {
		t.Helper()
		active, _, known, err := s.evaluate(ctx, rule, node, at)
		if err != nil || active != wantActive || known != wantKnown {
			t.Fatalf("active=%v known=%v err=%v; want %v/%v", active, known, err, wantActive, wantKnown)
		}
	}
	check(now, false, false)
	var targets []store.Target
	for i := 0; i < 2; i++ {
		target, err := db.CreateTarget(ctx, store.CreateTargetParams{Name: "fixture", Kind: protocol.TaskKindPing, Host: "example.com", IntervalSeconds: 60, TimeoutMS: 1000})
		if err != nil {
			t.Fatal(err)
		}
		if err := db.AssignTarget(ctx, node.ID, target.ID); err != nil {
			t.Fatal(err)
		}
		targets = append(targets, target)
	}
	save := func(index int, success bool, latency float64, at time.Time) {
		t.Helper()
		result := protocol.LatencyResult{TaskID: "fixture", TargetID: targets[index].ID, Success: success, LatencyMS: latency, CompletedAt: at}
		if !success {
			result.ErrorClass = "timeout"
		}
		if err := db.SaveLatencyResult(ctx, node.ID, protocol.TaskKindPing, result); err != nil {
			t.Fatal(err)
		}
	}
	check(now, false, false)
	save(0, true, 10, now)
	check(now, false, false) // another target has never reported
	save(0, false, 0, now.Add(time.Second))
	check(now.Add(time.Second), true, true) // a known failure proves a fault
	save(0, true, 10, now.Add(2*time.Second))
	save(1, true, 20, now.Add(2*time.Second))
	check(now.Add(2*time.Second), false, true)
	check(now.Add(183*time.Second), false, false) // both samples stale
	save(1, true, 150, now.Add(3*time.Second))
	check(now.Add(3*time.Second), true, true)
}

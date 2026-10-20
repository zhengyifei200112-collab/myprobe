package store

import (
	"context"
	"reflect"
	"testing"
	"time"
)

func TestHistorySeriesIdentityDoesNotReplacePublicTargetIDs(t *testing.T) {
	s, node, first := historyFixture(t)
	ctx := context.Background()
	base := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	targets := []Target{first}
	for range 2 {
		target, err := s.CreateTarget(ctx, CreateTargetParams{Name: "same name", Kind: "tcping", Host: "example.test", Port: first.Port, IntervalSeconds: 60, TimeoutMS: 1000})
		if err != nil {
			t.Fatal(err)
		}
		targets = append(targets, target)
	}
	for index, target := range targets {
		// Equal display metadata exercises the public-ID tie ordering. Current
		// kind/assignment cannot replace the two independently retained kinds.
		if _, err := s.db.Exec("UPDATE targets SET name='same name',sort_order=0,kind='ping' WHERE id=?", target.ID); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO latency_samples(node_id,target_id,kind,captured_at,success,latency_ms) VALUES(?,?,'tcping',?,1,?)`, node.ID, target.ID, formatTime(base.Add(610*time.Second)), 10+index); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO latency_rollups VALUES(?,?,'tcping',60,?,9,3,2,?)`, node.ID, target.ID, formatTime(base), 20+index); err != nil {
			t.Fatal(err)
		}
		if _, err := s.db.Exec(`INSERT INTO latency_rollups VALUES(?,?,'ping',300,?,2,2,0,0)`, node.ID, target.ID, formatTime(base.Add(300*time.Second))); err != nil {
			t.Fatal(err)
		}
	}
	end := base.Add(2 * time.Hour)
	args, err := historyQueryArgs(node.ID, base, end, 3600, false)
	if err != nil {
		t.Fatal(err)
	}
	want := previousLatencyPoints(t, s, args)
	if len(want) != 6 {
		t.Fatalf("reference series=%d, want three targets with both kinds", len(want))
	}
	for _, identities := range [][3]int64{{7, -5, 1 << 40}, {-900, 1 << 60, 42}} {
		for index, identity := range identities {
			if _, err := s.db.Exec("UPDATE targets SET rowid=? WHERE id=?", identity, targets[index].ID); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.LatencyHistoryRange(ctx, node.ID, base, end, 3600)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("internal identities %v changed public IDs/order/weighted points:\ngot %+v\nwant %+v", identities, got, want)
		}
	}
}

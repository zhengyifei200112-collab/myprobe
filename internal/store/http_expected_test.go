package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/protocol/httpcheck"
)

func TestHTTPSlotHalfOpenBoundaries(t *testing.T) {
	for _, tc := range []struct{ start, end, want int64 }{{0, 30, 1}, {0, 31, 2}, {1, 30, 0}, {30, 60, 1}, {31, 90, 1}, {0, 0, 0}, {-10, 1, 1}} {
		if got := countHTTPSlots(0, 30, tc.start, tc.end); got != tc.want {
			t.Fatalf("%+v got %d", tc, got)
		}
	}
}

func TestHTTPExpectedSurvivesRevisionAndDisable(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "expected.db"))
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
	start := v.UpdatedAt
	// No tasks or results exist: the planned denominator still contains slots.
	n, err := s.CountHTTPExpected(ctx, v.ID, node.ID, start, start.Add(time.Minute))
	if err != nil || n != 2 {
		t.Fatalf("offline slots: %d %v", n, err)
	}
	v.Enabled = false
	// Windows wall-clock precision can give successive saves the same timestamp;
	// a zero-length epoch correctly has no slots. Exercise a nonempty epoch here.
	deadline := time.Now().Add(time.Second)
	for !time.Now().After(start) {
		if time.Now().After(deadline) {
			t.Fatal("clock did not advance")
		}
		time.Sleep(time.Millisecond)
	}
	v, err = s.SaveHTTPService(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	n, err = s.CountHTTPExpected(ctx, v.ID, node.ID, start, v.UpdatedAt.Add(time.Minute))
	if err != nil || n != 1 {
		t.Fatalf("disabled slots: %d %v", n, err)
	}
	// Simulate a pre-018 schema and rerun its transactional backfill.
	v.Enabled = true
	v, err = s.SaveHTTPService(ctx, v)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.ExecContext(ctx, `DROP TABLE http_schedule_epochs; DELETE FROM schema_migrations WHERE version='018_http_schedule_epochs.sql'`); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	n, err = s.CountHTTPExpected(ctx, v.ID, node.ID, v.UpdatedAt, v.UpdatedAt.Add(30*time.Second))
	if err != nil || n != 1 {
		t.Fatalf("migration anchor: %d %v", n, err)
	}
	stats, err := s.HTTPServiceStatistics(ctx, v.ID, node.ID, v.CreatedAt.Add(-time.Minute), v.UpdatedAt.Add(time.Minute), v.UpdatedAt.Add(5*time.Minute))
	if err != nil || !stats.ScheduleKnownFrom.Equal(v.UpdatedAt) || !stats.Start.Equal(v.UpdatedAt) || stats.Expected != 2 {
		t.Fatalf("unknown prior revisions: %+v %v", stats, err)
	}
	prior, err := s.HTTPServiceStatistics(ctx, v.ID, node.ID, v.UpdatedAt.Add(-time.Minute), v.UpdatedAt, v.UpdatedAt.Add(5*time.Minute))
	if err != nil || !prior.Start.Equal(prior.End) || prior.Expected != 0 || prior.Coverage != nil || prior.SuccessRate != nil {
		t.Fatalf("unreconstructable history: %+v %v", prior, err)
	}
}

package store

import (
	"context"
	"encoding/json"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

func TestCachedAndOlderReportsDoNotDuplicateHistory(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "cached"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	for _, offset := range []time.Duration{0, 0, -time.Second, time.Second, time.Second} {
		report := protocol.Report{CapturedAt: at.Add(offset), Networks: []protocol.NetworkMetric{{Interface: "eth0", RXTotalBytes: 100}}}
		if err := s.SaveReport(ctx, node.ID, report); err != nil {
			t.Fatal(err)
		}
	}
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT count(*) FROM metric_samples WHERE node_id=?", node.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("stored %d samples, want 2", count)
	}
	var raw string
	if err := s.db.QueryRowContext(ctx, "SELECT report_json FROM metric_latest WHERE node_id=?", node.ID).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var latest protocol.Report
	if err := json.Unmarshal([]byte(raw), &latest); err != nil {
		t.Fatal(err)
	}
	if !latest.CapturedAt.Equal(at.Add(time.Second)) {
		t.Fatal("latest sample moved backward")
	}
	old := at.Add(-time.Hour).Format(time.RFC3339Nano)
	if _, err := s.db.ExecContext(ctx, "UPDATE nodes SET last_seen_at=? WHERE id=?", old, node.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveReport(ctx, node.ID, latest); err != nil {
		t.Fatal(err)
	}
	var seen string
	if err := s.db.QueryRowContext(ctx, "SELECT last_seen_at FROM nodes WHERE id=?", node.ID).Scan(&seen); err != nil {
		t.Fatal(err)
	}
	if seen == old {
		t.Fatal("cached report did not refresh liveness")
	}
}

func TestConcurrentCachedIngressAndFailureRemainAtomic(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "cached.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "synthetic cached ingress"})
	if err != nil {
		t.Fatal(err)
	}
	report := protocol.Report{CapturedAt: time.Now().UTC()}
	var wait sync.WaitGroup
	for range 8 {
		wait.Add(1)
		go func() {
			defer wait.Done()
			if err := s.SaveReportWithIngress(ctx, node.ID, report, "http", 1); err != nil {
				t.Error(err)
			}
		}()
	}
	wait.Wait()
	assertCount(t, s, `SELECT COUNT(*) FROM metric_samples`, 1)
	const old = "2000-01-01T00:00:00Z"
	if _, err := s.db.Exec(`UPDATE nodes SET last_seen_at=? WHERE id=?`, old, node.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER fail_cached_ingress BEFORE UPDATE ON node_report_ingress BEGIN SELECT RAISE(ABORT,'synthetic cached failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveReportWithIngress(ctx, node.ID, report, "websocket", 2); err == nil {
		t.Fatal("cached evidence failure was ignored")
	}
	var seen string
	if err := s.db.QueryRow(`SELECT last_seen_at FROM nodes WHERE id=?`, node.ID).Scan(&seen); err != nil || seen != old {
		t.Fatal("liveness escaped ingress rollback", err)
	}
	ingress, err := s.LastReportIngress(ctx, node.ID)
	if err != nil || ingress.Transport != "http" {
		t.Fatal("failed cached ingress replaced source", err)
	}
}

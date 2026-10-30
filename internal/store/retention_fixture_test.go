package store

import (
	"context"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"path/filepath"
	"testing"
	"time"
)

func historyCapacityReport(at time.Time, sample int) protocol.Report {
	return protocol.Report{CapturedAt: at, CPU: protocol.CPUMetric{UsagePercent: 10}, Memory: protocol.MemoryMetric{TotalBytes: 1024, UsedBytes: 512}, Disks: []protocol.DiskMetric{{Mount: "synthetic", TotalBytes: 2048, UsedBytes: 1024}}, Networks: []protocol.NetworkMetric{{Interface: "synthetic", RXTotalBytes: uint64(1000 + sample*600), TXTotalBytes: uint64(2000 + sample*300), RXBytesPerS: 40, TXBytesPerS: 20}}}
}

// Build the actual current predecessor, including050/051, using a new owned
// empty fixture; no historical046/048 dependency is assumed.
func preHistoryAnchorStore(t *testing.T) *Store {
	t.Helper()
	s, err := Open(context.Background(), filepath.Join(httpFixtureDir(t), "pre049.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	if _, err = s.db.Exec(`DROP TABLE history_retention_anchors; DELETE FROM schema_migrations WHERE version='049_history_retention_anchors.sql'`); err != nil {
		t.Fatal(err)
	}
	return s
}

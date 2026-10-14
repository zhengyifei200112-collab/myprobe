package store

import (
	"context"
	"testing"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func TestReportIngressSameTransactionAndUnknownInternalSource(t *testing.T) {
	ctx := context.Background()
	db, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node, _, err := db.CreateNode(ctx, CreateNodeParams{Name: "synthetic ingress node"})
	if err != nil {
		t.Fatal(err)
	}
	report := protocol.Report{CapturedAt: time.Now().UTC(), Memory: protocol.MemoryMetric{TotalBytes: 1024, UsedBytes: 512}}
	if err = db.SaveReportWithIngress(ctx, node.ID, report, "http", 1); err != nil {
		t.Fatal(err)
	}
	evidence, err := db.LastReportIngress(ctx, node.ID)
	if err != nil || evidence.Status != "observed" || evidence.Transport != "http" || evidence.WireVersion == nil || *evidence.WireVersion != 1 || !evidence.CapturedAt.Equal(report.CapturedAt) {
		t.Fatalf("ingress: %+v %v", evidence, err)
	}
	if _, err = db.db.ExecContext(ctx, `CREATE TRIGGER synthetic_ingress_failure BEFORE UPDATE ON node_report_ingress BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`); err != nil {
		t.Fatal(err)
	}
	later := report
	later.CapturedAt = report.CapturedAt.Add(time.Second)
	if err = db.SaveReportWithIngress(ctx, node.ID, later, "websocket", 2); err == nil {
		t.Fatal("failed evidence persistence succeeded")
	}
	current, err := db.LatestReport(ctx, node.ID)
	if err != nil || current == nil || !current.CapturedAt.Equal(report.CapturedAt) {
		t.Fatal("report/evidence partially committed")
	}
	if _, err = db.db.ExecContext(ctx, `DROP TRIGGER synthetic_ingress_failure`); err != nil {
		t.Fatal(err)
	}
	if err = db.SaveReportWithIngress(ctx, node.ID, later, "websocket", 2); err != nil {
		t.Fatal(err)
	}
	evidence, err = db.LastReportIngress(ctx, node.ID)
	if err != nil || evidence.Transport != "websocket" || *evidence.WireVersion != 2 {
		t.Fatal("actual source switch not recorded")
	}
	if err = db.SaveReport(ctx, node.ID, later); err != nil {
		t.Fatal(err)
	}
	evidence, err = db.LastReportIngress(ctx, node.ID)
	if err != nil || evidence.Status != "unavailable" || evidence.Transport != "" || evidence.WireVersion != nil {
		t.Fatal("internal write inherited an earlier source")
	}
}

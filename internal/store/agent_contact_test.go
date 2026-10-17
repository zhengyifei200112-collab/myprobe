package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func TestAgentContactDoesNotRefreshMetricsOrConfiguration(t *testing.T) {
	ctx := context.Background()
	s, node, _ := historyFixture(t)
	now := time.Now().UTC()
	report := protocol.Report{CapturedAt: now.Add(-time.Hour), CPU: protocol.CPUMetric{UsagePercent: 91}}
	if err := s.SaveReportWithIngress(ctx, node.ID, report, "websocket", 2); err != nil {
		t.Fatal(err)
	}
	before, err := s.LastReportIngress(ctx, node.ID)
	if err != nil {
		t.Fatal(err)
	}
	epoch := strings.Repeat("a", 32)
	if _, err := s.BeginAgentConnection(ctx, node.ID, epoch, 2, false, now); err != nil {
		t.Fatal(err)
	}
	var revision, sampling int64
	if err := s.db.QueryRow(`SELECT config_revision,agent_config_revision FROM nodes WHERE id=?`, node.ID).Scan(&revision, &sampling); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordAgentHeartbeat(ctx, node.ID, epoch, now.Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	item, err := s.PublicNode(ctx, node.ID, now.Add(time.Second))
	if err != nil || !item.Online || !item.Stale || !item.Report.CapturedAt.Equal(report.CapturedAt) {
		t.Fatalf("fresh contact hid stale capture: %+v %v", item, err)
	}
	after, err := s.LastReportIngress(ctx, node.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatal("heartbeat fabricated report ingress", err)
	}
	assertCount(t, s, `SELECT count(*) FROM metric_samples`, 1)
	var currentRevision, currentSampling int64
	if err := s.db.QueryRow(`SELECT config_revision,agent_config_revision FROM nodes WHERE id=?`, node.ID).Scan(&currentRevision, &currentSampling); err != nil || currentRevision != revision || currentSampling != sampling {
		t.Fatal("heartbeat invalidated batch/configuration revisions", err)
	}
	if _, err := s.BeginAgentConnection(ctx, node.ID, strings.Repeat("b", 32), 1, false, now.Add(2*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.RecordAgentHeartbeat(ctx, node.ID, epoch, now.Add(3*time.Second)); !errors.Is(err, ErrConfigurationSuperseded) {
		t.Fatal("replaced epoch refreshed contact", err)
	}
	item, err = s.PublicNode(ctx, node.ID, now.Add(4*time.Second))
	if err != nil || !item.Node.LastSeenAt.Equal(now.Add(time.Second)) {
		t.Fatal("rejected heartbeat changed last contact", err)
	}
}

func TestPublicFreshnessUsesCaptureAndBothSamplingPeriods(t *testing.T) {
	for _, tc := range []struct {
		name               string
		collection, report int
		age                time.Duration
		stale              bool
	}{
		{"boundary", 5, 5, 12 * time.Second, false},
		{"past boundary", 5, 5, 12*time.Second + time.Nanosecond, true},
		{"slow collection", 3600, 5, time.Hour, false},
		{"slow reporting", 5, 3600, time.Hour, false},
		{"slow capture stale", 3600, 5, 2*time.Hour + time.Nanosecond, true},
		{"small skew", 5, 5, -30 * time.Second, false},
		{"future unusable", 5, 5, -31 * time.Second, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, node, _ := historyFixture(t)
			ctx := context.Background()
			now := time.Now().UTC()
			if _, err := s.db.Exec(`UPDATE nodes SET collection_seconds=?,report_seconds=? WHERE id=?`, tc.collection, tc.report, node.ID); err != nil {
				t.Fatal(err)
			}
			if err := s.SaveReport(ctx, node.ID, protocol.Report{CapturedAt: now.Add(-tc.age)}); err != nil {
				t.Fatal(err)
			}
			item, err := s.PublicNode(ctx, node.ID, now)
			if err != nil || !item.Online || item.Stale != tc.stale {
				t.Fatalf("capture freshness: %+v %v", item, err)
			}
		})
	}
}

func TestAgentHelloContactAndMetadataAreAtomic(t *testing.T) {
	s, node, _ := historyFixture(t)
	ctx := context.Background()
	initial := protocol.Hello{AgentVersion: "original", OS: "linux"}
	if err := s.SaveAgentMetadata(ctx, node.ID, initial); err != nil {
		t.Fatal(err)
	}
	old := time.Now().UTC().Add(-time.Hour)
	if _, err := s.db.Exec(`UPDATE nodes SET last_seen_at=? WHERE id=?`, formatTime(old), node.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER reject_metadata BEFORE UPDATE ON node_agent_metadata BEGIN SELECT RAISE(ABORT,'synthetic metadata failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveAgentMetadata(ctx, node.ID, protocol.Hello{AgentVersion: "replacement"}); err == nil {
		t.Fatal("partial contact/metadata commit succeeded")
	}
	item, err := s.AdminNode(ctx, node.ID, time.Now().UTC())
	if err != nil || !item.Node.LastSeenAt.Equal(old) || item.Node.Agent.AgentVersion != "original" {
		t.Fatal("failed hello partially changed metadata/contact", err)
	}
	if _, err := s.db.Exec(`DROP TRIGGER reject_metadata`); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveAgentMetadata(ctx, node.ID, initial); err != nil {
		t.Fatal(err)
	}
	item, err = s.PublicNode(ctx, node.ID, time.Now().UTC())
	if err != nil || !item.Online || item.Report != nil || item.Node.Agent.Hostname != "" || item.Node.Agent.AgentVersion != "" {
		t.Fatal("hello did not produce private/masked contact-only state", err)
	}
	assertCount(t, s, `SELECT count(*) FROM metric_samples`, 0)
	assertCount(t, s, `SELECT count(*) FROM node_report_ingress`, 0)
}

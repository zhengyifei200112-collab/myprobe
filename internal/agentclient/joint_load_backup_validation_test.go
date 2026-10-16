package agentclient

import (
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestJointBackupOverlapCountsOnlyCommittedInWindow(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	end := start.Add(time.Minute)
	p := &jointReportObserver{attempts: map[jointAttemptKey]*jointReportAttempt{}}
	items := []jointReportAttempt{
		{node: "one", forwarded: true, acknowledged: true, sentAt: start, ackAt: start.Add(time.Second)},
		{node: "one", forwarded: true, acknowledged: true, sentAt: start.Add(time.Second), ackAt: start.Add(2 * time.Second)},
		{node: "two", forwarded: true, acknowledged: true, sentAt: start.Add(time.Second), ackAt: end.Add(-time.Nanosecond)},
		{node: "old", forwarded: true, acknowledged: true, sentAt: start.Add(-time.Nanosecond), ackAt: start.Add(time.Second)},
		{node: "late", forwarded: true, acknowledged: true, sentAt: start.Add(time.Second), ackAt: end},
		{node: "unconfirmed", forwarded: true, sentAt: start.Add(time.Second)},
		{node: "unforwarded", acknowledged: true, sentAt: start.Add(time.Second), ackAt: start.Add(2 * time.Second)},
		{node: "reversed", forwarded: true, acknowledged: true, sentAt: start.Add(time.Second), ackAt: start},
	}
	for i := range items {
		p.attempts[jointAttemptKey{items[i].node, uint64(i + 1)}] = &items[i]
	}
	count, nodes := p.acknowledgedDuring(start, end)
	if count != 3 || nodes != 2 {
		t.Fatal("old, late or unconfirmed report counted", count, nodes)
	}
}

func TestJointBackupDoesNotSubstituteCompletionForOverlap(t *testing.T) {
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	b := &jointBackupController{complete: true, backupStart: start, recoveryStart: start.Add(time.Minute), evidence: jointBackupEvidence{ArchiveCompletedAt: start.Add(time.Minute), RecoveryCompletedAt: start.Add(2 * time.Minute), Recovered: store.BackupRecoveryEvidence{Nodes: 5, Users: 1, Channels: 1, Reports: 100}, CompletionPreservedAfterRestart: true}}
	p := &jointReportObserver{attempts: map[jointAttemptKey]*jointReportAttempt{}}
	if _, err := b.finish(p, 5, 100); err == nil {
		t.Fatal("completed artifact without in-operation ACKs was accepted")
	}
	b.complete = false
	if b.readyForRestart() {
		t.Fatal("intentional restart would interrupt active backup")
	}
	var disabled *jointBackupController
	if !disabled.readyForRestart() {
		t.Fatal("disabled backup changed normal fault schedule")
	}
}

func TestJointBackupRetainedSeedPopulationUsesExactCutoff(t *testing.T) {
	anchor := time.Date(2026, 1, 10, 0, 0, 0, 0, time.UTC)
	history := jointHistorySeed{Anchor: anchor, MetricRows: 5 * (7*24*60*4 + 2)}
	retention := 7 * 24 * time.Hour
	for _, tc := range []struct {
		offset time.Duration
		want   int64
	}{{-15 * time.Second, history.MetricRows}, {0, history.MetricRows - 5}, {time.Nanosecond, history.MetricRows - 10}, {15 * time.Second, history.MetricRows - 10}, {retention + time.Second, 0}} {
		got, err := minimumRetainedJointSeedReports(history, 5, anchor.Add(tc.offset), retention)
		if err != nil || got != tc.want {
			t.Fatalf("offset %s: got %d, want %d: %v", tc.offset, got, tc.want, err)
		}
	}
	if _, err := minimumRetainedJointSeedReports(history, 0, anchor, retention); err == nil {
		t.Fatal("invalid node count accepted")
	}
}

package store

import (
	"context"
	"strings"
	"testing"
	"time"

	quality "github.com/zhengyifei200112-collab/myprobe/internal/networkquality"
	check "github.com/zhengyifei200112-collab/myprobe/internal/protocol/networkcheck"
	v1 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func TestNetworkQualityEvidenceSurvivesMixedRetentionAndReopen(t *testing.T) {
	s, node, target, epoch, _ := networkTaskFixture(t)
	ctx := context.Background()
	anchor := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	connection := strings.Repeat("d", 32)
	if _, err := s.db.Exec(`UPDATE network_schedule_epochs SET starts_at=?,interval_seconds=5,packet_count=3,timeout_ms=1500 WHERE id=?`, anchor.Unix(), epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE network_schedule_metadata SET known_since=?,known_results_since=?`, anchor.Unix(), anchor.Unix()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginAgentConnection(ctx, node, connection, 2, false, anchor); err != nil {
		t.Fatal(err)
	}
	wantEvidence := quality.EvidenceTotals{}
	for i := range 500 {
		slot := anchor.Add(time.Duration(i) * 5 * time.Second)
		if i%7 == 6 {
			continue
		} // No manufactured task or evidence for a missing slot.
		task, err := s.ClaimNetworkTask(ctx, epoch, slot, slot.Add(10*time.Millisecond))
		if err != nil {
			t.Fatal(err)
		}
		if i%7 == 0 {
			_, err = s.AcceptNetworkResult(ctx, node, target.Kind, v1.LatencyResult{TaskID: task.Task.ID, TargetID: target.ID, Success: true, LatencyMS: 20, CompletedAt: slot.Add(time.Second)}, slot.Add(time.Second))
			if err != nil {
				t.Fatal(err)
			}
			continue // Real legacy path has no family or packet proof.
		}
		if err = s.RequireNetworkEvidence(ctx, task, connection); err != nil {
			t.Fatal(err)
		}
		rtt := float64(i % 100)
		r := check.Result{TaskID: task.Task.ID, TargetID: target.ID, Kind: target.Kind, Outcome: check.Success, AddressFamily: check.FamilyIPv4, RTTMS: &rtt, Packets: &check.PacketCounts{Sent: 3, Received: 2}, CompletedAt: slot.Add(time.Second)}
		switch i % 7 {
		case 1:
			r.AddressFamily = check.FamilyIPv6
			wantEvidence.IPv6++
			wantEvidence.PacketSuccess++
			wantEvidence.Sent += 3
			wantEvidence.Received += 2
		case 2:
			r.Packets = &check.PacketCounts{Sent: 2}
			r.Outcome = check.Failure
			r.ErrorClass = "timeout"
			r.RTTMS = nil
			wantEvidence.IPv4++
			wantEvidence.PacketFailure++
			wantEvidence.Sent += 2
		case 3:
			r.Packets = nil
			wantEvidence.IPv4++ // Valid RTT, but tool did not supply reliable packet counts.
		case 4:
			r.Outcome = check.Unobserved
			r.ErrorClass = "dns"
			r.AddressFamily = check.FamilyUnknown
			r.RTTMS = nil
			r.Packets = nil
		case 5:
			r.Outcome = check.Unobserved
			r.ErrorClass = "send_error"
			r.AddressFamily = check.FamilyUnknown
			r.RTTMS = nil
			r.Packets = nil
		}
		if ok, err := s.AcceptNetworkProbeResult(ctx, node, connection, r, slot.Add(time.Second)); err != nil || !ok {
			t.Fatal(err)
		}
	}
	q := NetworkQualityQuery{NodeID: node, TargetID: target.ID, Kind: target.Kind, Start: anchor, End: anchor.Add(45 * time.Minute), AsOf: anchor.Add(50 * time.Minute), Series: true}
	want, err := s.PublicNetworkQuality(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if want.Totals.Evidence != wantEvidence {
		t.Fatal("raw evidence differs", want.Totals.Evidence, wantEvidence)
	}
	checkTotals := func(got NetworkQualityStats) {
		t.Helper()
		assertNetworkQualityEquivalent(t, want, got)
		if got.Totals.Evidence != wantEvidence {
			t.Fatal("archive changed evidence", got.Totals.Evidence, wantEvidence)
		}
		var sum quality.Totals
		for _, point := range got.Points {
			if err := sum.Merge(point.Stats.Totals); err != nil {
				t.Fatal(err)
			}
		}
		if sum.Evidence != wantEvidence || sum.Success != got.Success || sum.Failure != got.Failure || sum.NotExecuted != got.NotExecuted {
			t.Fatal("series/range populations differ", sum.Evidence, got.Totals.Evidence)
		}
	}
	checkTotals(want)
	policy := RetentionPolicy{Raw: 10 * time.Minute, OneMinute: 30 * time.Minute, FiveMinute: 2 * time.Hour}
	for pass := range 10 {
		batch, err := s.RetainNetworkQuality(ctx, q.AsOf, policy)
		if err != nil {
			t.Fatal(err)
		}
		got, err := s.PublicNetworkQuality(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		checkTotals(got)
		if !batch.More {
			break
		}
		if pass == 9 {
			t.Fatal("retention did not drain")
		}
	}
	for _, tier := range []int{60, 300} {
		var count int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM network_quality_rollups WHERE bucket_seconds=?`, tier).Scan(&count); err != nil || count == 0 {
			t.Fatal("missing archive tier", tier, err)
		}
	}
	reopened, err := Open(ctx, s.DatabasePath())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got, err := reopened.PublicNetworkQuality(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	checkTotals(got)
}

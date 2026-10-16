package store

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	quality "github.com/zhengyifei200112-collab/myprobe/internal/networkquality"
	check "github.com/zhengyifei200112-collab/myprobe/internal/protocol/networkcheck"
	v1 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func networkEvidenceFixture(t *testing.T) (*Store, string, string, NetworkTask, check.Result, time.Time) {
	t.Helper()
	s, node, target, epoch, now := networkTaskFixture(t)
	ctx := context.Background()
	connection := strings.Repeat("a", 32)
	if _, err := s.db.Exec(`UPDATE network_schedule_epochs SET address_family='ipv4' WHERE id=?`, epoch); err != nil {
		t.Fatal(err)
	}
	if _, err := s.BeginAgentConnection(ctx, node, connection, 2, false, now); err != nil {
		t.Fatal(err)
	}
	task, err := s.ClaimNetworkTask(ctx, epoch, now.Truncate(time.Second), now)
	if err != nil {
		t.Fatal(err)
	}
	rtt := 12.5
	r := check.Result{TaskID: task.Task.ID, TargetID: target.ID, Kind: target.Kind, Outcome: check.Success, AddressFamily: check.FamilyIPv4, RTTMS: &rtt, Packets: &check.PacketCounts{Sent: 1, Received: 1}, CompletedAt: now.Add(100 * time.Millisecond)}
	return s, node, connection, task, r, now.Add(200 * time.Millisecond)
}

func TestNetworkProbeAdmissionFencesRollbackAndReplay(t *testing.T) {
	s, node, connection, task, r, now := networkEvidenceFixture(t)
	ctx := context.Background()
	reject := func(node, epoch string, value check.Result) {
		t.Helper()
		if _, err := s.AcceptNetworkProbeResult(ctx, node, epoch, value, now); !errors.Is(err, ErrNetworkTaskRejected) {
			t.Fatal("unbound evidence accepted", err)
		}
	}
	reject(node, connection, r) // No dispatch evidence obligation yet.
	stale := task
	stale.LeaseToken = "wrong"
	if err := s.RequireNetworkEvidence(ctx, stale, connection); !errors.Is(err, ErrNetworkTaskRejected) {
		t.Fatal("stale lease bound", err)
	}
	if err := s.RequireNetworkEvidence(ctx, task, connection); err != nil {
		t.Fatal(err)
	}
	reject("other-node", connection, r)
	reject(node, strings.Repeat("b", 32), r)
	for _, mutate := range []func(*check.Result){func(v *check.Result) { v.AddressFamily = check.FamilyIPv6 }, func(v *check.Result) { v.Kind = v1.TaskKindTCPing; v.Packets = nil }, func(v *check.Result) { v.TargetID = "wrong" }, func(v *check.Result) { v.Packets = &check.PacketCounts{Sent: 2, Received: 1} }} {
		bad := r
		mutate(&bad)
		reject(node, connection, bad)
	}
	if _, err := s.AcceptNetworkResult(ctx, node, task.Task.Kind, v1.LatencyResult{TaskID: r.TaskID, TargetID: r.TargetID, Success: true, LatencyMS: 12.5, CompletedAt: r.CompletedAt}, now); !errors.Is(err, ErrNetworkTaskRejected) {
		t.Fatal("v2 obligation downgraded", err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER synthetic_proof_failure BEFORE UPDATE ON network_probe_tasks WHEN NEW.state='completed' BEGIN SELECT RAISE(ABORT,'synthetic failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcceptNetworkProbeResult(ctx, node, connection, r, now); err == nil {
		t.Fatal("failed transaction acknowledged")
	}
	assertCount(t, s, `SELECT COUNT(*) FROM latency_samples`, 0)
	assertCount(t, s, `SELECT COUNT(*) FROM network_probe_tasks WHERE state='sending' AND result_json IS NULL`, 1)
	if _, err := s.db.Exec(`DROP TRIGGER synthetic_proof_failure`); err != nil {
		t.Fatal(err)
	}
	// A new authenticated generation may replay a task sent before reconnect;
	// the former generation loses admission before the new socket is published.
	newConnection := strings.Repeat("c", 32)
	if _, err := s.BeginAgentConnection(ctx, node, newConnection, 2, false, now); err != nil {
		t.Fatal(err)
	}
	reject(node, connection, r)
	accepted, err := s.AcceptNetworkProbeResult(ctx, node, newConnection, r, now)
	if err != nil || !accepted {
		t.Fatal("current generation retry failed", err)
	}
	reopened, err := Open(ctx, s.DatabasePath())
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	r.CompletedAt = r.CompletedAt.In(time.FixedZone("canonical", 8*3600))
	accepted, err = reopened.AcceptNetworkProbeResult(ctx, node, newConnection, r, now.Add(time.Minute))
	if err != nil || accepted {
		t.Fatal("post-expiry durable replay changed population", err)
	}
	conflict := r
	conflict.Packets = &check.PacketCounts{Sent: 1, Received: 0}
	conflict.Outcome = check.Failure
	conflict.ErrorClass = "timeout"
	conflict.RTTMS = nil
	if _, err = reopened.AcceptNetworkProbeResult(ctx, node, newConnection, conflict, now); !errors.Is(err, ErrNetworkTaskRejected) {
		t.Fatal("conflicting replay accepted", err)
	}
	assertCount(t, s, `SELECT COUNT(*) FROM latency_samples`, 1)
	assertCount(t, s, `SELECT COUNT(*) FROM network_probe_tasks WHERE result_format='network.v1' AND state='completed'`, 1)
}

func TestNetworkProbeUnobservedAndExpiredAreNotTargetLoss(t *testing.T) {
	for _, reason := range []string{"dns", "busy", "unsupported", "cancelled", "no_address", "no_packets_sent", "send_error", "internal"} {
		t.Run(reason, func(t *testing.T) {
			s, node, connection, task, r, now := networkEvidenceFixture(t)
			ctx := context.Background()
			if err := s.RequireNetworkEvidence(ctx, task, connection); err != nil {
				t.Fatal(err)
			}
			if _, err := s.AcceptNetworkProbeResult(ctx, node, connection, r, task.Task.ExpiresAt.Add(time.Nanosecond)); !errors.Is(err, ErrNetworkTaskRejected) {
				t.Fatal("late first result accepted", err)
			}
			r.Outcome = check.Unobserved
			r.ErrorClass = reason
			r.AddressFamily = check.FamilyUnknown
			r.RTTMS = nil
			r.Packets = nil
			if ok, err := s.AcceptNetworkProbeResult(ctx, node, connection, r, now); err != nil || !ok {
				t.Fatal(err)
			}
			assertCount(t, s, `SELECT COUNT(*) FROM latency_samples`, 0)
			q := NetworkQualityQuery{NodeID: node, TargetID: r.TargetID, Kind: r.Kind, Start: now.Truncate(time.Second), End: now.Add(10 * time.Second), AsOf: now.Add(20 * time.Second)}
			got, err := s.PublicNetworkQuality(ctx, q)
			if err != nil || got.NotExecuted != 1 || got.Failure != 0 || got.Totals.Evidence != (quality.EvidenceTotals{}) {
				t.Fatal("unexecuted became measured loss", got, err)
			}
		})
	}
}

func TestNetworkProbeConcurrentReplayOnlyInsertsOnce(t *testing.T) {
	s, node, connection, task, r, now := networkEvidenceFixture(t)
	ctx := context.Background()
	if err := s.RequireNetworkEvidence(ctx, task, connection); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var inserted atomic.Int64
	for range 8 {
		wg.Go(func() {
			ok, err := s.AcceptNetworkProbeResult(ctx, node, connection, r, now)
			if err != nil {
				t.Error(err)
			}
			if ok {
				inserted.Add(1)
			}
		})
	}
	wg.Wait()
	if inserted.Load() != 1 {
		t.Fatal("duplicate receipts inserted", inserted.Load())
	}
	assertCount(t, s, `SELECT COUNT(*) FROM latency_samples`, 1)
}

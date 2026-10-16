package agentgateway

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"
	"time"

	check "github.com/zhengyifei200112-collab/myprobe/internal/protocol/networkcheck"
	v1 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestNetworkExplicitProfileNeverFallsBackToLegacyTask(t *testing.T) {
	for _, family := range []string{check.FamilyIPv6, check.FamilyAuto} {
		t.Run(family, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			path := filepath.Join(t.TempDir(), "synthetic-profile.db")
			db, err := store.Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "Synthetic legacy profile observer"})
			if err != nil {
				t.Fatal(err)
			}
			target, err := db.CreateTarget(ctx, store.CreateTargetParams{Name: "Synthetic explicit profile", Kind: v1.TaskKindPing, Host: "example.test", IntervalSeconds: 60, TimeoutMS: 3000, AddressFamily: family, PacketCount: 2})
			if err != nil {
				t.Fatal(err)
			}
			if err = db.AssignTarget(ctx, node.ID, target.ID); err != nil {
				t.Fatal(err)
			}
			assignments, err := db.ListNetworkAssignments(ctx, time.Now().Add(2*time.Second))
			if err != nil || len(assignments) != 1 {
				t.Fatal("missing immutable plan", err)
			}
			assignment := assignments[0]
			if delay := time.Until(assignment.ScheduledAt); delay > 0 {
				timer := time.NewTimer(delay)
				defer timer.Stop()
				select {
				case <-timer.C:
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			gateway := New(db, NewHub())
			// A nil transport makes any attempted fallback fail the test. The
			// legacy session must be classified before pending/write handling.
			gateway.register(node.ID, &agentSession{version: v1.Version})
			if err = gateway.DispatchNetwork(ctx, assignment.EpochID, assignment.ScheduledAt); !errors.Is(err, store.ErrNetworkTaskRejected) {
				t.Fatalf("unsupported dispatch = %v", err)
			}
			raw, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer raw.Close()
			var format, payload, source string
			var sent sql.NullInt64
			var samples int
			if err = raw.QueryRow(`SELECT result_format,result_json,sent_at_ns FROM network_probe_tasks WHERE epoch_id=?`, assignment.EpochID).Scan(&format, &payload, &sent); err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err = json.Unmarshal([]byte(payload), &fields); err != nil {
				t.Fatal(err)
			}
			source, _ = fields["source"].(string)
			if format != "dispatch.v1" || source != "server_dispatch" || sent.Valid {
				t.Fatal("unsupported was represented as an Agent result or sent task")
			}
			if err = raw.QueryRow(`SELECT COUNT(*) FROM latency_samples`).Scan(&samples); err != nil || samples != 0 {
				t.Fatal("dispatcher invented a latency sample")
			}
			var forged v1.LatencyResult
			if err = json.Unmarshal([]byte(payload), &forged); err != nil {
				t.Fatal(err)
			}
			if _, err = db.AcceptNetworkResult(ctx, node.ID, v1.TaskKindPing, forged, time.Now()); !errors.Is(err, store.ErrNetworkTaskRejected) {
				t.Fatal("server dispatch projection authorized an Agent ACK", err)
			}
			if len(gateway.pending) != 0 {
				t.Fatal("unwritten task became pending transport evidence")
			}
		})
	}
}

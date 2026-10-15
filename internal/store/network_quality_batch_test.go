package store

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	quality "github.com/zhengyifei200112-collab/myprobe/internal/networkquality"
)

func TestNetworkQualityBatchScopeAndMissingPlan(t *testing.T) {
	s, single := networkQualityFixture(t)
	ctx := context.Background()
	hidden, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "Synthetic hidden comparison"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.UpdateNode(ctx, hidden.ID, UpdateNodeParams{Name: hidden.Name, Hidden: true, LatencyMode: "ping", CollectionSeconds: 15, ReportSeconds: 15}); err != nil {
		t.Fatal(err)
	}
	if err = s.AssignTarget(ctx, hidden.ID, single.TargetID); err != nil {
		t.Fatal(err)
	}
	empty, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "Synthetic no retained plan"})
	if err != nil {
		t.Fatal(err)
	}
	q := NetworkQualityBatchQuery{NodeIDs: []string{single.NodeID, empty.ID}, TargetID: single.TargetID, Kind: single.Kind, Start: single.Start, End: single.End, AsOf: single.AsOf}
	got, err := s.PublicNetworkQualityBatch(ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.Nodes) != 2 || got.Nodes[0].Stats.Success != 3 || got.Nodes[1].Stats.Availability != "no_plan_history" || got.Nodes[1].Stats.Coverage != nil {
		t.Fatal("comparison fabricated absent plans", got)
	}
	q.NodeIDs = []string{single.NodeID, hidden.ID}
	if got, err = s.PublicNetworkQualityBatch(ctx, q); !errors.Is(err, ErrNotFound) || len(got.Nodes) != 0 {
		t.Fatal("mixed hidden batch returned a partial projection", err)
	}
	if _, err = s.AdminNetworkQualityBatch(ctx, q); err != nil {
		t.Fatal("admin lost hidden scope", err)
	}
	share, err := s.CreateChartShare(ctx, "Synthetic restricted quality", "synthetic hash", []string{hidden.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SharedNetworkQualityBatch(ctx, share.ID, q); !errors.Is(err, ErrNotFound) {
		t.Fatal("share crossed explicit node membership", err)
	}
	q.NodeIDs = []string{hidden.ID}
	if _, err = s.SharedNetworkQualityBatch(ctx, share.ID, q); err != nil {
		t.Fatal("explicit private share lost hidden node", err)
	}
	if _, err = s.UpdateChartShare(ctx, share.ID, share.Name, nil, share.NodeIDs, false); err != nil {
		t.Fatal(err)
	}
	if _, err = s.SharedNetworkQualityBatch(ctx, share.ID, q); !errors.Is(err, ErrNotFound) {
		t.Fatal("disabled share authorized", err)
	}
	q.NodeIDs = []string{single.NodeID, single.NodeID}
	if _, err = s.PublicNetworkQualityBatch(ctx, q); !errors.Is(err, quality.ErrInvalid) {
		t.Fatal("duplicate nodes accepted", err)
	}
	q.NodeIDs = []string{single.NodeID}
	q.End = q.Start.Add(366 * 24 * time.Hour)
	if _, err = s.PublicNetworkQualityBatch(ctx, q); !errors.Is(err, quality.ErrInvalid) {
		t.Fatal("oversized range accepted", err)
	}
}

func TestNetworkQualityReadKeepsEstablishedSnapshot(t *testing.T) {
	s, q := networkQualityFixture(t)
	ctx := context.Background()
	tx, err := s.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	var name string
	if err = tx.QueryRow(`SELECT name FROM nodes WHERE id=?`, q.NodeID).Scan(&name); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`UPDATE network_probe_tasks SET result_json='{}' WHERE state='completed' AND source='scheduled'`); err != nil {
		t.Fatal(err)
	}
	got, err := readNetworkQualityTx(ctx, tx, q, false)
	if err != nil || got.Success != 3 {
		t.Fatal("statistics left authorized read snapshot", err)
	}
	if err = tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if _, err = s.PublicNetworkQuality(ctx, q); !errors.Is(err, ErrNetworkQualityUnavailable) {
		t.Fatal("later snapshot did not see corruption", err)
	}
}

func TestNetworkQualityRejectsUnrepresentableHistoryMetadata(t *testing.T) {
	for _, query := range []string{`UPDATE network_schedule_metadata SET known_results_since=9223372036854775807`, `UPDATE network_schedule_epochs SET starts_at=-1`} {
		t.Run(query, func(t *testing.T) {
			s, q := networkQualityFixture(t)
			if _, err := s.db.Exec(query); err != nil {
				t.Fatal(err)
			}
			if _, err := s.PublicNetworkQuality(context.Background(), q); !errors.Is(err, ErrNetworkQualityUnavailable) {
				t.Fatal("unrepresentable history became serializable statistics", err)
			}
		})
	}
}

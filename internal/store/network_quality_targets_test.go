package store

import (
	"context"
	"errors"
	"fmt"
	"testing"

	quality "github.com/zhengyifei200112-collab/myprobe/internal/networkquality"
)

func TestNetworkQualityTargetAliasesPagesScopesAndHistory(t *testing.T) {
	s, node, target, _, _ := networkTaskFixture(t)
	ctx := context.Background()
	for i := 0; i < 130; i++ {
		tgt, err := s.CreateTarget(ctx, CreateTargetParams{Name: fmt.Sprintf("Synthetic alias %03d", i), Kind: "ping", Host: "private-target.example.test", IntervalSeconds: 60, TimeoutMS: 1000})
		if err != nil {
			t.Fatal(err)
		}
		if err = s.AssignTarget(ctx, node, tgt.ID); err != nil {
			t.Fatal(err)
		}
	}
	first, err := s.PublicNetworkQualityTargets(ctx, []string{node}, "")
	if err != nil || len(first.Targets) != 128 || first.NextCursor == "" {
		t.Fatal("target page is unbounded or lost its cursor", err)
	}
	second, err := s.PublicNetworkQualityTargets(ctx, []string{node}, first.NextCursor)
	if err != nil || len(second.Targets) != 3 || second.NextCursor != "" {
		t.Fatal("target keyset dropped remaining aliases", err)
	}
	seen := map[string]bool{}
	for _, tgt := range append(first.Targets, second.Targets...) {
		key := tgt.Kind + ":" + tgt.ID
		if seen[key] {
			t.Fatal("duplicate target across pages")
		}
		seen[key] = true
	}
	other, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "Synthetic other scope"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.PublicNetworkQualityTargets(ctx, []string{other.ID}, first.NextCursor); !errors.Is(err, quality.ErrInvalid) {
		t.Fatal("cursor crossed node scope", err)
	}
	if _, err = s.AdminNetworkQualityTargets(ctx, []string{node}, first.NextCursor); !errors.Is(err, quality.ErrInvalid) {
		t.Fatal("cursor crossed privilege context", err)
	}
	if _, err = s.db.Exec(`DELETE FROM node_targets WHERE node_id=? AND target_id=?`, node, target.ID); err != nil {
		t.Fatal(err)
	}
	page, err := s.PublicNetworkQualityTargets(ctx, []string{node}, "")
	if err != nil {
		t.Fatal(err)
	}
	present := false
	for {
		for _, v := range page.Targets {
			if v.ID == target.ID {
				present = true
			}
		}
		if page.NextCursor == "" {
			break
		}
		page, err = s.PublicNetworkQualityTargets(ctx, []string{node}, page.NextCursor)
		if err != nil {
			t.Fatal(err)
		}
	}
	if !present {
		t.Fatal("removed assignment hid retained target history")
	}
	if _, err = s.UpdateNode(ctx, node, UpdateNodeParams{Name: "Synthetic hidden aliases", Hidden: true, LatencyMode: "ping", CollectionSeconds: 15, ReportSeconds: 15}); err != nil {
		t.Fatal(err)
	}
	if page, err = s.PublicNetworkQualityTargets(ctx, []string{other.ID, node}, ""); !errors.Is(err, ErrNotFound) || len(page.Targets) != 0 {
		t.Fatal("mixed hidden aliases returned partial results", err)
	}
	share, err := s.CreateChartShare(ctx, "Synthetic alias scope", "synthetic hash", []string{node})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.SharedNetworkQualityTargets(ctx, share.ID, []string{node}, ""); err != nil {
		t.Fatal("selected private aliases rejected", err)
	}
	if _, err = s.SharedNetworkQualityTargets(ctx, share.ID, []string{other.ID}, ""); !errors.Is(err, ErrNotFound) {
		t.Fatal("shared aliases crossed membership", err)
	}
}

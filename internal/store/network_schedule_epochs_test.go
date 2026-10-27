package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

func TestNetworkScheduleEpochConfigurationLifecycle(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	n, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "Synthetic epoch node"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.CreateTarget(ctx, CreateTargetParams{Name: "Synthetic epoch target", Kind: protocol.TaskKindPing, Host: "example.test", IntervalSeconds: 60, TimeoutMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AssignTarget(ctx, n.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	active := func() (id string, start int64, interval int) {
		t.Helper()
		if err := s.db.QueryRow(`SELECT id,starts_at,interval_seconds FROM network_schedule_epochs WHERE node_id=? AND target_id=? AND ends_at IS NULL`, n.ID, target.ID).Scan(&id, &start, &interval); err != nil {
			t.Fatal(err)
		}
		return
	}
	count := func() int {
		t.Helper()
		var count int
		if err := s.db.QueryRow(`SELECT COUNT(*) FROM network_schedule_epochs WHERE node_id=? AND target_id=?`, n.ID, target.ID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		return count
	}
	first, start, interval := active()
	if interval != 60 || start < time.Now().Unix()-1 {
		t.Fatal("invalid initial epoch")
	}
	if err := s.AssignTarget(ctx, n.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	params := UpdateTargetParams{Name: "Renamed alias", Kind: target.Kind, Host: target.Host, IntervalSeconds: 60, TimeoutMS: 1000, Enabled: true, SortOrder: 9}
	if _, err := s.UpdateTarget(ctx, target.ID, params); err != nil {
		t.Fatal(err)
	}
	if current, _, _ := active(); current != first || count() != 1 {
		t.Fatal("presentation/duplicate assignment reset the plan")
	}
	if _, err := s.UpdateNode(ctx, n.ID, UpdateNodeParams{Name: n.Name, Hidden: true, LatencyMode: "tcping", CollectionSeconds: 15, ReportSeconds: 15}); err != nil {
		t.Fatal(err)
	}
	if current, _, _ := active(); current != first {
		t.Fatal("node presentation changed execution phase")
	}
	params.IntervalSeconds = 30
	if _, err := s.UpdateTarget(ctx, target.ID, params); err != nil {
		t.Fatal(err)
	}
	second, next, nextInterval := active()
	var end int64
	if err := s.db.QueryRow(`SELECT ends_at FROM network_schedule_epochs WHERE id=?`, first).Scan(&end); err != nil {
		t.Fatal(err)
	}
	if second == first || next != end || nextInterval != 30 || count() != 2 {
		t.Fatal("definition transition overlaps or loses history")
	}
	params.Enabled = false
	if _, err := s.UpdateTarget(ctx, target.ID, params); err != nil {
		t.Fatal(err)
	}
	var activeCount int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM network_schedule_epochs WHERE ends_at IS NULL`).Scan(&activeCount); err != nil {
		t.Fatal(err)
	}
	if activeCount != 0 {
		t.Fatal("disabled target kept an active epoch")
	}
	params.Enabled = true
	if _, err := s.UpdateTarget(ctx, target.ID, params); err != nil {
		t.Fatal(err)
	}
	third, _, _ := active()
	if third == second || count() != 3 {
		t.Fatal("reenable reused a closed epoch")
	}
	// Persisted future boundaries simulate a backward wall-clock jump.
	future := time.Now().Unix() + 120
	if _, err := s.db.Exec(`UPDATE network_schedule_epochs SET starts_at=? WHERE id=?`, future, third); err != nil {
		t.Fatal(err)
	}
	params.TimeoutMS = 2000
	if _, err := s.UpdateTarget(ctx, target.ID, params); err != nil {
		t.Fatal(err)
	}
	_, afterJump, _ := active()
	if afterJump < future {
		t.Fatal("clock rollback moved the plan backward")
	}
	port := 443
	params.Kind, params.Host, params.Port = protocol.TaskKindTCPing, "192.0.2.1", &port
	if _, err := s.UpdateTarget(ctx, target.ID, params); err != nil {
		t.Fatal(err)
	}
	var oldKind, oldHost, newKind, newHost string
	var newPort int
	if err := s.db.QueryRow(`SELECT kind,host FROM network_schedule_epochs WHERE id=?`, first).Scan(&oldKind, &oldHost); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT kind,host,port FROM network_schedule_epochs WHERE node_id=? AND target_id=? AND ends_at IS NULL`, n.ID, target.ID).Scan(&newKind, &newHost, &newPort); err != nil {
		t.Fatal(err)
	}
	if oldKind != "ping" || oldHost != "example.test" || newKind != "tcping" || newHost != params.Host || newPort != 443 {
		t.Fatal("execution definition was not frozen per epoch")
	}
	if err := s.UnassignTarget(ctx, n.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM network_schedule_epochs WHERE ends_at IS NULL`).Scan(&activeCount); err != nil {
		t.Fatal(err)
	}
	if activeCount != 0 || count() != 5 {
		t.Fatal("unassign lost closed history or kept an active epoch")
	}
	if err := s.DeleteTarget(ctx, target.ID); err != nil {
		t.Fatal(err)
	}
	if count() != 0 {
		t.Fatal("target deletion did not preserve existing cascade semantics")
	}
}

func TestNetworkScheduleEpochGroupAndRollback(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	n, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "Synthetic group node"})
	if err != nil {
		t.Fatal(err)
	}
	g, err := s.CreateTargetGroup(ctx, "Synthetic group", protocol.TaskKindPing)
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.CreateTarget(ctx, CreateTargetParams{Name: "Synthetic group target", Kind: protocol.TaskKindPing, Host: "example.test", IntervalSeconds: 60, TimeoutMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AssignTargetGroup(ctx, n.ID, g.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AddTargetToGroup(ctx, g.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	var before string
	if err := s.db.QueryRow(`SELECT id FROM network_schedule_epochs WHERE node_id=? AND target_id=? AND ends_at IS NULL`, n.ID, target.ID).Scan(&before); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER synthetic_epoch_failure BEFORE INSERT ON network_schedule_epochs BEGIN SELECT RAISE(ABORT,'synthetic epoch failure'); END`); err != nil {
		t.Fatal(err)
	}
	params := UpdateTargetParams{Name: target.Name, Kind: target.Kind, Host: target.Host, IntervalSeconds: 30, TimeoutMS: 1000, Enabled: true}
	if _, err := s.UpdateTarget(ctx, target.ID, params); err == nil {
		t.Fatal("expected injected write failure")
	}
	var actualInterval int
	var after string
	if err := s.db.QueryRow(`SELECT interval_seconds FROM targets WHERE id=?`, target.ID).Scan(&actualInterval); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT id FROM network_schedule_epochs WHERE node_id=? AND target_id=? AND ends_at IS NULL`, n.ID, target.ID).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if actualInterval != 60 || after != before {
		t.Fatal("failed epoch transition partially applied configuration")
	}
	// Group membership and effective assignments must roll back together.
	n2, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "Synthetic rollback node"})
	if err != nil {
		t.Fatal(err)
	}
	g2, err := s.CreateTargetGroup(ctx, "Synthetic rollback group", protocol.TaskKindPing)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AssignTargetGroup(ctx, n2.ID, g2.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AddTargetToGroup(ctx, g2.ID, target.ID); err == nil {
		t.Fatal("expected member expansion failure")
	}
	var count int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM target_group_members WHERE group_id=?`, g2.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed expansion retained group membership")
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM node_targets WHERE node_id=?`, n2.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed expansion retained effective assignment")
	}
	g3, err := s.CreateTargetGroup(ctx, "Synthetic assignment rollback", protocol.TaskKindPing)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddTargetToGroup(ctx, g3.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.AssignTargetGroup(ctx, n2.ID, g3.ID); err == nil {
		t.Fatal("expected group assignment failure")
	}
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM node_target_groups WHERE node_id=? AND group_id=?`, n2.ID, g3.ID).Scan(&count); err != nil || count != 0 {
		t.Fatal("failed assignment retained node group membership")
	}
}

func TestNetworkScheduleEpochUpgradeRollbackAndSnapshot(t *testing.T) {
	ctx := context.Background()
	s := preNetworkLedgerStore(t)
	n, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "Synthetic legacy node"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.CreateTarget(ctx, CreateTargetParams{Name: "Synthetic legacy target", Kind: protocol.TaskKindPing, Host: "example.test", IntervalSeconds: 60, TimeoutMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AssignTarget(ctx, n.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`UPDATE targets SET created_at='2020-01-01T00:00:00Z' WHERE id=?`, target.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.SaveLatencyResult(ctx, n.ID, protocol.TaskKindPing, protocol.LatencyResult{TaskID: "legacy-result", TargetID: target.ID, Success: true, LatencyMS: 10, CompletedAt: time.Now().UTC()}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec(`CREATE TRIGGER synthetic_042_failure BEFORE INSERT ON schema_migrations WHEN NEW.version='042_network_schedule_epochs.sql' BEGIN SELECT RAISE(ABORT,'synthetic migration failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.Migrate(ctx); err == nil {
		t.Fatal("expected migration failure")
	}
	assertCount(t, s, `SELECT COUNT(*) FROM sqlite_master WHERE name IN ('network_schedule_metadata','network_schedule_epochs','network_assignment_added','network_assignment_removed','network_target_definition_changed')`, 0)
	assertCount(t, s, `SELECT COUNT(*) FROM latency_samples`, 1)
	if _, err := s.db.Exec(`DROP TRIGGER synthetic_042_failure`); err != nil {
		t.Fatal(err)
	}
	before := time.Now().Unix()
	if err := s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var id string
	var start, known int64
	if err := s.db.QueryRow(`SELECT id,starts_at FROM network_schedule_epochs WHERE node_id=? AND target_id=?`, n.ID, target.ID).Scan(&id, &start); err != nil {
		t.Fatal(err)
	}
	if err := s.db.QueryRow(`SELECT known_since FROM network_schedule_metadata WHERE singleton=1`).Scan(&known); err != nil {
		t.Fatal(err)
	}
	if start != known || start < before || start > time.Now().Unix()+1 {
		t.Fatal("upgrade backfilled an invented historical plan")
	}
	path := filepath.Join(httpFixtureDir(t), "network-restored.db")
	if _, err := s.db.Exec(`VACUUM INTO ?`, path); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	var restoredID string
	var restoredStart, restoredKnown int64
	if err := restored.db.QueryRow(`SELECT id,starts_at FROM network_schedule_epochs WHERE node_id=? AND target_id=?`, n.ID, target.ID).Scan(&restoredID, &restoredStart); err != nil {
		t.Fatal(err)
	}
	if err := restored.db.QueryRow(`SELECT known_since FROM network_schedule_metadata WHERE singleton=1`).Scan(&restoredKnown); err != nil {
		t.Fatal(err)
	}
	if restoredID != id || restoredStart != start || restoredKnown != known {
		t.Fatal("restore changed plan phase or known-history boundary")
	}
	if err := restored.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	assertCount(t, restored, `SELECT COUNT(*) FROM network_schedule_epochs`, 1)
	assertCount(t, restored, `SELECT COUNT(*) FROM latency_samples`, 1)
}

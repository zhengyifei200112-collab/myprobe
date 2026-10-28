package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"io/fs"
	"path/filepath"
	"testing"
	"time"

	check "github.com/zhengyifei200112-collab/myprobe/internal/protocol/networkcheck"
	v1 "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
)

// Upgrade fixtures seed the frozen old schema, rather than invoking current
// production methods which correctly require the current migrated columns.
func createLegacyNetworkTarget(ctx context.Context, s *Store, p CreateTargetParams) (Target, error) {
	now := time.Now().UTC()
	target := Target{ID: randomID(), Name: p.Name, Kind: p.Kind, Host: p.Host, Port: p.Port, IntervalSeconds: p.IntervalSeconds, TimeoutMS: p.TimeoutMS, Enabled: true, CreatedAt: now, UpdatedAt: now}
	_, err := s.db.ExecContext(ctx, `INSERT INTO targets(id,name,kind,host,port,interval_seconds,timeout_ms,enabled,created_at,updated_at) VALUES(?,?,?,?,?,?,?,1,?,?)`, target.ID, target.Name, target.Kind, target.Host, target.Port, target.IntervalSeconds, target.TimeoutMS, formatTime(now), formatTime(now))
	return target, err
}

func TestNetworkDefinitionFreezesEpochAndClaimedTask(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "Synthetic family observer"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := s.CreateTarget(ctx, CreateTargetParams{Name: "Synthetic family target", Kind: v1.TaskKindPing, Host: "example.test", IntervalSeconds: 60, TimeoutMS: 3000, AddressFamily: check.FamilyIPv6, PacketCount: 3})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AssignTarget(ctx, node.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	active := func() (id string, start int64, family string, packets int) {
		t.Helper()
		if err := s.db.QueryRow(`SELECT id,starts_at,address_family,packet_count FROM network_schedule_epochs WHERE node_id=? AND target_id=? AND ends_at IS NULL`, node.ID, target.ID).Scan(&id, &start, &family, &packets); err != nil {
			t.Fatal(err)
		}
		return
	}
	first, start, family, packets := active()
	if family != check.FamilyIPv6 || packets != 3 {
		t.Fatal("definition not frozen")
	}
	claimed, err := s.ClaimNetworkTask(ctx, first, time.Unix(start, 0), time.Unix(start, 0).Add(time.Millisecond))
	if err != nil {
		t.Fatal(err)
	}
	if claimed.ProbeTask().Validate(time.Now()) != nil || claimed.AddressFamily != check.FamilyIPv6 || claimed.PacketCount != 3 {
		t.Fatal("task dropped requested proof settings")
	}
	params := UpdateTargetParams{Name: "Renamed only", Kind: target.Kind, Host: target.Host, IntervalSeconds: 60, TimeoutMS: 3000, Enabled: true}
	renamed, err := s.UpdateTarget(ctx, target.ID, params)
	if err != nil {
		t.Fatal(err)
	}
	if id, _, _, _ := active(); id != first || renamed.AddressFamily != check.FamilyIPv6 || renamed.PacketCount != 3 {
		t.Fatal("legacy rename reset a family/count or plan")
	}
	params.AddressFamily = check.FamilyIPv4
	two := 2
	params.PacketCount = &two
	if _, err = s.UpdateTarget(ctx, target.ID, params); err != nil {
		t.Fatal(err)
	}
	second, next, nextFamily, nextPackets := active()
	var end int64
	if err = s.db.QueryRow(`SELECT ends_at FROM network_schedule_epochs WHERE id=?`, first).Scan(&end); err != nil {
		t.Fatal(err)
	}
	if second == first || next != end || nextFamily != check.FamilyIPv4 || nextPackets != 2 {
		t.Fatal("definition transition lost its interval or settings")
	}
	var savedFamily string
	var savedPackets int
	if err = s.db.QueryRow(`SELECT address_family,packet_count FROM network_probe_tasks WHERE id=?`, claimed.Task.ID).Scan(&savedFamily, &savedPackets); err != nil {
		t.Fatal(err)
	}
	if savedFamily != check.FamilyIPv6 || savedPackets != 3 {
		t.Fatal("historical task changed with current target")
	}
	if _, err = s.db.Exec(`CREATE TRIGGER synthetic_family_transition_failure BEFORE INSERT ON network_schedule_epochs BEGIN SELECT RAISE(ABORT,'synthetic family failure'); END`); err != nil {
		t.Fatal(err)
	}
	params.AddressFamily = check.FamilyIPv6
	if _, err = s.UpdateTarget(ctx, target.ID, params); err == nil {
		t.Fatal("expected atomic transition failure")
	}
	if id, _, f, p := active(); id != second || f != check.FamilyIPv4 || p != 2 {
		t.Fatal("failed change partially updated plan")
	}
	if err = s.db.QueryRow(`SELECT address_family,packet_count FROM targets WHERE id=?`, target.ID).Scan(&savedFamily, &savedPackets); err != nil || savedFamily != check.FamilyIPv4 || savedPackets != 2 {
		t.Fatal("failed change partially updated target")
	}
}

func TestNetworkDefinitionConfigVersionAndRoundTrip(t *testing.T) {
	ctx := context.Background()
	s, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	_, err = s.CreateTarget(ctx, CreateTargetParams{Name: "Synthetic portable profile", Kind: v1.TaskKindPing, Host: "example.test", IntervalSeconds: 60, TimeoutMS: 3000, AddressFamily: check.FamilyIPv6, PacketCount: 3})
	if err != nil {
		t.Fatal(err)
	}
	snapshot, err := s.ExportConfig(ctx, time.Now())
	if err != nil || snapshot.Version != 3 || snapshot.Targets[0].AddressFamily != check.FamilyIPv6 || snapshot.Targets[0].PacketCount != 3 {
		t.Fatal("new export lost requested settings", err)
	}
	other, err := Open(ctx, ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if _, err = other.ImportConfig(ctx, snapshot, true); err != nil {
		t.Fatal(err)
	}
	assertCount(t, other, `SELECT COUNT(*) FROM targets`, 0)
	if _, err = other.ImportConfig(ctx, snapshot, false); err != nil {
		t.Fatal(err)
	}
	loaded, err := other.ListTargets(ctx)
	if err != nil || loaded[0].AddressFamily != check.FamilyIPv6 || loaded[0].PacketCount != 3 {
		t.Fatal("import lost requested settings", err)
	}
	snapshot.Version = 2
	if _, err = other.ImportConfig(ctx, snapshot, false); err == nil {
		t.Fatal("nonlegacy options accepted under old snapshot version")
	}
	snapshot.Targets[0].AddressFamily = ""
	snapshot.Targets[0].PacketCount = 0
	if _, err = other.ImportConfig(ctx, snapshot, false); err != nil {
		t.Fatal("old default config rejected", err)
	}
	loaded, err = other.ListTargets(ctx)
	if err != nil || loaded[0].AddressFamily != check.FamilyAuto || loaded[0].PacketCount != 1 {
		t.Fatal("old config did not retain old auto/single-packet meaning", err)
	}
}

func TestNetworkDefinitionMigrationRollbackKeepsLegacyProofUnknown(t *testing.T) {
	ctx := context.Background()
	// Seed the actual current-main predecessor, including039/040/047. The
	// historical retention-index fixture is not part of this integration.
	file := filepath.Join(httpFixtureDir(t), "pre-network-evidence.db")
	db, err := sql.Open("sqlite", sqliteDSN(file))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		version := entry.Name()
		if entry.IsDir() || version == "045_network_probe_evidence.sql" {
			continue
		}
		body, err := migrations.ReadFile("migrations/" + version)
		if err != nil {
			t.Fatal(err)
		}
		if _, err = db.ExecContext(ctx, string(body)); err != nil {
			t.Fatal(err)
		}
		if _, err = db.ExecContext(ctx, `INSERT INTO schema_migrations(version,applied_at) VALUES(?,?)`, version, nowText()); err != nil {
			t.Fatal(err)
		}
	}
	s := &Store{db: db, path: file}
	assertCount(t, s, `SELECT COUNT(*) FROM schema_migrations WHERE version IN ('039_scheduled_backups.sql','040_agent_runtime_evidence.sql','041_history_retention_indexes.sql','042_network_schedule_epochs.sql','043_network_probe_tasks.sql','044_network_quality_rollups.sql','047_node_timeline_indexes.sql')`, 7)
	node, _, err := s.CreateNode(ctx, CreateNodeParams{Name: "Synthetic pre-family observer"})
	if err != nil {
		t.Fatal(err)
	}
	target, err := createLegacyNetworkTarget(ctx, s, CreateTargetParams{Name: "Synthetic pre-family target", Kind: v1.TaskKindPing, Host: "example.test", IntervalSeconds: 60, TimeoutMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if err = s.AssignTarget(ctx, node.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	var oldEpoch string
	if err = s.db.QueryRow(`SELECT id FROM network_schedule_epochs WHERE target_id=?`, target.ID).Scan(&oldEpoch); err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	raw, _ := json.Marshal(v1.LatencyResult{TaskID: "synthetic-legacy-proof", TargetID: target.ID, Success: true, LatencyMS: 10, CompletedAt: now})
	digest := sha256.Sum256(raw)
	hash := hex.EncodeToString(digest[:])
	if _, err = s.db.Exec(`INSERT INTO network_probe_tasks(id,source,node_id,target_id,kind,host,timeout_ms,dispatch_until_ns,expires_at_ns,created_at_ns,state,lease_token,lease_until_ns,received_at_ns,result_sha,result_json,outcome) VALUES(?,'manual',?,?,'ping','example.test',1000,?,?,?,'completed','synthetic',?,?,?,?,'success')`, "synthetic-legacy-proof", node.ID, target.ID, now.Add(time.Minute).UnixNano(), now.Add(time.Minute).UnixNano(), now.UnixNano(), now.UnixNano(), now.UnixNano(), hash, string(raw)); err != nil {
		t.Fatal(err)
	}
	if _, err = s.db.Exec(`CREATE TRIGGER synthetic_045_failure BEFORE INSERT ON schema_migrations WHEN NEW.version='045_network_probe_evidence.sql' BEGIN SELECT RAISE(ABORT,'synthetic migration failure'); END`); err != nil {
		t.Fatal(err)
	}
	if s.Migrate(ctx) == nil {
		t.Fatal("expected upgrade rollback")
	}
	assertCount(t, s, `SELECT COUNT(*) FROM pragma_table_info('targets') WHERE name='address_family'`, 0)
	assertCount(t, s, `SELECT COUNT(*) FROM pragma_table_info('network_schedule_epochs') WHERE name='address_family'`, 0)
	assertCount(t, s, `SELECT COUNT(*) FROM pragma_table_info('network_probe_tasks') WHERE name='result_format'`, 0)
	assertCount(t, s, `SELECT COUNT(*) FROM schema_migrations WHERE version='045_network_probe_evidence.sql'`, 0)
	if _, err = s.db.Exec(`DROP TRIGGER synthetic_045_failure`); err != nil {
		t.Fatal(err)
	}
	if err = s.Migrate(ctx); err != nil {
		t.Fatal(err)
	}
	var format, family, savedRaw, savedHash, epoch string
	var packets int
	if err = s.db.QueryRow(`SELECT result_format,address_family,packet_count,result_json,result_sha FROM network_probe_tasks WHERE id='synthetic-legacy-proof'`).Scan(&format, &family, &packets, &savedRaw, &savedHash); err != nil {
		t.Fatal(err)
	}
	if format != "v1" || family != check.FamilyAuto || packets != 1 || savedRaw != string(raw) || savedHash != hash {
		t.Fatal("upgrade invented observed evidence or rewrote admitted legacy result")
	}
	if err = s.db.QueryRow(`SELECT id FROM network_schedule_epochs WHERE target_id=? AND ends_at IS NULL`, target.ID).Scan(&epoch); err != nil || epoch != oldEpoch {
		t.Fatal("upgrade reset legacy plan phase", err)
	}
}

package agentclient

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

// SQL-generated retained history includes its derived read state. Execute the
// same immutable migration backfill inside the seed transaction, with setup
// duration recorded before the measured workload begins.
func seedJointLatencyHours(ctx context.Context, tx *sql.Tx) (int64, error) {
	migration, err := os.ReadFile("../store/migrations/052_latency_history_hours.sql")
	if err != nil {
		return 0, err
	}
	prefix := []byte("INSERT INTO latency_history_hours\n")
	if bytes.Count(migration, prefix) != 1 {
		return 0, errors.New("hour migration backfill marker differs")
	}
	_, body, _ := bytes.Cut(migration, prefix)
	statement, _, found := bytes.Cut(body, []byte(";"))
	if !found {
		return 0, errors.New("hour migration backfill is incomplete")
	}
	r, err := tx.ExecContext(ctx, string(prefix)+string(statement)+";")
	if err != nil {
		return 0, err
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM latency_history_hours_dirty"); err != nil {
		return 0, err
	}
	return r.RowsAffected()
}

func TestJointLatencyHoursSeedUsesProductionBackfillAndRollback(t *testing.T) {
	ctx := context.Background()
	file := filepath.Join(t.TempDir(), "synthetic.db")
	database, err := store.Open(ctx, file)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	node, _, err := database.CreateNode(ctx, store.CreateNodeParams{Name: "synthetic hour fixture"})
	if err != nil {
		t.Fatal(err)
	}
	port := 443
	target, err := database.CreateTarget(ctx, store.CreateTargetParams{Name: "owned target", Kind: "tcping",
		Host: "example.test", Port: &port, IntervalSeconds: 60, TimeoutMS: 1000})
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", file)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.ExecContext(ctx, "INSERT INTO latency_rollups VALUES(?,?,'tcping',60,'2026-01-01T00:00:00Z',10,7,5,50)",
		node.ID, target.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(ctx, "CREATE TRIGGER reject_seed_hour_delete BEFORE DELETE ON latency_history_hours_dirty BEGIN SELECT RAISE(ABORT,'synthetic seed failure'); END"); err != nil {
		t.Fatal(err)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := seedJointLatencyHours(ctx, tx); err == nil {
		tx.Rollback()
		t.Fatal("seed failure was ignored")
	}
	if err := tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM latency_history_hours").Scan(&count); err != nil || count != 0 {
		t.Fatal("failed seed published derived rows", count, err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM latency_history_hours_dirty").Scan(&count); err != nil || count != 1 {
		t.Fatal("failed seed lost its source marker", count, err)
	}
	if _, err := db.Exec("DROP TRIGGER reject_seed_hour_delete"); err != nil {
		t.Fatal(err)
	}
	tx, err = db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	rows, err := seedJointLatencyHours(ctx, tx)
	if err != nil || rows != 1 {
		t.Fatal("production backfill seed failed", rows, err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := db.QueryRow("SELECT COUNT(*) FROM latency_history_hours_dirty").Scan(&count); err != nil || count != 0 {
		t.Fatal("completed seed retained pending source", count, err)
	}
	start := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	points, err := database.LatencyHistoryRange(ctx, node.ID, start, start.Add(2*time.Hour), 3600)
	if err != nil || len(points) != 1 || points[0].SuccessRate != 70 || points[0].LatencyMS == nil || *points[0].LatencyMS != 10 {
		t.Fatal("generated derived state changed the weighted fixture", points, err)
	}
}

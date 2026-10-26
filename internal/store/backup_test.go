package store

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestDatabaseBackupStageAndApply(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "myprobe.db")
	database, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.CreateNode(ctx, CreateNodeParams{ID: "before", Name: "Before"}); err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(directory, "snapshot.db")
	if err := database.ConsistentBackup(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	if _, _, err := database.CreateNode(ctx, CreateNodeParams{ID: "after", Name: "After"}); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := StageDatabaseRestore(ctx, databasePath, snapshot); err != nil {
		t.Fatal(err)
	}
	recovery, err := ApplyPendingRestore(ctx, databasePath, time.Date(2026, 7, 22, 1, 2, 3, 0, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if recovery == "" {
		t.Fatal("recovery path was not returned")
	}
	if _, err := os.Stat(recovery); err != nil {
		t.Fatalf("recovery database: %v", err)
	}
	restored, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	nodes, err := restored.ListNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) != 1 || nodes[0].ID != "before" {
		t.Fatalf("restored nodes = %#v", nodes)
	}
	recoveryDB, err := Open(ctx, recovery)
	if err != nil {
		t.Fatal(err)
	}
	defer recoveryDB.Close()
	recoveryNodes, err := recoveryDB.ListNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(recoveryNodes) != 2 {
		t.Fatalf("recovery nodes = %#v", recoveryNodes)
	}
}

func TestBackupRestoresIncidentAndRecoversExpiredDeliveryLease(t *testing.T) {
	s, rule, node := incidentFixture(t, `{"threshold_percent":90,"recovery_seconds":20}`)
	ctx := context.Background()
	now := time.Now().UTC().Truncate(time.Second)
	incident := observe(t, s, rule, node, now, true, true)
	job, err := s.ClaimDelivery(ctx, now, time.Minute)
	if err != nil || job == nil {
		t.Fatalf("claim: %+v %v", job, err)
	}
	snapshot := filepath.Join(t.TempDir(), "incident-snapshot.db")
	if err := s.ConsistentBackup(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	if err := s.CompleteDelivery(ctx, job.ID, job.LeaseToken, now.Add(time.Second), DeliveryOutcome{Delivered: true}); err != nil {
		t.Fatal(err)
	}
	path := s.path
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := StageDatabaseRestore(ctx, path, snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyPendingRestore(ctx, path, now.Add(2*time.Minute)); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	current, err := restored.Incident(ctx, incident.ID)
	if err != nil || current.State != "firing" || !sameSnapshot(current.RuleSnapshot, snapshotRule(rule)) {
		t.Fatalf("restored incident: %+v %v", current, err)
	}
	reclaimed, err := restored.ClaimDelivery(ctx, now.Add(2*time.Minute), time.Minute)
	if err != nil || reclaimed == nil || reclaimed.ID != job.ID || reclaimed.AttemptCount != 2 || reclaimed.LeaseToken == job.LeaseToken {
		t.Fatalf("restored lease: %+v %v", reclaimed, err)
	}
	attempts, err := restored.ListDeliveryAttempts(ctx, job.ID)
	if err != nil || len(attempts) != 2 || attempts[0].Outcome != "unknown" || attempts[1].Outcome != "started" {
		t.Fatalf("restored attempts: %+v %v", attempts, err)
	}
}

func TestStageDatabaseRestoreRejectsExistingPendingFile(t *testing.T) {
	ctx := context.Background()
	directory := t.TempDir()
	databasePath := filepath.Join(directory, "myprobe.db")
	database, err := Open(ctx, databasePath)
	if err != nil {
		t.Fatal(err)
	}
	snapshot1 := filepath.Join(directory, "one.db")
	snapshot2 := filepath.Join(directory, "two.db")
	if err := database.ConsistentBackup(ctx, snapshot1); err != nil {
		t.Fatal(err)
	}
	if err := database.ConsistentBackup(ctx, snapshot2); err != nil {
		t.Fatal(err)
	}
	database.Close()
	if _, err := StageDatabaseRestore(ctx, databasePath, snapshot1); err != nil {
		t.Fatal(err)
	}
	if _, err := StageDatabaseRestore(ctx, databasePath, snapshot2); !errors.Is(err, ErrRestorePending) {
		t.Fatalf("error = %v", err)
	}
}

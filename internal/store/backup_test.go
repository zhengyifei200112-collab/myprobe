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

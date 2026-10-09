package store

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/alertpolicy"
)

func TestBackupRestoresPolicyBindingsWithoutDuplicatingFaults(t *testing.T) {
	for _, origin := range []string{"legacy", "materialized"} {
		t.Run(origin, func(t *testing.T) {
			s, rule, node := incidentFixture(t, `{"threshold_percent":90,"recovery_seconds":0}`)
			ctx := context.Background()
			now := time.Now().UTC().Truncate(time.Second)
			if origin == "materialized" {
				if err := s.DeleteAlertRule(ctx, rule.ID); err != nil {
					t.Fatal(err)
				}
				_, err := s.SaveAlertPolicy(ctx, AlertPolicy{Policy: alertpolicy.Policy{Key: "cpu", Enabled: true, Scope: alertpolicy.Scope{Kind: "all"}}, Name: "backup fixture", ChannelID: rule.ChannelID, Kind: "cpu", Config: json.RawMessage(`{"threshold_percent":90,"recovery_seconds":0}`), CooldownSeconds: 900})
				if err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.PrepareAlertPolicyRules(ctx, now); err != nil {
				t.Fatal(err)
			}
			rules, err := s.ListAlertRules(ctx)
			if err != nil || len(rules) != 1 {
				t.Fatalf("rules: %+v %v", rules, err)
			}
			rule = rules[0]
			policies, err := s.ListAlertPolicies(ctx)
			if err != nil || len(policies) != 1 {
				t.Fatalf("policies: %+v %v", policies, err)
			}
			before := policies[0]
			incident := observe(t, s, rule, node, now, true, true)
			jobs, err := s.ListIncidentDeliveries(ctx, incident.ID, 0, 10)
			if err != nil || len(jobs) != 1 {
				t.Fatalf("jobs: %+v %v", jobs, err)
			}
			originalJob := jobs[0]
			snapshot := filepath.Join(t.TempDir(), "policies.db")
			if err = s.ConsistentBackup(ctx, snapshot); err != nil {
				t.Fatal(err)
			}
			changed := before
			changed.Enabled = false
			if _, err = s.SaveAlertPolicy(ctx, changed); err != nil {
				t.Fatal(err)
			}
			if _, err = s.PrepareAlertPolicyRules(ctx, now.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			if err = s.ReconcileIncidents(ctx, now.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			path := s.path
			if err = s.Close(); err != nil {
				t.Fatal(err)
			}
			if _, err = StageDatabaseRestore(ctx, path, snapshot); err != nil {
				t.Fatal(err)
			}
			if _, err = ApplyPendingRestore(ctx, path, now.Add(2*time.Second)); err != nil {
				t.Fatal(err)
			}
			restored, err := Open(ctx, path)
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			if mapped, err := restored.PrepareAlertPolicyRules(ctx, now.Add(3*time.Second)); err != nil || mapped != 0 {
				t.Fatalf("restore remapped rules: %d %v", mapped, err)
			}
			after, err := restored.ListAlertPolicies(ctx)
			if err != nil || len(after) != 1 || after[0].ID != before.ID || after[0].Revision != before.Revision || !after[0].Enabled || string(after[0].Config) != string(before.Config) {
				t.Fatalf("policy restore: %+v %v", after, err)
			}
			var restoredOrigin string
			if err = restored.db.QueryRowContext(ctx, `SELECT origin FROM alert_policy_rule_bindings WHERE policy_id=? AND node_id=? AND rule_id=?`, before.ID, node.ID, rule.ID).Scan(&restoredOrigin); err != nil || restoredOrigin != origin {
				t.Fatalf("binding restore: %s %v", restoredOrigin, err)
			}
			current, err := restored.AlertRule(ctx, rule.ID)
			if err != nil || current.PolicyID != before.ID || !current.Enabled || !current.UpdatedAt.Equal(rule.UpdatedAt) {
				t.Fatalf("execution rule restore: %+v %v", current, err)
			}
			if err = restored.ReconcileIncidents(ctx, now.Add(3*time.Second)); err != nil {
				t.Fatal(err)
			}
			observed := observe(t, restored, current, node, now.Add(4*time.Second), true, true)
			if observed.ID != incident.ID || observed.State != "firing" {
				t.Fatalf("fault duplicated after restore: %+v", observed)
			}
			jobs, err = restored.ListIncidentDeliveries(ctx, incident.ID, 0, 10)
			if err != nil || len(jobs) != 1 || jobs[0].ID != originalJob.ID || jobs[0].Status != "pending" || string(jobs[0].Payload) != string(originalJob.Payload) {
				t.Fatalf("outbox changed: %+v %v", jobs, err)
			}
			claimed, err := restored.ClaimDelivery(ctx, now.Add(5*time.Second), time.Minute)
			if err != nil || claimed == nil || claimed.ID != originalJob.ID {
				t.Fatalf("restored policy rejected delivery: %+v %v", claimed, err)
			}
		})
	}
}

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

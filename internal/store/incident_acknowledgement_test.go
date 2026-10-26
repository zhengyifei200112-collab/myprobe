package store

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAcknowledgementRetainsFirstActorAndDoesNotChangeDeliveryAuthority(t *testing.T) {
	s, rule, node := incidentFixture(t, `{"threshold_percent":90}`)
	ctx := context.Background()
	actor, err := s.CreateUser(ctx, "fixture operator", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Second)
	i := observe(t, s, rule, node, at, true, true)
	before, err := s.Incident(ctx, i.ID)
	if err != nil {
		t.Fatal(err)
	}
	jobs, err := s.ListIncidentDeliveries(ctx, i.ID, 0, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs: %+v %v", jobs, err)
	}
	view, err := s.IncidentAcknowledgement(ctx, i.ID)
	if err != nil || view.Acknowledgement != nil || view.IncidentState != "firing" {
		t.Fatalf("unacknowledged: %+v %v", view, err)
	}
	started := time.Now().UTC().Truncate(time.Millisecond)
	result, err := s.AcknowledgeIncident(ctx, i.ID, "first", "  排查中\r\nsecond line\tvalue  ", actor.ID)
	if err != nil || result.Replayed || result.Acknowledgement.ActorID != actor.ID || result.Acknowledgement.ActorName != actor.Username || result.Acknowledgement.Note != "排查中\nsecond line\tvalue" || result.Acknowledgement.AcknowledgedAt.Before(started) || result.Acknowledgement.AcknowledgedAt.After(time.Now().UTC()) {
		t.Fatalf("first: %+v %v", result, err)
	}
	after, err := s.Incident(ctx, i.ID)
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("ack changed incident: before=%+v after=%+v err=%v", before, after, err)
	}
	afterJobs, err := s.ListIncidentDeliveries(ctx, i.ID, 0, 10)
	if err != nil || !reflect.DeepEqual(jobs, afterJobs) {
		t.Fatalf("ack changed jobs: %+v %v", afterJobs, err)
	}
	listed, err := s.ListIncidents(ctx, IncidentFilter{Limit: 10})
	if err != nil || len(listed) != 1 || listed[0].Acknowledgement == nil || listed[0].Acknowledgement.ActorName != actor.Username || !listed[0].Acknowledgement.AcknowledgedAt.Equal(result.Acknowledgement.AcknowledgedAt) {
		t.Fatalf("list summary: %+v %v", listed, err)
	}
	if _, err := s.db.ExecContext(ctx, `UPDATE users SET username='renamed fixture' WHERE id=?`, actor.ID); err != nil {
		t.Fatal(err)
	}
	observe(t, s, rule, node, at.Add(time.Second), true, false)
	replay, err := s.AcknowledgeIncident(ctx, i.ID, "first", "排查中\nsecond line\tvalue", actor.ID)
	if err != nil || !replay.Replayed || !reflect.DeepEqual(result.Acknowledgement, replay.Acknowledgement) {
		t.Fatalf("resolved replay lost history: %+v %v", replay, err)
	}
	if _, err := s.AcknowledgeIncident(ctx, i.ID, "first", "changed note", actor.ID); !errors.Is(err, ErrAcknowledgementRequestConflict) {
		t.Fatalf("changed identity: %v", err)
	}
	other, err := s.CreateUser(ctx, "other fixture", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcknowledgeIncident(ctx, i.ID, "first", result.Acknowledgement.Note, other.ID); !errors.Is(err, ErrAcknowledgementRequestConflict) {
		t.Fatalf("changed actor identity: %v", err)
	}
	if _, err := s.AcknowledgeIncident(ctx, i.ID, "new-key", "changed note", actor.ID); !errors.Is(err, ErrIncidentAlreadyAcknowledged) {
		t.Fatalf("overwrote first acknowledgement: %v", err)
	}
	if count := rowCount(t, s, "incident_acknowledgements"); count != 1 {
		t.Fatalf("duplicate acknowledgements: %d", count)
	}
	if count := rowCount(t, s, "audit_log"); count != 1 {
		t.Fatalf("duplicate audits: %d", count)
	}
	var details string
	if err := s.db.QueryRowContext(ctx, `SELECT details_json FROM audit_log WHERE action='acknowledge'`).Scan(&details); err != nil || details != `{"has_note":true}` {
		t.Fatalf("audit leaked note: %s %v", details, err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM users WHERE id=?`, actor.ID); err != nil {
		t.Fatal(err)
	}
	view, err = s.IncidentAcknowledgement(ctx, i.ID)
	if err != nil || view.Acknowledgement == nil || !reflect.DeepEqual(*view.Acknowledgement, result.Acknowledgement) {
		t.Fatalf("account removal erased history: %+v %v", view, err)
	}
	audits, err := s.ListAudit(ctx, 10, 0)
	if err != nil || len(audits) != 1 || audits[0].UserID != nil {
		t.Fatalf("removed-account audit: %+v %v", audits, err)
	}
}

func TestAcknowledgementValidationAndAtomicAudit(t *testing.T) {
	s, rule, node := incidentFixture(t, `{"threshold_percent":90,"duration_seconds":10}`)
	ctx := context.Background()
	actor, err := s.CreateUser(ctx, "fixture", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Second)
	i := observe(t, s, rule, node, at, true, true)
	if _, err := s.AcknowledgeIncident(ctx, i.ID, "pending", "", actor.ID); !errors.Is(err, ErrIncidentNotFiring) {
		t.Fatalf("pending admission: %v", err)
	}
	observe(t, s, rule, node, at.Add(10*time.Second), true, true)
	for _, tc := range []struct{ id, key, note, actor string }{
		{i.ID, "", "", actor.ID},
		{i.ID, strings.Repeat("x", 129), "", actor.ID},
		{i.ID, "bad key", "", actor.ID},
		{"bad/id", "valid", "", actor.ID},
		{i.ID, "valid", strings.Repeat("界", 334), actor.ID},
		{i.ID, "valid", string([]byte{0xff}), actor.ID},
		{i.ID, "valid", "nul\x00value", actor.ID},
		{i.ID, "valid", "bare\rreturn", actor.ID},
		{i.ID, "valid", "", "missing-actor"},
	} {
		if _, err := s.AcknowledgeIncident(ctx, tc.id, tc.key, tc.note, tc.actor); !errors.Is(err, ErrInvalidAcknowledgement) {
			t.Fatalf("validation (%q): %v", tc.key, err)
		}
	}
	if _, err := s.AcknowledgeIncident(ctx, "missing", "valid", "", actor.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fail_ack_audit BEFORE INSERT ON audit_log WHEN NEW.action='acknowledge' BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcknowledgeIncident(ctx, i.ID, "rollback", "note", actor.ID); err == nil {
		t.Fatal("audit failure ignored")
	}
	if rowCount(t, s, "incident_acknowledgements") != 0 || rowCount(t, s, "audit_log") != 0 {
		t.Fatal("partial acknowledgement persisted")
	}
	if _, err := s.db.ExecContext(ctx, `DROP TRIGGER fail_ack_audit`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AcknowledgeIncident(ctx, i.ID, "rollback", strings.Repeat("a", 1000), actor.ID); err != nil {
		t.Fatalf("exact byte boundary: %v", err)
	}
}

func TestConcurrentAcknowledgementsHaveOneFirstWinner(t *testing.T) {
	for _, sameKey := range []bool{true, false} {
		t.Run(map[bool]string{true: "replay", false: "different-actors"}[sameKey], func(t *testing.T) {
			s, rule, node := incidentFixture(t, `{"threshold_percent":90}`)
			ctx := context.Background()
			first, err := s.CreateUser(ctx, "first fixture", "synthetic")
			if err != nil {
				t.Fatal(err)
			}
			second, err := s.CreateUser(ctx, "second fixture", "synthetic")
			if err != nil {
				t.Fatal(err)
			}
			i := observe(t, s, rule, node, time.Now().UTC(), true, true)
			start := make(chan struct{})
			var wg sync.WaitGroup
			results := make(chan AcknowledgeIncidentResult, 16)
			failures := make(chan error, 16)
			for n := range 16 {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					key, actor := "first-key", first.ID
					if !sameKey && n%2 == 1 {
						key, actor = "second-key", second.ID
					}
					result, err := s.AcknowledgeIncident(ctx, i.ID, key, "note", actor)
					if err != nil {
						failures <- err
					} else {
						results <- result
					}
				}()
			}
			close(start)
			wg.Wait()
			close(results)
			close(failures)
			for err := range failures {
				if sameKey || !errors.Is(err, ErrIncidentAlreadyAcknowledged) {
					t.Fatal(err)
				}
			}
			newCount := 0
			winner := ""
			for result := range results {
				if winner == "" {
					winner = result.Acknowledgement.ActorID
				}
				if result.Acknowledgement.ActorID != winner {
					t.Fatal("multiple actor records")
				}
				if !result.Replayed {
					newCount++
				}
			}
			if newCount != 1 || rowCount(t, s, "incident_acknowledgements") != 1 || rowCount(t, s, "audit_log") != 1 {
				t.Fatal("concurrency duplicated the action")
			}
		})
	}
}

func TestAcknowledgementSurvivesRestartAndStagedRestore(t *testing.T) {
	s, rule, node := incidentFixture(t, `{"threshold_percent":90}`)
	ctx := context.Background()
	actor, err := s.CreateUser(ctx, "fixture", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	i := observe(t, s, rule, node, time.Now().UTC(), true, true)
	first, err := s.AcknowledgeIncident(ctx, i.ID, "durable", "backup note", actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	snapshot := filepath.Join(t.TempDir(), "snapshot.db")
	if err := s.ConsistentBackup(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	path := s.DatabasePath()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if replay, err := s.AcknowledgeIncident(ctx, i.ID, "durable", "backup note", actor.ID); err != nil || !replay.Replayed || !reflect.DeepEqual(first.Acknowledgement, replay.Acknowledgement) {
		t.Fatalf("restart: %+v %v", replay, err)
	}
	if _, err := s.db.ExecContext(ctx, `DELETE FROM incident_acknowledgements WHERE incident_id=?`, i.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := StageDatabaseRestore(ctx, path, snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyPendingRestore(ctx, path, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if replay, err := s.AcknowledgeIncident(ctx, i.ID, "durable", "backup note", actor.ID); err != nil || !replay.Replayed || !reflect.DeepEqual(first.Acknowledgement, replay.Acknowledgement) {
		t.Fatalf("restore: %+v %v", replay, err)
	}
	if rowCount(t, s, "audit_log") != 1 {
		t.Fatal("restored replay duplicated audit")
	}
}

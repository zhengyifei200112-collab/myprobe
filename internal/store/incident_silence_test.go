package store

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

func silenceFixture(t *testing.T) (*Store, AlertRule, Node, User, time.Time, *Incident) {
	t.Helper()
	s, rule, node := incidentFixture(t, `{"threshold_percent":90}`)
	actor, err := s.CreateUser(context.Background(), "silence fixture", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC().Truncate(time.Second)
	i := observe(t, s, rule, node, at, true, true)
	return s, rule, node, actor, at, i
}
func readSilence(t *testing.T, s *Store, id string) IncidentSilence {
	t.Helper()
	w, err := scanSilence(s.db.QueryRow(`SELECT `+silenceColumns+` FROM incident_silences WHERE id=?`, id))
	if err != nil {
		t.Fatal(err)
	}
	return w
}

func TestSilenceExpiryRequiresFreshEvidenceAndKeepsOriginalReplay(t *testing.T) {
	s, rule, node, actor, at, i := silenceFixture(t)
	ctx := context.Background()
	p := CreateIncidentSilenceParams{DurationSeconds: 60, Reason: "  排查中\r\nwork  ", Acknowledge: true}
	first, err := s.CreateIncidentSilence(ctx, i.ID, "create", actor.ID, p, at.Add(time.Second))
	if err != nil || first.Replayed || !first.AcknowledgementCreated || !first.EndsAt.Equal(at.Add(61*time.Second)) {
		t.Fatalf("create: %+v %v", first, err)
	}
	jobs, err := s.ListIncidentDeliveries(ctx, i.ID, 0, 10)
	if err != nil || len(jobs) != 1 || jobs[0].Status != "canceled" || jobs[0].ErrorClass != "silence" || rowCount(t, s, "delivery_silence_links") != 1 {
		t.Fatalf("queued suppression: %+v %v", jobs, err)
	}
	current, err := s.Incident(ctx, i.ID)
	if err != nil || current.State != "firing" || current.LastEnqueuedAt != nil || current.ObservedAt == nil || !current.ObservedAt.Equal(at) {
		t.Fatalf("silence faked observation: %+v %v", current, err)
	}
	// A recovery computation captured before the mutation may not commit after it.
	old := observe(t, s, rule, node, at.Add(500*time.Millisecond), true, false)
	if old.State != "firing" || old.ObservedAt == nil || !old.ObservedAt.Equal(at) {
		t.Fatal("older captured observation bypassed the silence action")
	}
	observe(t, s, rule, node, at.Add(2*time.Second), true, true)
	unknown := observe(t, s, rule, node, first.EndsAt, false, false)
	if unknown.State != "firing" || !unknown.ObservationStale {
		t.Fatalf("expiry invented recovery: %+v", unknown)
	}
	if claim, err := s.ClaimDelivery(ctx, first.EndsAt, time.Minute); err != nil || claim != nil {
		t.Fatalf("expiry forced an old message: %+v %v", claim, err)
	}
	observe(t, s, rule, node, at.Add(62*time.Second), true, true)
	summary, err := s.ClaimDelivery(ctx, at.Add(62*time.Second), time.Minute)
	if err != nil || summary == nil || summary.ID == jobs[0].ID {
		t.Fatalf("fresh summary: %+v %v", summary, err)
	}
	var links int
	if err := s.db.QueryRow(`SELECT count(*) FROM delivery_silence_links WHERE delivery_id=?`, summary.ID).Scan(&links); err != nil || links != 0 {
		t.Fatalf("new summary inherited an old cause: %d %v", links, err)
	}
	if err := s.CompleteDelivery(ctx, summary.ID, summary.LeaseToken, at.Add(63*time.Second), DeliveryOutcome{Delivered: true}); err != nil {
		t.Fatal(err)
	}
	observe(t, s, rule, node, at.Add(64*time.Second), true, true)
	if rowCount(t, s, "notification_deliveries") != 2 {
		t.Fatal("fresh summary repeated before cooldown")
	}
	replay, err := s.CreateIncidentSilence(ctx, i.ID, "create", actor.ID, p, at.Add(80*time.Second))
	if err != nil || !replay.Replayed || replay.SilenceID != first.SilenceID || !replay.EndsAt.Equal(first.EndsAt) || !replay.AcknowledgementCreated {
		t.Fatalf("expired replay: %+v %v", replay, err)
	}
	if _, err := s.CreateIncidentSilence(ctx, i.ID, "create", actor.ID, CreateIncidentSilenceParams{DurationSeconds: 120}, at.Add(80*time.Second)); !errors.Is(err, ErrSilenceRequestConflict) {
		t.Fatalf("changed identity: %v", err)
	}
}

func TestSilenceCancelIsRevisionedAndDoesNotQueueWithoutObservation(t *testing.T) {
	s, rule, node, actor, at, i := silenceFixture(t)
	ctx := context.Background()
	first, err := s.CreateIncidentSilence(ctx, i.ID, "create", actor.ID, CreateIncidentSilenceParams{DurationSeconds: 3600}, at.Add(time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.CancelIncidentSilence(ctx, i.ID, first.SilenceID, "bad-revision", actor.ID, 2, at.Add(2*time.Second)); !errors.Is(err, ErrSilenceConflict) {
		t.Fatalf("stale revision: %v", err)
	}
	canceled, err := s.CancelIncidentSilence(ctx, i.ID, first.SilenceID, "cancel", actor.ID, 1, at.Add(2*time.Second))
	if err != nil || canceled.Replayed || canceled.Revision != 2 || canceled.CanceledAt == nil {
		t.Fatalf("cancel: %+v %v", canceled, err)
	}
	replay, err := s.CancelIncidentSilence(ctx, i.ID, first.SilenceID, "cancel", actor.ID, 1, at.Add(4000*time.Second))
	if err != nil || !replay.Replayed || replay.CanceledAt == nil || !replay.CanceledAt.Equal(*canceled.CanceledAt) {
		t.Fatalf("cancel replay: %+v %v", replay, err)
	}
	if claim, err := s.ClaimDelivery(ctx, at.Add(3*time.Second), time.Minute); err != nil || claim != nil {
		t.Fatalf("cancel sent without fresh evaluation: %+v %v", claim, err)
	}
	observe(t, s, rule, node, at.Add(3*time.Second), true, true)
	if claim, err := s.ClaimDelivery(ctx, at.Add(3*time.Second), time.Minute); err != nil || claim == nil {
		t.Fatalf("cancel fresh summary: %+v %v", claim, err)
	}
}

func TestRecoveryClosesSilenceWithoutCatchupOrMutingNextEpisode(t *testing.T) {
	s, rule, node, actor, at, i := silenceFixture(t)
	ctx := context.Background()
	prior, err := s.ClaimDelivery(ctx, at, time.Minute)
	if err != nil || prior == nil {
		t.Fatalf("prior: %+v %v", prior, err)
	}
	if err := s.CompleteDelivery(ctx, prior.ID, prior.LeaseToken, at.Add(time.Second), DeliveryOutcome{Delivered: true}); err != nil {
		t.Fatal(err)
	}
	created, err := s.CreateIncidentSilence(ctx, i.ID, "create", actor.ID, CreateIncidentSilenceParams{DurationSeconds: 3600}, at.Add(2*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	resolved := observe(t, s, rule, node, at.Add(3*time.Second), true, false)
	w := readSilence(t, s, created.SilenceID)
	if resolved.State != "resolved" || w.ClosedAt == nil || !w.ClosedAt.Equal(at.Add(3*time.Second)) || w.ClosureReason != "recovered" || w.Revision != 2 {
		t.Fatalf("recovery: %+v %+v", resolved, w)
	}
	if rowCount(t, s, "notification_deliveries") != 1 {
		t.Fatal("recovery during silence queued a notification")
	}
	if _, err := s.CancelIncidentSilence(ctx, i.ID, w.ID, "new-cancel", actor.ID, 2, at.Add(4*time.Second)); !errors.Is(err, ErrSilenceConflict) {
		t.Fatalf("canceled closed silence: %v", err)
	}
	if _, err := s.CreateIncidentSilence(ctx, i.ID, "new-create", actor.ID, CreateIncidentSilenceParams{DurationSeconds: 60}, at.Add(4*time.Second)); !errors.Is(err, ErrIncidentNotFiring) {
		t.Fatalf("silenced a resolved event: %v", err)
	}
	next := observe(t, s, rule, node, at.Add(5*time.Second), true, true)
	if next.ID == i.ID || next.State != "firing" || rowCount(t, s, "notification_deliveries") != 2 {
		t.Fatal("silence inherited by a new episode")
	}
}

func TestCombinedSilencePreservesExistingAcknowledgement(t *testing.T) {
	s, _, _, actor, at, i := silenceFixture(t)
	ctx := context.Background()
	first, err := s.AcknowledgeIncident(ctx, i.ID, "original-ack", "original note", actor.ID)
	if err != nil {
		t.Fatal(err)
	}
	other, err := s.CreateUser(ctx, "other fixture", "synthetic")
	if err != nil {
		t.Fatal(err)
	}
	combined, err := s.CreateIncidentSilence(ctx, i.ID, "combined", other.ID, CreateIncidentSilenceParams{DurationSeconds: 60, Acknowledge: true, Reason: "new work"}, at.Add(time.Second))
	if err != nil || combined.AcknowledgementCreated {
		t.Fatalf("combined existing ACK: %+v %v", combined, err)
	}
	view, err := s.IncidentAcknowledgement(ctx, i.ID)
	if err != nil || view.Acknowledgement == nil || !reflect.DeepEqual(*view.Acknowledgement, first.Acknowledgement) || rowCount(t, s, "incident_acknowledgements") != 1 {
		t.Fatalf("overwrote original ACK: %+v %v", view, err)
	}
}

func TestSilenceValidationAndCombinedRollback(t *testing.T) {
	s, _, _, actor, at, i := silenceFixture(t)
	ctx := context.Background()
	for _, p := range []CreateIncidentSilenceParams{
		{DurationSeconds: 59}, {DurationSeconds: 604801},
		{DurationSeconds: 60, Reason: strings.Repeat("界", 334)},
		{DurationSeconds: 60, Reason: "bad\x00note"},
		{DurationSeconds: 60, Reason: string([]byte{0xff})},
	} {
		if _, err := s.CreateIncidentSilence(ctx, i.ID, "invalid", actor.ID, p, at.Add(time.Second)); !errors.Is(err, ErrInvalidSilence) {
			t.Fatalf("validation: %+v %v", p, err)
		}
	}
	before, err := s.Incident(ctx, i.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"incident_silence_decisions", "delivery_silence_links", "incident_silence_requests", "audit_log"} {
		if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fail_silence BEFORE INSERT ON `+table+` BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
			t.Fatal(err)
		}
		if _, err := s.CreateIncidentSilence(ctx, i.ID, "rollback", actor.ID, CreateIncidentSilenceParams{DurationSeconds: 60, Acknowledge: true}, at.Add(time.Second)); err == nil {
			t.Fatalf("%s failure ignored", table)
		}
		for _, empty := range []string{"incident_silences", "incident_silence_requests", "incident_silence_decisions", "delivery_silence_links", "incident_acknowledgements", "audit_log"} {
			if rowCount(t, s, empty) != 0 {
				t.Fatalf("partial write after %s failure: %s", table, empty)
			}
		}
		after, err := s.Incident(ctx, i.ID)
		jobs, jobsErr := s.ListIncidentDeliveries(ctx, i.ID, 0, 10)
		if err != nil || !reflect.DeepEqual(before, after) || jobsErr != nil || len(jobs) != 1 || jobs[0].Status != "pending" {
			t.Fatalf("partial state after %s: %+v %+v %v %v", table, after, jobs, err, jobsErr)
		}
		if _, err := s.db.ExecContext(ctx, `DROP TRIGGER fail_silence`); err != nil {
			t.Fatal(err)
		}
	}
	if result, err := s.CreateIncidentSilence(ctx, i.ID, "rollback", actor.ID, CreateIncidentSilenceParams{DurationSeconds: 60, Acknowledge: true}, at.Add(time.Second)); err != nil || !result.AcknowledgementCreated {
		t.Fatalf("retry after rollback: %+v %v", result, err)
	}
}

func TestSilenceActionWatermarkKeepsSubmillisecondOrdering(t *testing.T) {
	s, rule, node, actor, at, i := silenceFixture(t)
	ctx := context.Background()
	observedAt := at.Add(100 * time.Microsecond)
	observe(t, s, rule, node, observedAt, true, true)
	actionAt := at.Add(200 * time.Microsecond)
	result, err := s.CreateIncidentSilence(ctx, i.ID, "subms", actor.ID, CreateIncidentSilenceParams{DurationSeconds: 60}, actionAt)
	if err != nil || !result.StartsAt.Equal(at) {
		t.Fatalf("valid action rejected by timestamp flooring: %+v %v", result, err)
	}
	old := observe(t, s, rule, node, at.Add(150*time.Microsecond), true, false)
	if old.State != "firing" || !old.UpdatedAt.Equal(actionAt) || old.ObservedAt == nil || !old.ObservedAt.Equal(observedAt) {
		t.Fatalf("old capture crossed subms watermark: %+v", old)
	}
	if _, err := s.CancelIncidentSilence(ctx, i.ID, result.SilenceID, "cancel-subms", actor.ID, 1, at.Add(300*time.Microsecond)); err != nil {
		t.Fatal(err)
	}
	current := observe(t, s, rule, node, at.Add(400*time.Microsecond), true, true)
	if current.State != "firing" || rowCount(t, s, "notification_deliveries") != 2 {
		t.Fatal("fresh capture did not resume after same-millisecond cancellation")
	}
}

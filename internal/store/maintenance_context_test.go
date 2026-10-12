package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestMaintenanceContextDecisionAndDeliveryLinks(t *testing.T) {
	s, rule, node := incidentFixture(t, `{"threshold_percent":90}`)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Second)
	i := observe(t, s, rule, node, at, true, true)
	jobs, err := s.ListIncidentDeliveries(ctx, i.ID, 0, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("initial jobs: %+v %v", jobs, err)
	}
	jobID := jobs[0].ID
	create := func(start, end time.Time) MaintenanceWindow {
		t.Helper()
		w, err := s.CreateMaintenanceWindow(ctx, MaintenanceWindow{Reason: "planned work", Timezone: "UTC", CreatedBy: "fixture", NodeIDs: []string{node.ID}, StartsAt: start, EndsAt: end})
		if err != nil {
			t.Fatal(err)
		}
		return w
	}
	first := create(at.Add(time.Second), at.Add(time.Minute))
	second := create(at.Add(2*time.Second), at.Add(2*time.Minute))
	before, err := s.ListIncidentMaintenance(ctx, i.ID, "", 10, at.Add(3*time.Second))
	if err != nil || !before.NodeInMaintenance || len(before.Windows) != 2 {
		t.Fatalf("active context: %+v %v", before, err)
	}
	for _, w := range before.Windows {
		if !w.Active || len(w.Decisions) != 0 {
			t.Fatalf("read invented history: %+v", w)
		}
	}
	delivery, err := s.ListDeliveryMaintenance(ctx, jobID, "", 10)
	if err != nil || len(delivery.Windows) != 0 {
		t.Fatalf("read invented delivery association: %+v %v", delivery, err)
	}
	observe(t, s, rule, node, at.Add(3*time.Second), true, true)
	after, err := s.ListIncidentMaintenance(ctx, i.ID, "", 1, at.Add(4*time.Second))
	if err != nil || len(after.Windows) != 1 || after.NextCursor == "" || len(after.Windows[0].Decisions) != 2 {
		t.Fatalf("recorded page: %+v %v", after, err)
	}
	last, err := s.ListIncidentMaintenance(ctx, i.ID, after.NextCursor, 1, at.Add(4*time.Second))
	if err != nil || len(last.Windows) != 1 || last.NextCursor != "" || last.Windows[0].ID == after.Windows[0].ID {
		t.Fatalf("next page: %+v %v", last, err)
	}
	for _, w := range append(after.Windows, last.Windows...) {
		for _, d := range w.Decisions {
			if d.Kind != "observation" && d.Kind != "observation_gap" || !d.FirstAt.Equal(at.Add(3*time.Second)) || !d.LastAt.Equal(d.FirstAt) {
				t.Fatalf("decision: %+v", d)
			}
		}
	}
	delivery, err = s.ListDeliveryMaintenance(ctx, jobID, "", 1)
	if err != nil || len(delivery.Windows) != 1 || delivery.NextCursor == "" {
		t.Fatalf("delivery page: %+v %v", delivery, err)
	}
	lastDelivery, err := s.ListDeliveryMaintenance(ctx, jobID, delivery.NextCursor, 1)
	if err != nil || len(lastDelivery.Windows) != 1 || lastDelivery.NextCursor != "" || lastDelivery.Windows[0].ID == delivery.Windows[0].ID {
		t.Fatalf("delivery next: %+v %v", lastDelivery, err)
	}
	observe(t, s, rule, node, at.Add(5*time.Second), true, true)
	if err := s.CancelMaintenanceWindow(ctx, first.ID, 1, at.Add(6*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := s.CancelMaintenanceWindow(ctx, second.ID, 1, at.Add(6*time.Second)); err != nil {
		t.Fatal(err)
	}
	history, err := s.ListIncidentMaintenance(ctx, i.ID, "", 10, at.Add(7*time.Second))
	if err != nil || history.NodeInMaintenance || len(history.Windows) != 2 {
		t.Fatalf("ended context: %+v %v", history, err)
	}
	for _, w := range history.Windows {
		if w.Active || w.CanceledAt == nil {
			t.Fatalf("history window: %+v", w)
		}
		for _, d := range w.Decisions {
			if !d.FirstAt.Equal(at.Add(3*time.Second)) || !d.LastAt.Equal(at.Add(5*time.Second)) {
				t.Fatalf("decision range: %+v", d)
			}
		}
	}
	if err := s.DeleteNode(ctx, node.ID); err != nil {
		t.Fatal(err)
	}
	history, err = s.ListIncidentMaintenance(ctx, i.ID, "", 10, at.Add(7*time.Second))
	if err != nil || history.NodeExists || history.NodeInMaintenance || len(history.Windows) != 2 {
		t.Fatalf("deleted node history: %+v %v", history, err)
	}
	snapshot := filepath.Join(t.TempDir(), "history.db")
	if err := s.ConsistentBackup(ctx, snapshot); err != nil {
		t.Fatal(err)
	}
	path := s.path
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := StageDatabaseRestore(ctx, path, snapshot); err != nil {
		t.Fatal(err)
	}
	if _, err := ApplyPendingRestore(ctx, path, at.Add(8*time.Second)); err != nil {
		t.Fatal(err)
	}
	restored, err := Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	history, err = restored.ListIncidentMaintenance(ctx, i.ID, "", 10, at.Add(9*time.Second))
	if err != nil || len(history.Windows) != 2 || history.NodeExists {
		t.Fatalf("restored context: %+v %v", history, err)
	}
	delivery, err = restored.ListDeliveryMaintenance(ctx, jobID, "", 10)
	if err != nil || len(delivery.Windows) != 2 {
		t.Fatalf("restored delivery associations: %+v %v", delivery, err)
	}
}

func TestMaintenanceDecisionFailureRollsBackSuppression(t *testing.T) {
	s, rule, node := incidentFixture(t, `{"threshold_percent":90}`)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Second)
	i := observe(t, s, rule, node, at, true, true)
	if _, err := s.CreateMaintenanceWindow(ctx, MaintenanceWindow{Reason: "work", Timezone: "UTC", CreatedBy: "fixture", NodeIDs: []string{node.ID}, StartsAt: at.Add(time.Second), EndsAt: at.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fail_decision BEFORE INSERT ON incident_maintenance_decisions BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ObserveAlert(ctx, rule, node, AlertObservation{At: at.Add(2 * time.Second), Known: true, Active: true}); err == nil {
		t.Fatal("decision failure accepted")
	}
	if _, err := s.ClaimDelivery(ctx, at.Add(2*time.Second), time.Minute); err == nil {
		t.Fatal("claim accepted failed decision write")
	}
	current, err := s.Incident(ctx, i.ID)
	if err != nil || !current.UpdatedAt.Equal(at) {
		t.Fatalf("partial observation: %+v %v", current, err)
	}
	jobs, err := s.ListIncidentDeliveries(ctx, i.ID, 0, 10)
	if err != nil || len(jobs) != 1 || jobs[0].Status != "pending" {
		t.Fatalf("partial cancellation: %+v %v", jobs, err)
	}
	if got := rowCount(t, s, "delivery_maintenance_links"); got != 0 {
		t.Fatalf("partial links: %d", got)
	}
	for _, limit := range []int{0, 101} {
		if _, err := s.ListIncidentMaintenance(ctx, i.ID, "", limit, at); !errors.Is(err, ErrInvalidMaintenance) {
			t.Fatalf("limit: %v", err)
		}
	}
	if _, err := s.ListIncidentMaintenance(ctx, "missing", "", 10, at); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown incident: %v", err)
	}
	if _, err := s.ListDeliveryMaintenance(ctx, "missing", "", 10); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown delivery: %v", err)
	}
}

func TestMaintenancePreSendLinkRollbackAndRetry(t *testing.T) {
	s, rule, node := incidentFixture(t, `{"threshold_percent":90}`)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Second)
	i := observe(t, s, rule, node, at, true, true)
	job, err := s.ClaimDelivery(ctx, at, time.Minute)
	if err != nil || job == nil {
		t.Fatalf("claim: %+v %v", job, err)
	}
	if _, err := s.CreateMaintenanceWindow(ctx, MaintenanceWindow{Reason: "pre-send work", Timezone: "UTC", CreatedBy: "fixture", NodeIDs: []string{node.ID}, StartsAt: at.Add(time.Second), EndsAt: at.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fail_delivery_link BEFORE INSERT ON delivery_maintenance_links BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckDeliveryLease(ctx, job.ID, job.LeaseToken, at.Add(2*time.Second)); err == nil || errors.Is(err, ErrDeliveryCanceled) {
		t.Fatalf("failed link accepted: %v", err)
	}
	current, err := s.Delivery(ctx, job.ID)
	if err != nil || current.Status != "inflight" {
		t.Fatalf("partial cancel: %+v %v", current, err)
	}
	if got := rowCount(t, s, "incident_maintenance_decisions"); got != 0 {
		t.Fatalf("partial decision: %d", got)
	}
	if _, err := s.db.ExecContext(ctx, `DROP TRIGGER fail_delivery_link`); err != nil {
		t.Fatal(err)
	}
	if err := s.CheckDeliveryLease(ctx, job.ID, job.LeaseToken, at.Add(3*time.Second)); !errors.Is(err, ErrDeliveryCanceled) {
		t.Fatalf("suppressed retry: %v", err)
	}
	delivery, err := s.ListDeliveryMaintenance(ctx, job.ID, "", 10)
	if err != nil || len(delivery.Windows) != 1 || !delivery.Windows[0].RecordedAt.Equal(at.Add(3*time.Second)) {
		t.Fatalf("pre-send link: %+v %v", delivery, err)
	}
	history, err := s.ListIncidentMaintenance(ctx, i.ID, "", 10, at.Add(4*time.Second))
	if err != nil || len(history.Windows) != 1 || len(history.Windows[0].Decisions) != 1 || history.Windows[0].Decisions[0].Kind != "delivery" {
		t.Fatalf("pre-send decision: %+v %v", history, err)
	}
}

func TestWorkerMaintenanceLinksDoNotAttributeLaterJobs(t *testing.T) {
	s, rule, node := incidentFixture(t, `{"threshold_percent":90}`)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Second)
	i := observe(t, s, rule, node, at, true, true)
	jobs, err := s.ListIncidentDeliveries(ctx, i.ID, 0, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("initial jobs: %+v %v", jobs, err)
	}
	oldID := jobs[0].ID
	if _, err := s.CreateMaintenanceWindow(ctx, MaintenanceWindow{Reason: "downtime work", Timezone: "UTC", CreatedBy: "fixture", NodeIDs: []string{node.ID}, StartsAt: at.Add(10 * time.Second), EndsAt: at.Add(20 * time.Second)}); err != nil {
		t.Fatal(err)
	}
	if claimed, err := s.ClaimDelivery(ctx, at.Add(30*time.Second), time.Minute); err != nil || claimed != nil {
		t.Fatalf("old claim: %+v %v", claimed, err)
	}
	old, err := s.ListDeliveryMaintenance(ctx, oldID, "", 10)
	if err != nil || len(old.Windows) != 1 {
		t.Fatalf("worker link: %+v %v", old, err)
	}
	observe(t, s, rule, node, at.Add(31*time.Second), true, true)
	newJob, err := s.ClaimDelivery(ctx, at.Add(31*time.Second), time.Minute)
	if err != nil || newJob == nil {
		t.Fatalf("current claim: %+v %v", newJob, err)
	}
	current, err := s.ListDeliveryMaintenance(ctx, newJob.ID, "", 10)
	if err != nil || len(current.Windows) != 0 {
		t.Fatalf("attributed later job: %+v %v", current, err)
	}
	history, err := s.ListIncidentMaintenance(ctx, i.ID, "", 10, at.Add(32*time.Second))
	if err != nil || len(history.Windows) != 1 || len(history.Windows[0].Decisions) != 2 {
		t.Fatalf("gap/worker context: %+v %v", history, err)
	}
}

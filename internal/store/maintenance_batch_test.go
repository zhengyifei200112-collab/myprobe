package store

import (
	"context"
	"testing"
	"time"
)

// Failure on the last ID occurs after the first 200-row batch. Earlier rows,
// links and task state must roll back together, then succeed on a fresh retry.
func TestMaintenanceDecisionBatchesAreAtomic(t *testing.T) {
	for _, table := range []string{"incident_maintenance_decisions", "delivery_maintenance_links"} {
		t.Run(table, func(t *testing.T) {
			s, rule, node := incidentFixture(t, `{"threshold_percent":90}`)
			ctx := context.Background()
			at := time.Now().UTC().Truncate(time.Second)
			i := observe(t, s, rule, node, at.Add(-time.Second), true, true)
			for range 201 {
				if _, err := s.CreateMaintenanceWindow(ctx, MaintenanceWindow{Reason: "batch fixture", Timezone: "UTC", CreatedBy: "fixture", NodeIDs: []string{node.ID}, StartsAt: at, EndsAt: at.Add(time.Hour)}); err != nil {
					t.Fatal(err)
				}
			}
			// Table names are fixed fixture constants, not supplied by callers.
			if _, err := s.db.ExecContext(ctx, `CREATE TRIGGER fail_last_batch BEFORE INSERT ON `+table+` WHEN NEW.window_id=(SELECT max(id) FROM maintenance_windows) BEGIN SELECT RAISE(ABORT,'fixture'); END`); err != nil {
				t.Fatal(err)
			}
			if _, err := s.ObserveAlert(ctx, rule, node, AlertObservation{At: at, Known: true, Active: true}); err == nil {
				t.Fatal("failed second batch accepted")
			}
			if rowCount(t, s, "incident_maintenance_decisions") != 0 || rowCount(t, s, "delivery_maintenance_links") != 0 {
				t.Fatal("partial batch records persisted")
			}
			jobs, err := s.ListIncidentDeliveries(ctx, i.ID, 0, 10)
			if err != nil || len(jobs) != 1 || jobs[0].Status != "pending" {
				t.Fatalf("partial cancellation: %+v %v", jobs, err)
			}
			before, err := s.Incident(ctx, i.ID)
			if err != nil || before.LastEnqueuedAt == nil || !before.LastEnqueuedAt.Equal(at.Add(-time.Second)) || before.ObservedAt == nil || !before.ObservedAt.Equal(at.Add(-time.Second)) {
				t.Fatalf("partial incident: %+v %v", before, err)
			}
			if _, err := s.db.ExecContext(ctx, `DROP TRIGGER fail_last_batch`); err != nil {
				t.Fatal(err)
			}
			observe(t, s, rule, node, at, true, true)
			observe(t, s, rule, node, at.Add(time.Second), true, true)
			if rowCount(t, s, "incident_maintenance_decisions") != 402 || rowCount(t, s, "delivery_maintenance_links") != 201 {
				t.Fatal("retry lost or duplicated batch records")
			}
			jobs, err = s.ListIncidentDeliveries(ctx, i.ID, 0, 10)
			if err != nil || len(jobs) != 1 || jobs[0].Status != "canceled" || jobs[0].ErrorClass != "maintenance" {
				t.Fatalf("retry cancellation: %+v %v", jobs, err)
			}
			page, err := s.ListDeliveryMaintenance(ctx, jobs[0].ID, "", 100)
			if err != nil || len(page.Windows) != 100 || page.NextCursor == "" {
				t.Fatalf("batch context: %+v %v", page, err)
			}
			for _, link := range page.Windows {
				if !link.RecordedAt.Equal(at) {
					t.Fatal("later no-op rewrote original cancellation association")
				}
			}
		})
	}
}

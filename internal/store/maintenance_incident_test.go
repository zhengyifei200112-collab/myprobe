package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestMaintenanceSkipsOldDeliveryAfterDowntime(t *testing.T) {
	s, rule, node := incidentFixture(t, `{"threshold_percent":90}`)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Second)
	i := observe(t, s, rule, node, at, true, true)
	jobs, err := s.ListIncidentDeliveries(ctx, i.ID, 0, 10)
	if err != nil || len(jobs) != 1 {
		t.Fatalf("jobs: %+v %v", jobs, err)
	}
	if _, err := s.CreateMaintenanceWindow(ctx, MaintenanceWindow{Reason: "offline maintenance", Timezone: "UTC", CreatedBy: "admin", NodeIDs: []string{node.ID}, StartsAt: at.Add(time.Minute), EndsAt: at.Add(2 * time.Minute)}); err != nil {
		t.Fatal(err)
	}
	path := s.path
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := at.Add(3 * time.Minute)
	if job, err := s.ClaimDelivery(ctx, now, time.Minute); err != nil || job != nil {
		t.Fatalf("old transition delivered after downtime: %+v %v", job, err)
	}
	saved, err := s.Delivery(ctx, jobs[0].ID)
	if err != nil || saved.Status != "canceled" || saved.ErrorClass != "maintenance" {
		t.Fatalf("historical cancellation: %+v %v", saved, err)
	}
	observe(t, s, rule, node, now, true, true)
	current, err := s.ClaimDelivery(ctx, now, time.Minute)
	if err != nil || current == nil || current.ID == jobs[0].ID {
		t.Fatalf("current fault: %+v %v", current, err)
	}
	if err := s.CheckDeliveryLease(ctx, current.ID, current.LeaseToken, now.Add(time.Second)); err != nil {
		t.Fatalf("new transition incorrectly suppressed by past window: %v", err)
	}
}

func TestMaintenanceCancelsPendingAndLeasedDelivery(t *testing.T) {
	for _, leased := range []bool{false, true} {
		t.Run(map[bool]string{false: "pending", true: "leased"}[leased], func(t *testing.T) {
			s, rule, node := incidentFixture(t, `{"threshold_percent":90}`)
			ctx := context.Background()
			at := time.Now().UTC().Truncate(time.Second)
			i := observe(t, s, rule, node, at, true, true)
			jobs, err := s.ListIncidentDeliveries(ctx, i.ID, 0, 10)
			if err != nil || len(jobs) != 1 {
				t.Fatalf("jobs: %+v %v", jobs, err)
			}
			var claimed *NotificationDelivery
			if leased {
				claimed, err = s.ClaimDelivery(ctx, at, time.Minute)
				if err != nil || claimed == nil {
					t.Fatalf("claim: %+v %v", claimed, err)
				}
			}
			start := at.Add(time.Second)
			if _, err := s.CreateMaintenanceWindow(ctx, MaintenanceWindow{Reason: "work", Timezone: "UTC", CreatedBy: "admin", NodeIDs: []string{node.ID}, StartsAt: start, EndsAt: start.Add(time.Minute)}); err != nil {
				t.Fatal(err)
			}
			if leased {
				if err := s.CheckDeliveryLease(ctx, claimed.ID, claimed.LeaseToken, start); !errors.Is(err, ErrDeliveryCanceled) {
					t.Fatalf("pre-send: %v", err)
				}
			} else if job, err := s.ClaimDelivery(ctx, start, time.Minute); err != nil || job != nil {
				t.Fatalf("maintenance claim: %+v %v", job, err)
			}
			saved, err := s.Delivery(ctx, jobs[0].ID)
			if err != nil || saved.Status != "canceled" || saved.ErrorClass != "maintenance" {
				t.Fatalf("saved: %+v %v", saved, err)
			}
			if job, err := s.ClaimDelivery(ctx, start.Add(time.Minute), time.Minute); err != nil || job != nil {
				t.Fatalf("old job resurrected: %+v %v", job, err)
			}
			observe(t, s, rule, node, start.Add(time.Minute), true, true)
			job, err := s.ClaimDelivery(ctx, start.Add(time.Minute), time.Minute)
			if err != nil || job == nil || job.ID == jobs[0].ID {
				t.Fatalf("fresh post-window: %+v %v", job, err)
			}
		})
	}
}

func TestMaintenancePreservesIncidentsWithoutQueueing(t *testing.T) {
	for _, recoverInside := range []bool{true, false} {
		t.Run(map[bool]string{true: "recovers", false: "continues"}[recoverInside], func(t *testing.T) {
			s, rule, node := incidentFixture(t, `{"threshold_percent":90}`)
			ctx := context.Background()
			start := time.Now().UTC().Truncate(time.Second)
			_, err := s.CreateMaintenanceWindow(ctx, MaintenanceWindow{Reason: "work", Timezone: "UTC", CreatedBy: "admin", NodeIDs: []string{node.ID}, StartsAt: start, EndsAt: start.Add(time.Minute)})
			if err != nil {
				t.Fatal(err)
			}
			i := observe(t, s, rule, node, start, true, true)
			if i.State != "firing" || rowCount(t, s, "notification_deliveries") != 0 {
				t.Fatal("maintenance lost incident or queued firing")
			}
			i = observe(t, s, rule, node, start.Add(30*time.Second), true, !recoverInside)
			if recoverInside && i.State != "resolved" {
				t.Fatal("maintenance blocked recovery")
			}
			if rowCount(t, s, "notification_deliveries") != 0 {
				t.Fatal("queued in-window notification")
			}
			if !recoverInside {
				observe(t, s, rule, node, start.Add(time.Minute), false, true)
				if rowCount(t, s, "notification_deliveries") != 0 {
					t.Fatal("stale evidence notified at end")
				}
			}
			observe(t, s, rule, node, start.Add(61*time.Second), true, !recoverInside)
			observe(t, s, rule, node, start.Add(62*time.Second), true, !recoverInside)
			want := 0
			if !recoverInside {
				want = 1
			}
			if got := rowCount(t, s, "notification_deliveries"); got != want {
				t.Fatalf("post-maintenance jobs %d want %d", got, want)
			}
		})
	}
}

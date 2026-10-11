package store

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestOverlappingMaintenanceWaitsForLastWindow(t *testing.T) {
	for _, cancelLast := range []bool{false, true} {
		t.Run(map[bool]string{false: "natural-end", true: "cancel-last"}[cancelLast], func(t *testing.T) {
			s, rule, node := incidentFixture(t, `{"threshold_percent":90}`)
			ctx := context.Background()
			at := time.Now().UTC().Truncate(time.Second)
			create := func(start, end time.Time) MaintenanceWindow {
				t.Helper()
				w, err := s.CreateMaintenanceWindow(ctx, MaintenanceWindow{Reason: "overlap", Timezone: "UTC", CreatedBy: "admin", NodeIDs: []string{node.ID}, StartsAt: start, EndsAt: end})
				if err != nil {
					t.Fatal(err)
				}
				return w
			}
			first := create(at, at.Add(time.Minute))
			last := create(at.Add(20*time.Second), at.Add(2*time.Minute))
			i := observe(t, s, rule, node, at, true, true)
			observe(t, s, rule, node, at.Add(25*time.Second), true, true)
			if err := s.CancelMaintenanceWindow(ctx, first.ID, 1, at.Add(30*time.Second)); err != nil {
				t.Fatal(err)
			}
			observe(t, s, rule, node, at.Add(30*time.Second), true, true)
			observe(t, s, rule, node, at.Add(time.Minute), true, true)
			if got := rowCount(t, s, "notification_deliveries"); got != 0 {
				t.Fatalf("notified while second window active: %d", got)
			}
			end := at.Add(2 * time.Minute)
			if cancelLast {
				end = at.Add(90 * time.Second)
				if err := s.CancelMaintenanceWindow(ctx, last.ID, 1, end); err != nil {
					t.Fatal(err)
				}
			}
			observe(t, s, rule, node, end, true, true)
			jobs, err := s.ListIncidentDeliveries(ctx, i.ID, 0, 10)
			if err != nil || len(jobs) != 1 {
				t.Fatalf("final window summary: %+v %v", jobs, err)
			}
			job, err := s.ClaimDelivery(ctx, end, time.Minute)
			if err != nil || job == nil {
				t.Fatalf("summary claim: %+v %v", job, err)
			}
			if err := s.CompleteDelivery(ctx, job.ID, job.LeaseToken, end.Add(time.Second), DeliveryOutcome{Delivered: true}); err != nil {
				t.Fatal(err)
			}
			observe(t, s, rule, node, end.Add(2*time.Second), true, true)
			if got := rowCount(t, s, "notification_deliveries"); got != 1 {
				t.Fatalf("repeated end summary: %d", got)
			}
		})
	}
}

func TestNotifiedIncidentRecoveryDuringMaintenanceIsNotSent(t *testing.T) {
	s, rule, node := incidentFixture(t, `{"threshold_percent":90}`)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Second)
	i := observe(t, s, rule, node, at, true, true)
	job, err := s.ClaimDelivery(ctx, at, time.Minute)
	if err != nil || job == nil {
		t.Fatalf("claim: %+v %v", job, err)
	}
	if err := s.CompleteDelivery(ctx, job.ID, job.LeaseToken, at.Add(time.Second), DeliveryOutcome{Delivered: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateMaintenanceWindow(ctx, MaintenanceWindow{Reason: "work", Timezone: "UTC", CreatedBy: "admin", NodeIDs: []string{node.ID}, StartsAt: at.Add(10 * time.Second), EndsAt: at.Add(time.Minute)}); err != nil {
		t.Fatal(err)
	}
	resolved := observe(t, s, rule, node, at.Add(20*time.Second), true, false)
	if resolved.State != "resolved" || resolved.ResolutionReason != "recovered" {
		t.Fatalf("recovery not recorded: %+v", resolved)
	}
	observe(t, s, rule, node, at.Add(time.Minute), true, false)
	jobs, err := s.ListIncidentDeliveries(ctx, i.ID, 0, 10)
	if err != nil || len(jobs) != 1 || jobs[0].Status != "delivered" {
		t.Fatalf("recovery catch-up or lost delivery history: %+v %v", jobs, err)
	}
}

func TestMaintenanceResumesPreviouslyDeliveredFaultAfterRestart(t *testing.T) {
	s, rule, node := incidentFixture(t, `{"threshold_percent":90}`)
	ctx := context.Background()
	at := time.Now().UTC().Truncate(time.Second)
	i := observe(t, s, rule, node, at, true, true)
	job, err := s.ClaimDelivery(ctx, at, time.Minute)
	if err != nil || job == nil {
		t.Fatalf("initial claim: %+v %v", job, err)
	}
	if err := s.CompleteDelivery(ctx, job.ID, job.LeaseToken, at.Add(time.Second), DeliveryOutcome{Delivered: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateMaintenanceWindow(ctx, MaintenanceWindow{Reason: "planned restart", Timezone: "UTC", CreatedBy: "admin", NodeIDs: []string{node.ID}, StartsAt: at.Add(10 * time.Second), EndsAt: at.Add(30 * time.Second)}); err != nil {
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
	observe(t, s, rule, node, at.Add(35*time.Second), false, true)
	if got := rowCount(t, s, "notification_deliveries"); got != 1 {
		t.Fatalf("unknown evidence notified: %d", got)
	}
	current := observe(t, s, rule, node, at.Add(40*time.Second), true, true)
	if current.ID != i.ID {
		t.Fatal("maintenance replaced incident identity")
	}
	jobs, err := s.ListIncidentDeliveries(ctx, i.ID, 0, 10)
	if err != nil || len(jobs) != 2 {
		t.Fatalf("fresh summary missing: %+v %v", jobs, err)
	}
	next, err := s.ClaimDelivery(ctx, at.Add(40*time.Second), time.Minute)
	if err != nil || next == nil {
		t.Fatalf("summary claim: %+v %v", next, err)
	}
	if err := s.CompleteDelivery(ctx, next.ID, next.LeaseToken, at.Add(41*time.Second), DeliveryOutcome{Delivered: true}); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	observe(t, s, rule, node, at.Add(42*time.Second), true, true)
	if got := rowCount(t, s, "notification_deliveries"); got != 2 {
		t.Fatalf("summary repeated after restart: %d", got)
	}
}

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

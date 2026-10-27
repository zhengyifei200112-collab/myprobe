package store

import (
	"context"
	"testing"
	"time"
)

func TestSilenceAndMaintenanceWaitForLastSuppression(t *testing.T) {
	for _, mode := range []string{"maintenance-expires", "silence-expires", "maintenance-canceled", "silence-canceled"} {
		t.Run(mode, func(t *testing.T) {
			s, rule, node, actor, at, i := silenceFixture(t)
			ctx := context.Background()
			end := at.Add(121 * time.Second)
			if mode == "maintenance-expires" {
				end = at.Add(32 * time.Second)
			}
			w, err := s.CreateMaintenanceWindow(ctx, MaintenanceWindow{Reason: "overlap", Timezone: "UTC", CreatedBy: actor.ID, NodeIDs: []string{node.ID}, StartsAt: at.Add(time.Second), EndsAt: end})
			if err != nil {
				t.Fatal(err)
			}
			silence, err := s.CreateIncidentSilence(ctx, i.ID, "silence", actor.ID, CreateIncidentSilenceParams{DurationSeconds: 60}, at.Add(2*time.Second))
			if err != nil {
				t.Fatal(err)
			}
			jobs, err := s.ListIncidentDeliveries(ctx, i.ID, 0, 10)
			if err != nil || len(jobs) != 1 || jobs[0].ErrorClass != "maintenance_and_silence" || rowCount(t, s, "delivery_silence_links") != 1 || rowCount(t, s, "delivery_maintenance_links") != 1 {
				t.Fatalf("combined cancellation: %+v %v", jobs, err)
			}
			firstEnd, lastEnd := at.Add(32*time.Second), at.Add(62*time.Second)
			switch mode {
			case "silence-expires":
				firstEnd, lastEnd = at.Add(62*time.Second), at.Add(121*time.Second)
			case "maintenance-canceled":
				firstEnd, lastEnd = at.Add(3*time.Second), at.Add(62*time.Second)
				if err := s.CancelMaintenanceWindow(ctx, w.ID, 1, firstEnd); err != nil {
					t.Fatal(err)
				}
			case "silence-canceled":
				firstEnd, lastEnd = at.Add(3*time.Second), at.Add(121*time.Second)
				if _, err := s.CancelIncidentSilence(ctx, i.ID, silence.SilenceID, "cancel", actor.ID, 1, firstEnd); err != nil {
					t.Fatal(err)
				}
			}
			observe(t, s, rule, node, firstEnd, true, true)
			if rowCount(t, s, "notification_deliveries") != 1 {
				t.Fatal("removing one suppression released notifications")
			}
			observe(t, s, rule, node, lastEnd, false, false)
			if rowCount(t, s, "notification_deliveries") != 1 {
				t.Fatal("last suppression expiry used stale evidence")
			}
			observe(t, s, rule, node, lastEnd.Add(time.Second), true, true)
			fresh, err := s.ClaimDelivery(ctx, lastEnd.Add(time.Second), time.Minute)
			if err != nil || fresh == nil || fresh.ID == jobs[0].ID || rowCount(t, s, "notification_deliveries") != 2 {
				t.Fatalf("last suppression summary: %+v %v", fresh, err)
			}
			if err := s.CompleteDelivery(ctx, fresh.ID, fresh.LeaseToken, lastEnd.Add(2*time.Second), DeliveryOutcome{Delivered: true}); err != nil {
				t.Fatal(err)
			}
			observe(t, s, rule, node, lastEnd.Add(3*time.Second), true, true)
			if rowCount(t, s, "notification_deliveries") != 2 {
				t.Fatal("repeated summary after last suppression")
			}
		})
	}
}

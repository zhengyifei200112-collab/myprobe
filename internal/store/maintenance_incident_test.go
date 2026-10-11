package store

import (
	"context"
	"testing"
	"time"
)

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

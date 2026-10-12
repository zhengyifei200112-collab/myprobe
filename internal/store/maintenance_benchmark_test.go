package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"testing"
	"time"
)

// Uses on-disk SQLite and public Store mutations. Every window contains all 100
// nodes, so overlap and decision writes reach the actual membership bound. This
// measures Store work only, not full Server/Agent or concurrent writer capacity.
func BenchmarkMaintenanceOverlap(b *testing.B) {
	for _, count := range []int{1, 100, 1000} {
		b.Run(fmt.Sprintf("windows-%d", count), func(b *testing.B) {
			ctx := context.Background()
			db, err := Open(ctx, filepath.Join(b.TempDir(), "maintenance.db"))
			if err != nil {
				b.Fatal(err)
			}
			defer db.Close()
			channel, err := db.CreateNotificationChannel(ctx, "fixture", ChannelKindWebhook, "synthetic")
			if err != nil {
				b.Fatal(err)
			}
			nodes := make([]Node, 100)
			rules := make([]AlertRule, len(nodes))
			ids := make([]string, len(nodes))
			incidents := make([]string, len(nodes))
			at := time.Now().UTC().Truncate(time.Millisecond)
			for i := range nodes {
				node, _, err := db.CreateNode(ctx, CreateNodeParams{Name: fmt.Sprintf("fixture-%d", i)})
				if err != nil {
					b.Fatal(err)
				}
				nodes[i], ids[i] = node, node.ID
				rules[i], err = db.CreateAlertRule(ctx, node.ID, channel.ID, "cpu", json.RawMessage(`{"threshold_percent":90}`), 900)
				if err != nil {
					b.Fatal(err)
				}
				incident, err := db.ObserveAlert(ctx, rules[i], node, AlertObservation{At: at.Add(-time.Second), Known: true, Active: true, Message: "fixture fault"})
				if err != nil || incident == nil {
					b.Fatalf("seed incident: %+v %v", incident, err)
				}
				incidents[i] = incident.ID
			}
			draft := MaintenanceWindow{Reason: "capacity fixture", Timezone: "UTC", CreatedBy: "fixture", NodeIDs: ids, StartsAt: at, EndsAt: at.Add(365 * 24 * time.Hour)}
			for range count {
				if _, err := db.CreateMaintenanceWindow(ctx, draft); err != nil {
					b.Fatal(err)
				}
			}
			if count == 1000 {
				if _, err := db.CreateMaintenanceWindow(ctx, draft); !errors.Is(err, ErrMaintenanceLimit) {
					b.Fatalf("capacity admission: %v", err)
				}
			}
			observePass := func(tb testing.TB) {
				for i := range nodes {
					incident, err := db.ObserveAlert(ctx, rules[i], nodes[i], AlertObservation{At: at, Known: true, Active: true, Message: "fixture fault"})
					if err != nil || incident == nil || incident.State != "firing" || incident.LastEnqueuedAt != nil {
						tb.Fatalf("suppressed observation: %+v %v", incident, err)
					}
				}
				at = at.Add(time.Second)
			}
			first := time.Now()
			observePass(b) // Includes linking and canceling all 100 queued jobs.
			firstElapsed := time.Since(first)
			observePass(b) // Warm both observation and observation-gap decision keys.
			b.Run("warm-pass-100-nodes", func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					observePass(b)
				}
				b.StopTimer()
				b.ReportMetric(float64(firstElapsed)/float64(time.Millisecond), "first-pass-ms")
			})
			b.Run("context-page-100", func(b *testing.B) {
				b.ReportAllocs()
				b.ResetTimer()
				for range b.N {
					page, err := db.ListIncidentMaintenance(ctx, incidents[0], "", 100, at)
					if err != nil || len(page.Windows) != min(count, 100) || !page.NodeInMaintenance || ((count > 100) != (page.NextCursor != "")) {
						b.Fatalf("context page: %+v %v", page, err)
					}
				}
			})
			for _, check := range []struct {
				query string
				want  int
			}{
				{`SELECT COUNT(*) FROM maintenance_windows`, count},
				{`SELECT COUNT(*) FROM maintenance_window_nodes`, count * len(nodes)},
				{`SELECT COUNT(*) FROM incident_maintenance_decisions`, 2 * count * len(nodes)},
				{`SELECT COUNT(*) FROM delivery_maintenance_links`, count * len(nodes)},
				{`SELECT COUNT(*) FROM notification_deliveries WHERE status='canceled' AND error_class='maintenance'`, len(nodes)},
				{`SELECT COUNT(*) FROM notification_deliveries WHERE status!='canceled'`, 0},
			} {
				var got int
				if err := db.db.QueryRowContext(ctx, check.query).Scan(&got); err != nil || got != check.want {
					b.Fatalf("fixture population: got=%d want=%d query=%s err=%v", got, check.want, check.query, err)
				}
			}
		})
	}
}

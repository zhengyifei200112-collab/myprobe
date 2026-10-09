package store

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/alertpolicy"
)

// Measures warm, unchanged preparation with real on-disk SQLite transactions.
// Fixture creation and the first materialization are outside the timed region.
func BenchmarkPrepareAlertPolicyRules(b *testing.B) {
	for _, count := range []int{5, 50, 100, 500} {
		b.Run(fmt.Sprintf("nodes-%d", count), func(b *testing.B) {
			ctx := context.Background()
			db, err := Open(ctx, filepath.Join(b.TempDir(), "policies.db"))
			if err != nil {
				b.Fatal(err)
			}
			defer db.Close()
			for i := 0; i < count; i++ {
				if _, _, err := db.CreateNode(ctx, CreateNodeParams{Name: fmt.Sprintf("fixture-%d", i), Tags: []string{"prod"}}); err != nil {
					b.Fatal(err)
				}
			}
			channel, err := db.CreateNotificationChannel(ctx, "fixture", ChannelKindWebhook, "synthetic")
			if err != nil {
				b.Fatal(err)
			}
			for i := 0; i < 10; i++ {
				_, err := db.SaveAlertPolicy(ctx, AlertPolicy{Policy: alertpolicy.Policy{Key: fmt.Sprintf("cpu-%d", i), Enabled: true, Scope: alertpolicy.Scope{Kind: "tags", Tags: []string{"prod"}, TagMode: "all"}}, Name: "fixture", ChannelID: channel.ID, Kind: "cpu", Config: json.RawMessage(`{"threshold_percent":90}`), CooldownSeconds: 900})
				if err != nil {
					b.Fatal(err)
				}
			}
			now := time.Now().UTC()
			if _, err := db.PrepareAlertPolicyRules(ctx, now); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				if mapped, err := db.PrepareAlertPolicyRules(ctx, now.Add(time.Duration(i+1)*time.Second)); err != nil || mapped != 0 {
					b.Fatalf("preparation: %d %v", mapped, err)
				}
			}
			b.StopTimer()
			var bindings, rules int
			if err := db.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_policy_rule_bindings`).Scan(&bindings); err != nil {
				b.Fatal(err)
			}
			if err := db.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM alert_rules`).Scan(&rules); err != nil {
				b.Fatal(err)
			}
			if bindings != count*10 || rules != count*10 {
				b.Fatalf("benchmark changed rule population: %d bindings, %d rules", bindings, rules)
			}
			b.ReportMetric(float64(bindings), "bindings")
		})
	}
}

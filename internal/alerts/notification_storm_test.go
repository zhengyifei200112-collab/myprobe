package alerts

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/alertpolicy"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

// Opt-in receiver measurement, not a full-system capacity or percentile claim.
func TestNotificationStormMeasurement(t *testing.T) {
	if os.Getenv("MYPROBE_MEASURE_NOTIFICATION_STORM") != "1" {
		t.Skip("opt in to real receiver storm measurement")
	}
	window := 0
	if raw := os.Getenv("MYPROBE_STORM_AGGREGATION_SECONDS"); raw != "" {
		var err error
		window, err = strconv.Atoi(raw)
		if err != nil || window < 0 || window > 300 || (window > 0 && window < 5) {
			t.Fatal("invalid measurement aggregation window")
		}
	}
	for _, nodes := range []int{5, 50, 100} {
		for sample := 0; sample < 3; sample++ {
			t.Run(fmt.Sprintf("nodes%d/sample%d", nodes, sample), func(t *testing.T) {
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				db, err := store.Open(ctx, filepath.Join(t.TempDir(), "storm.db"))
				if err != nil {
					t.Fatal(err)
				}
				defer db.Close()
				var calls, bytes atomic.Int64
				receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					raw, err := io.ReadAll(r.Body)
					if err != nil {
						t.Error(err)
					}
					var n Notification
					if err := json.Unmarshal(raw, &n); err != nil {
						t.Error(err)
					}
					if n.State != "firing" || r.Header.Get("Idempotency-Key") == "" {
						t.Error("invalid notification")
					}
					calls.Add(1)
					bytes.Add(int64(len(raw)))
					w.WriteHeader(204)
				}))
				defer receiver.Close()
				svc := New(db, "synthetic-storm-key-0123456789abcdef", NewHTTPSender(receiver.Client()), nil)
				channel, err := svc.CreateChannel(ctx, "loopback receiver", "webhook", ChannelConfig{URL: receiver.URL})
				if err != nil {
					t.Fatal(err)
				}
				at := time.Now().UTC().Truncate(time.Millisecond)
				expires := at.Add(time.Hour)
				for n := 0; n < nodes; n++ {
					node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: fmt.Sprintf("fixture-%03d", n)})
					if err != nil {
						t.Fatal(err)
					}
					updateNodeExpiry(t, db, node, &expires)
				}
				if _, err := svc.SavePolicy(ctx, store.AlertPolicy{Policy: alertpolicy.Policy{Key: "storm-expiry", Enabled: true, Scope: alertpolicy.Scope{Kind: "all"}}, Name: "storm expiry", ChannelID: channel.ID, Kind: "expiry", Config: json.RawMessage(fmt.Sprintf(`{"days_before":1,"recovery_seconds":0,"aggregation_seconds":%d}`, window)), CooldownSeconds: 900}); err != nil {
					t.Fatal(err)
				}
				started := time.Now()
				if err := svc.Tick(ctx, at); err != nil {
					t.Fatal(err)
				}
				enqueue := time.Since(started)
				started = time.Now()
				for {
					worked, err := svc.DeliverOne(ctx, at.Add(time.Duration(window)*time.Second))
					if err != nil {
						t.Fatal(err)
					}
					if !worked {
						break
					}
				}
				drain := time.Since(started)
				want := nodes
				if window > 0 {
					want = (nodes + store.AggregateMemberLimit - 1) / store.AggregateMemberLimit
				}
				if calls.Load() != int64(want) {
					t.Fatalf("receiver count %d want %d", calls.Load(), want)
				}
				t.Logf("STORM nodes=%d sample=%d window_seconds=%d logical_wait_ms=%d enqueue_ms=%.3f drain_ms=%.3f requests=%d body_bytes=%d", nodes, sample, window, window*1000, float64(enqueue.Microseconds())/1000, float64(drain.Microseconds())/1000, calls.Load(), bytes.Load())
			})
		}
	}
}

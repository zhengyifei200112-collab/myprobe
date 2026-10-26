package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/alerts"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestLiveNotificationAggregationBrowser(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_AGGREGATION_BROWSER") != "1" {
		t.Skip("requires Playwright/Edge; opt in with MYPROBE_TEST_AGGREGATION_BROWSER=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "browser.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key, password := rand.Text()+rand.Text(), rand.Text()
	authService := auth.New(db, time.Hour)
	if _, err := authService.Bootstrap(ctx, "browser-admin", password); err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var bodies, keys []string
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		mu.Lock()
		bodies = append(bodies, string(raw))
		keys = append(keys, r.Header.Get("Idempotency-Key"))
		count := len(bodies)
		mu.Unlock()
		if count == 1 {
			conn, _, err := w.(http.Hijacker).Hijack()
			if err != nil {
				t.Error(err)
				return
			}
			conn.Close()
			return
		} // Received, but the sender loses the result.
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	svc := alerts.New(db, key, alerts.NewHTTPSender(receiver.Client()), nil, origin)
	channel, err := svc.CreateChannel(ctx, "Aggregate receiver", "webhook", alerts.ChannelConfig{URL: receiver.URL})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"Aggregate node A", `<img src=x onerror="window.aggregateExecuted=1">`} {
		node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: name})
		if err != nil {
			t.Fatal(err)
		}
		expiry := time.Now().Add(time.Hour)
		if _, err := db.UpdateNode(ctx, node.ID, store.UpdateNodeParams{Name: name, ExpiresAt: &expiry, LatencyMode: "ping", CollectionSeconds: 5, ReportSeconds: 5}); err != nil {
			t.Fatal(err)
		}
	}
	hub := agentgateway.NewHub()
	api := NewWithAlertService(config.Config{EncryptionKey: key, NotificationOrigin: origin}, db, authService, agentgateway.New(db, hub), hub, svc)
	server.Config.Handler = api.Handler()
	server.Start()
	defer server.Close()
	done := make(chan struct{})
	go func() {
		defer close(done)
		queued := false
		closed := false
		for ctx.Err() == nil {
			if !queued {
				policies, err := db.ListAlertPolicies(ctx)
				if err != nil {
					t.Error(err)
					return
				}
				for _, p := range policies {
					var c struct {
						Seconds int `json:"aggregation_seconds"`
					}
					if err := json.Unmarshal(p.Config, &c); err != nil {
						t.Error(err)
						return
					}
					if c.Seconds == 5 {
						if err := svc.Tick(ctx, time.Now().UTC()); err != nil {
							t.Error(err)
							return
						}
						queued = true
						break
					}
				}
			}
			if queued {
				if !closed {
					policies, err := db.ListAlertPolicies(ctx)
					if err != nil {
						t.Error(err)
						return
					}
					for _, p := range policies {
						if !p.Enabled {
							if err := svc.Tick(ctx, time.Now().UTC()); err != nil {
								t.Error(err)
								return
							}
							closed = true
							break
						}
					}
				}
				if _, err := svc.DeliverOne(ctx, time.Now().UTC()); err != nil && ctx.Err() == nil {
					t.Error(err)
					return
				}
			}
			select {
			case <-time.After(50 * time.Millisecond):
			case <-ctx.Done():
				return
			}
		}
	}()
	defer func() { cancel(); <-done }()
	command := exec.CommandContext(ctx, "node", "../../scripts/notification-aggregation-live-ui.cjs")
	command.Env = append(os.Environ(), "AGG_BROWSER_URL="+origin, "AGG_BROWSER_PASSWORD="+password, "AGG_BROWSER_CHANNEL="+channel.ID)
	output, err := command.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 2 || keys[0] == "" || keys[0] != keys[1] || bodies[0] != bodies[1] {
		t.Fatalf("real ambiguous send/retry mismatch: requests=%d", len(bodies))
	}
	var n alerts.Notification
	if err := json.Unmarshal([]byte(bodies[0]), &n); err != nil {
		t.Fatal(err)
	}
	if len(n.Members) != 2 || n.GroupPath == "" || n.SnapshotID == "" || n.AggregateID == "" {
		t.Fatal(fmt.Sprintf("actual aggregate missing references: %+v", n))
	}
}

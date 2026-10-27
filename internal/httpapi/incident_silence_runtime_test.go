package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/alerts"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

type silenceRuntime struct {
	db             *store.Store
	svc            *alerts.Service
	handler        http.Handler
	node           store.Node
	incident       store.Incident
	cookie         *http.Cookie
	csrf, password string
	receipts       chan alerts.Notification
}

func newSilenceRuntime(t *testing.T) *silenceRuntime {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "runtime.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	f := &silenceRuntime{db: db, password: rand.Text(), receipts: make(chan alerts.Notification, 8)}
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
		}
		for _, forbidden := range []string{"private-silence-note", "creator_name", "actor_name", "acknowledgement"} {
			if strings.Contains(string(raw), forbidden) {
				t.Errorf("private field in notification: %s", forbidden)
			}
		}
		var n alerts.Notification
		if err := json.Unmarshal(raw, &n); err != nil {
			t.Error(err)
		}
		if r.Header.Get("Idempotency-Key") == "" {
			t.Error("missing delivery key")
		}
		select {
		case f.receipts <- n:
		default:
			t.Error("notification storm")
		}
		w.WriteHeader(204)
	}))
	t.Cleanup(receiver.Close)
	key := rand.Text() + rand.Text()
	f.svc = alerts.New(db, key, alerts.NewHTTPSender(receiver.Client()), nil)
	a := auth.New(db, time.Hour)
	if _, err := a.Bootstrap(ctx, "browser-admin", f.password); err != nil {
		t.Fatal(err)
	}
	hub := agentgateway.NewHub()
	f.handler = NewWithAlertService(config.Config{EncryptionKey: key}, db, a, agentgateway.New(db, hub), hub, f.svc).Handler()
	body, _ := json.Marshal(map[string]string{"username": "browser-admin", "password": f.password})
	r := httptest.NewRecorder()
	f.handler.ServeHTTP(r, httptest.NewRequest("POST", "/api/v1/auth/login", strings.NewReader(string(body))))
	var credentials struct {
		CSRFToken string `json:"csrf_token"`
	}
	if err := json.Unmarshal(r.Body.Bytes(), &credentials); err != nil || r.Code != 200 {
		t.Fatalf("login: %d %v", r.Code, err)
	}
	f.cookie = r.Result().Cookies()[0]
	f.csrf = credentials.CSRFToken
	f.node, _, err = db.CreateNode(ctx, store.CreateNodeParams{Name: "Silence node"})
	if err != nil {
		t.Fatal(err)
	}
	f.setExpiry(t, time.Now().UTC().Add(time.Hour))
	channel, err := f.svc.CreateChannel(ctx, "local receiver", "webhook", alerts.ChannelConfig{URL: receiver.URL})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.svc.CreateRule(ctx, f.node.ID, channel.ID, "expiry", alerts.RuleConfig{DaysBefore: 1}, 300); err != nil {
		t.Fatal(err)
	}
	if err := f.svc.Tick(ctx, time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	items, err := db.ListIncidents(ctx, store.IncidentFilter{Limit: 10})
	if err != nil || len(items) != 1 {
		t.Fatal(err)
	}
	f.incident = items[0]
	return f
}
func (f *silenceRuntime) setExpiry(t *testing.T, at time.Time) {
	t.Helper()
	if _, err := f.db.UpdateNode(context.Background(), f.node.ID, store.UpdateNodeParams{Name: f.node.Name, ExpiresAt: &at, CollectionSeconds: 5, ReportSeconds: 5, LatencyMode: "ping"}); err != nil {
		t.Fatal(err)
	}
}
func (f *silenceRuntime) call(t *testing.T, method, path, body string) *httptest.ResponseRecorder {
	return authenticatedRequest(t, f.handler, f.cookie, f.csrf, method, path, body)
}

func TestSilenceAPIWithRealEvaluatorAndReceiver(t *testing.T) {
	for _, recoverInside := range []bool{false, true} {
		t.Run(map[bool]string{false: "expire-continuing", true: "recover-inside"}[recoverInside], func(t *testing.T) {
			f := newSilenceRuntime(t)
			ctx := context.Background()
			path := "/api/v1/admin/incidents/" + f.incident.ID + "/silences"
			r := f.call(t, "POST", path, `{"request_id":"runtime-silence","duration_seconds":60,"reason":"private-silence-note","acknowledge":true}`)
			var first store.SilenceMutationResult
			if err := json.Unmarshal(r.Body.Bytes(), &first); err != nil || r.Code != 201 {
				t.Fatalf("create: %d %s %v", r.Code, r.Body.String(), err)
			}
			if recoverInside {
				f.setExpiry(t, first.StartsAt.Add(72*time.Hour))
			}
			if err := f.svc.Tick(ctx, first.StartsAt.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			if worked, err := f.svc.DeliverOne(ctx, first.StartsAt.Add(time.Second)); err != nil || worked {
				t.Fatalf("muted delivery: %v %v", worked, err)
			}
			if err := f.svc.Tick(ctx, first.EndsAt.Add(time.Second)); err != nil {
				t.Fatal(err)
			}
			if worked, err := f.svc.DeliverOne(ctx, first.EndsAt.Add(time.Second)); err != nil || worked == recoverInside {
				t.Fatalf("expiry delivery: %v %v", worked, err)
			}
			if recoverInside {
				p, err := f.db.ListIncidentSilences(ctx, f.incident.ID, "", 10, first.EndsAt.Add(time.Second))
				if err != nil || len(p.Silences) != 1 || p.Silences[0].ClosureReason != "recovered" {
					t.Fatalf("history: %+v %v", p, err)
				}
				if len(f.receipts) != 0 {
					t.Fatal("catch-up recovery notification")
				}
				f.setExpiry(t, first.EndsAt.Add(time.Hour))
				if err := f.svc.Tick(ctx, first.EndsAt.Add(2*time.Second)); err != nil {
					t.Fatal(err)
				}
				if worked, err := f.svc.DeliverOne(ctx, first.EndsAt.Add(2*time.Second)); err != nil || !worked {
					t.Fatal("new episode inherited silence")
				}
			}
			select {
			case got := <-f.receipts:
				if got.State != "firing" || (got.IncidentID == f.incident.ID) == recoverInside {
					t.Fatalf("actual receipt: %+v", got)
				}
			case <-time.After(time.Second):
				t.Fatal("missing real receipt")
			}
			if len(f.receipts) != 0 {
				t.Fatal("extra receipts")
			}
		})
	}
}

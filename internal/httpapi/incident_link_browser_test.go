package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/alerts"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestLiveSingleIncidentLinkBrowser(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_AGGREGATION_BROWSER") != "1" {
		t.Skip("requires Playwright/Edge; opt in with MYPROBE_TEST_AGGREGATION_BROWSER=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "single.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key, password := rand.Text()+rand.Text(), rand.Text()
	authService := auth.New(db, time.Hour)
	if _, err := authService.Bootstrap(ctx, "single-admin", password); err != nil {
		t.Fatal(err)
	}
	received := make(chan alerts.Notification, 1)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var n alerts.Notification
		if err := json.NewDecoder(r.Body).Decode(&n); err != nil {
			w.WriteHeader(400)
			return
		}
		received <- n
		w.WriteHeader(204)
	}))
	defer receiver.Close()
	// The real listener address is configured before creating the queued payload.
	server := httptest.NewUnstartedServer(nil)
	origin := "http://" + server.Listener.Addr().String()
	svc := alerts.New(db, key, alerts.NewHTTPSender(receiver.Client()), nil, origin)
	c, err := svc.CreateChannel(ctx, "single webhook", "webhook", alerts.ChannelConfig{URL: receiver.URL})
	if err != nil {
		t.Fatal(err)
	}
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "Single linked node"})
	if err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	expires := at.Add(time.Hour)
	if _, err := db.UpdateNode(ctx, node.ID, store.UpdateNodeParams{Name: node.Name, ExpiresAt: &expires, CollectionSeconds: 5, ReportSeconds: 5, LatencyMode: "ping"}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.CreateRule(ctx, node.ID, c.ID, "expiry", alerts.RuleConfig{DaysBefore: 1}, 300); err != nil {
		t.Fatal(err)
	}
	if err := svc.Tick(ctx, at); err != nil {
		t.Fatal(err)
	}
	if worked, err := svc.DeliverOne(ctx, at); err != nil || !worked {
		t.Fatalf("delivery: %v %v", worked, err)
	}
	var n alerts.Notification
	select {
	case n = <-received:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if n.AggregateID != "" || n.IncidentID == "" || n.IncidentPath != origin+"/admin?incident="+n.IncidentID {
		t.Fatal("single receiver missing original incident link")
	}
	hub := agentgateway.NewHub()
	server.Config.Handler = NewWithAlertService(config.Config{EncryptionKey: key}, db, authService, agentgateway.New(db, hub), hub, svc).Handler()
	server.Start()
	defer server.Close()
	cmd := exec.CommandContext(ctx, "node", "../../scripts/single-incident-link-ui.cjs")
	cmd.Env = append(os.Environ(), "SINGLE_LINK_URL="+n.IncidentPath, "SINGLE_LINK_PASSWORD="+password)
	output, err := cmd.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal(err)
	}
}

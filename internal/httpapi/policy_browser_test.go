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

func TestLivePolicyBrowserLifecycle(t *testing.T) {
	if os.Getenv("MYPROBE_TEST_POLICY_BROWSER") != "1" {
		t.Skip("requires Playwright and Edge; opt in with MYPROBE_TEST_POLICY_BROWSER=1")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "browser.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	messages := make(chan alerts.Notification, 8)
	receiver := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var notification alerts.Notification
		if err := json.NewDecoder(r.Body).Decode(&notification); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		select {
		case messages <- notification:
		default:
			t.Error("unexpected notification volume")
		}
		w.WriteHeader(http.StatusNoContent)
	}))
	defer receiver.Close()
	key, password := rand.Text()+rand.Text(), rand.Text()
	authService := auth.New(db, time.Hour)
	if _, err = authService.Bootstrap(ctx, "browser-admin", password); err != nil {
		t.Fatal(err)
	}
	runtimeAlerts := alerts.New(db, key, alerts.NewHTTPSender(receiver.Client()), nil)
	channel, err := runtimeAlerts.CreateChannel(ctx, "Browser receiver", "webhook", alerts.ChannelConfig{URL: receiver.URL})
	if err != nil {
		t.Fatal(err)
	}
	node, _, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "Browser node", Tags: []string{"browser"}})
	if err != nil {
		t.Fatal(err)
	}
	expires := time.Now().Add(time.Hour)
	if _, err = db.UpdateNode(ctx, node.ID, store.UpdateNodeParams{Name: node.Name, Tags: node.Tags, ExpiresAt: &expires, LatencyMode: "ping", CollectionSeconds: 5, ReportSeconds: 5}); err != nil {
		t.Fatal(err)
	}
	hub := agentgateway.NewHub()
	api := NewWithAlertService(config.Config{EncryptionKey: key}, db, authService, agentgateway.New(db, hub), hub, runtimeAlerts)
	server := httptest.NewServer(api.Handler())
	defer server.Close()
	done := make(chan struct{})
	go func() { defer close(done); runtimeAlerts.Run(ctx) }()
	defer func() { cancel(); <-done }()
	command := exec.CommandContext(ctx, "node", "../../scripts/alert-policies-live-ui.cjs")
	command.Env = append(os.Environ(), "POLICY_BROWSER_URL="+server.URL, "POLICY_BROWSER_PASSWORD="+password, "POLICY_BROWSER_CHANNEL="+channel.ID)
	output, err := command.CombinedOutput()
	t.Log(string(output))
	if err != nil {
		t.Fatal(err)
	}
	select {
	case message := <-messages:
		if message.State != "firing" || message.NodeID != node.ID {
			t.Fatalf("unexpected notification: %+v", message)
		}
	default:
		t.Fatal("browser lifecycle produced no actual webhook")
	}
	select {
	case <-messages:
		t.Fatal("disable sent duplicate or recovery notification")
	default:
	}
}

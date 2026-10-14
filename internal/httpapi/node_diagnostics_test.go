package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/zhengyifei200112-collab/myprobe/internal/agentgateway"
	"github.com/zhengyifei200112-collab/myprobe/internal/auth"
	"github.com/zhengyifei200112-collab/myprobe/internal/config"
	protocol "github.com/zhengyifei200112-collab/myprobe/internal/protocol/v1"
	"github.com/zhengyifei200112-collab/myprobe/internal/store"
)

func TestNodeDiagnosticsEvidenceAndAccess(t *testing.T) {
	ctx := context.Background()
	db, err := store.Open(ctx, filepath.Join(t.TempDir(), "nodes.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	node, agentToken, err := db.CreateNode(ctx, store.CreateNodeParams{Name: "test"})
	if err != nil {
		t.Fatal(err)
	}
	a := auth.New(db, time.Hour)
	if _, err := a.Bootstrap(ctx, "admin", "diagnostic-test-password"); err != nil {
		t.Fatal(err)
	}
	_, token, err := a.Login(ctx, "admin", "diagnostic-test-password")
	if err != nil {
		t.Fatal(err)
	}
	hub := agentgateway.NewHub()
	s := New(config.Config{}, db, a, agentgateway.New(db, hub), hub)
	request := func(id, session string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(http.MethodGet, "/api/v1/admin/nodes/"+id+"/diagnostics", nil)
		if session != "" {
			r.AddCookie(&http.Cookie{Name: sessionCookie, Value: session})
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		if w.Header().Get("Cache-Control") != "private, no-store" {
			t.Fatal("cache policy absent")
		}
		return w
	}
	if request(node.ID, "").Code != http.StatusUnauthorized || request(node.ID, agentToken).Code != http.StatusUnauthorized {
		t.Fatal("non-admin access accepted")
	}
	if request("missing", token).Code != http.StatusNotFound {
		t.Fatal("missing node not distinguished")
	}
	var body struct {
		Node      store.NodeDiagnostics `json:"node"`
		Connected bool                  `json:"websocket_connected"`
	}
	initial := request(node.ID, token)
	if initial.Code != http.StatusOK {
		t.Fatalf("status %d", initial.Code)
	}
	if err := json.Unmarshal(initial.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Node.AgentEvidenceStatus != "unavailable" || body.Node.LastSeenAt != nil || body.Connected {
		t.Fatal("invented node evidence")
	}
	hello := protocol.Hello{AgentVersion: "test-version", Hostname: "private-hostname", MachineID: "private-machine-id", OS: "linux", Architecture: "amd64", Capabilities: []string{"metrics.v1"}, CollectionSeconds: 5, ReportSeconds: 5}
	if err := db.SaveAgentMetadata(ctx, node.ID, hello); err != nil {
		t.Fatal(err)
	}
	actual := request(node.ID, token)
	if err := json.Unmarshal(actual.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Node.AgentEvidenceStatus != "advertised" || body.Node.AgentVersion != "test-version" || body.Node.HelloReceivedAt == nil || len(body.Node.Capabilities) != 1 || body.Connected {
		t.Fatal("handshake incorrectly projected")
	}
	for _, secret := range []string{"private-hostname", "private-machine-id", token, agentToken} {
		if strings.Contains(actual.Body.String(), secret) {
			t.Fatal("private diagnostic field leaked")
		}
	}
	if err := a.Logout(ctx, token); err != nil {
		t.Fatal(err)
	}
	if request(node.ID, token).Code != http.StatusUnauthorized {
		t.Fatal("revoked session accepted")
	}
}
